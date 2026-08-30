package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	"go.opentelemetry.io/otel/attribute"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/telemetry"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type AgentInteractiveToolInvocationRequest struct {
	Run            agentmodel.AgentInteractiveRun
	Route          agentsdk.RouteResult
	IdempotencyKey string
}

type AgentInteractiveToolInvocationResult struct {
	Run        agentmodel.AgentInteractiveRun
	Invocation AgentToolInvocationResult
}

func (g *AgentToolGateway) InvokeInteractive(ctx context.Context, request AgentInteractiveToolInvocationRequest) (result AgentInteractiveToolInvocationResult, err error) {
	ctx, span := telemetry.StartUseCase(ctx, "agent.interactive.tool.invoke", attribute.String("workspace.id", request.Run.WorkspaceID), attribute.String("agent.session", request.Run.SessionID), attribute.String("agent.interactive_run_id", request.Run.ID), attribute.String("agent.entrypoint", request.Run.EntrypointKey), attribute.String("product.surface", request.Run.Surface), attribute.String("route.key", request.Run.RouteKey), attribute.String("correlation.id", request.Run.CorrelationID))
	defer func() {
		telemetry.EndUseCase(span, err, result.Invocation.Status)
		if err != nil && apperror.KindOf(err) == apperror.KindForbidden {
			g.dependencies.InteractiveRuns.ObservePermissionDenied(ctx)
		}
	}()
	if g == nil || g.dependencies.Authorization == nil || g.dependencies.InteractiveRuns == nil {
		return result, apperror.New(apperror.KindUnavailable, "agent.tool.gateway_unavailable", nil, nil)
	}
	run := request.Run
	if run.Status != agentmodel.AgentInteractiveRunRunning || run.Context.ContextRevision != run.ContextRevision || run.Context.Principal.UserID != run.UserID || run.Context.Principal.RoleKey != run.RoleKey {
		return result, apperror.New(apperror.KindForbidden, "agent.interactive.tool_scope_denied", nil, nil)
	}
	principal := interactiveRunPrincipal(run)
	authorized, err := g.dependencies.Authorization.AuthorizeInteractive(ctx, run.Context, principal)
	if err != nil {
		return result, err
	}
	tool := strings.TrimSpace(agentToolString(request.Route.Input, "tool"))
	if request.Route.RouteType == agentsdk.AgentRouteProposal {
		tool = AgentToolInvokeAction
	}
	if !agentContains(authorized.AllowedTools, tool) {
		return result, apperror.New(apperror.KindForbidden, "agent.tool.not_allowed", nil, nil)
	}
	maxCalls := authorized.Agent.ExecutionLimits.MaxToolCalls
	if maxCalls <= 0 {
		maxCalls = 20
	}
	if run.ToolCallCount >= maxCalls {
		return result, apperror.New(apperror.KindRateLimited, "agent.task.tool_call_limit", nil, nil)
	}
	usedCost := 0
	for _, invocation := range run.ToolInvocations {
		usedCost += invocation.CostUnits
	}
	if usedCost+agentToolCostUnits(tool) > agentTaskCostBudgetUnits(authorized.Agent.ExecutionLimits.CostBudget) {
		return result, apperror.New(apperror.KindRateLimited, "agent.task.cost_budget_exceeded", nil, nil)
	}
	input, marshalErr := json.Marshal(request.Route.Input)
	maxInput := authorized.Agent.ExecutionLimits.MaxInputBytes
	if maxInput <= 0 {
		maxInput = 64 * 1024
	}
	if marshalErr != nil || len(input) > maxInput {
		return result, apperror.New(apperror.KindBadRequest, "agent.tool.input_invalid", marshalErr, nil)
	}
	if g.dependencies.RateLimiter != nil {
		decision, rateErr := g.dependencies.RateLimiter.Allow(ctx, "agent_interactive_tool:"+run.WorkspaceID+":"+run.ID, maxCalls, time.Minute)
		if rateErr != nil {
			return result, rateErr
		}
		if !decision.Allowed {
			return result, apperror.New(apperror.KindRateLimited, "agent.tool.rate_limited", nil, nil)
		}
	}
	timeout := time.Duration(authorized.Agent.ExecutionLimits.TimeoutSeconds) * time.Second
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	authorizationEvidence := interactiveAuthorizationEvidence(authorized)
	started := time.Now().UTC()
	invocation := AgentToolInvocationResult{Tool: tool, CallRef: fmt.Sprintf("agent_interactive_tool_%s_%d", run.ID, run.ToolCallCount+1), Authorization: authorizationEvidence}
	switch tool {
	case AgentToolQueryRecords:
		if g.dependencies.Queries == nil {
			return result, apperror.New(apperror.KindUnavailable, "agent.tool.query_unavailable", nil, nil)
		}
		objectKey := agentToolString(request.Route.Input, "object_key")
		if objectKey == "" || objectKey != authorized.Context.ObjectKey {
			return result, apperror.New(apperror.KindForbidden, "agent.tool.object_denied", nil, nil)
		}
		invocation.Output, err = g.dependencies.Queries.QueryAgentRecords(ctx, objectKey, agentToolMap(request.Route.Input["query"]), AgentToolFieldScope{VisibleFields: authorized.VisibleFields}, authorized.Principal)
		invocation.Status = "executed"
	case AgentToolGetRecord:
		if g.dependencies.Queries == nil {
			return result, apperror.New(apperror.KindUnavailable, "agent.tool.query_unavailable", nil, nil)
		}
		objectKey, recordID := agentToolString(request.Route.Input, "object_key"), agentToolString(request.Route.Input, "record_id")
		if objectKey == "" || objectKey != authorized.Context.ObjectKey || recordID == "" {
			return result, apperror.New(apperror.KindForbidden, "agent.tool.record_denied", nil, nil)
		}
		invocation.Output, err = g.dependencies.Queries.GetAgentRecord(ctx, objectKey, recordID, AgentToolFieldScope{VisibleFields: authorized.VisibleFields}, authorized.Principal)
		invocation.Status = "executed"
	case AgentToolInvokeAction:
		actionKey := strings.TrimSpace(request.Route.TargetKey)
		if request.Route.RouteType != agentsdk.AgentRouteProposal || !agentContains(authorized.AllowedActions, actionKey) || strings.TrimSpace(request.IdempotencyKey) == "" {
			return result, apperror.New(apperror.KindForbidden, "agent.tool.action_denied", nil, nil)
		}
		if g.dependencies.Proposals == nil {
			return result, apperror.New(apperror.KindUnavailable, "agent.tool.proposal_unavailable", nil, nil)
		}
		objectKey, recordID := agentToolString(request.Route.Input, "object_key"), agentToolString(request.Route.Input, "record_id")
		if objectKey != authorized.Context.ObjectKey {
			return result, apperror.New(apperror.KindForbidden, "agent.tool.object_denied", nil, nil)
		}
		var proposal AgentToolProposalResult
		proposal, err = g.dependencies.Proposals.CreateAgentActionProposal(ctx, AgentToolProposalRequest{
			ActionKey: actionKey, ObjectKey: objectKey, RecordID: recordID, Input: agentToolMap(request.Route.Input["data"]), IdempotencyKey: request.IdempotencyKey,
			InteractiveRunID: run.ID, SessionID: run.SessionID, EntrypointKey: run.EntrypointKey, Surface: run.Surface, RouteKey: run.RouteKey, ContextRevision: run.ContextRevision,
			Principal: authorized.Principal, Identity: agentsdk.ExecutionIdentity{Mode: agentsdk.AgentTaskIdentityInherit, Initiator: run.Context.Principal, Execution: run.Context.Principal},
		})
		invocation.Proposal = proposal.Value
		invocation.Status = "proposal_required"
	default:
		return result, apperror.New(apperror.KindForbidden, "agent.tool.not_allowed", nil, nil)
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			err = apperror.New(apperror.KindUnavailable, "agent.tool.timeout", err, nil)
		}
		return result, err
	}
	encoded, encodeErr := json.Marshal(map[string]any{"output": invocation.Output, "proposal": invocation.Proposal})
	maxOutput := authorized.Agent.ExecutionLimits.MaxOutputBytes
	if maxOutput <= 0 {
		maxOutput = 64 * 1024
	}
	if encodeErr != nil || len(encoded) > maxOutput {
		return result, apperror.New(apperror.KindBadRequest, "agent.tool.output_invalid", encodeErr, nil)
	}
	finished := time.Now().UTC()
	evidence := agentmodel.AgentTaskToolInvocationEvidence{Ref: invocation.CallRef, Tool: tool, InputHash: agentStableHash(request.Route.Input), OutputHash: agentStableHash(map[string]any{"output": invocation.Output, "proposal": invocation.Proposal}), Status: invocation.Status, Authorization: authorizationEvidence, StartedAt: started, FinishedAt: &finished, DurationMilliseconds: finished.Sub(started).Milliseconds(), CostUnits: agentToolCostUnits(tool)}
	updated, err := g.dependencies.InteractiveRuns.RecordToolInvocation(ctx, run, evidence)
	if err != nil {
		return result, err
	}
	return AgentInteractiveToolInvocationResult{Run: updated, Invocation: invocation}, nil
}

func interactiveRunPrincipal(run agentmodel.AgentInteractiveRun) principalmodel.Principal {
	return principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: run.WorkspaceID, UserID: run.UserID, RoleKey: run.RoleKey, AuthorizationRevision: run.Context.Principal.AuthorizationRevision}, SurfaceKey: run.Surface, CorrelationID: run.CorrelationID}
}

func interactiveAuthorizationEvidence(authorization AgentInteractiveAuthorization) agentmodel.AgentAuthorizationEvidence {
	return agentmodel.AgentAuthorizationEvidence{Decision: "allow", Code: "agent.authorization.interactive_tool_allowed", PolicyRevision: agentStableHash(map[string]any{"context_revision": authorization.Context.ContextRevision, "authorization_revision": authorization.Principal.AuthorizationRevision, "tools": authorization.AllowedTools, "actions": authorization.AllowedActions, "fields": authorization.VisibleFields}), ContextRevision: authorization.Context.ContextRevision, AuthorizationRevision: authorization.Principal.AuthorizationRevision, AllowedObjects: []string{authorization.Context.ObjectKey}, AllowedActions: authorization.AllowedActions, AllowedTools: authorization.AllowedTools}
}
