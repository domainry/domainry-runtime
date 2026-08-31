package agenthost

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	"go.opentelemetry.io/otel/attribute"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentmodulehost "github.com/domainry/domainry-agent-sdk/modulehost"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/telemetry"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// InvokeInteractiveHost executes only the Runtime-owned authorization and
// business effect. Agent records the returned evidence in its own run state.
func (g *AgentToolGateway) InvokeInteractiveHost(ctx context.Context, request agentmodulehost.InteractiveToolInvocationRequest) (agentmodulehost.InteractiveToolInvocationResult, error) {
	result, err := g.invokeInteractive(ctx, request)
	return agentmodulehost.InteractiveToolInvocationResult{
		Tool: result.Tool, Status: result.Status,
		Output: result.Output, Proposal: result.Proposal,
		Authorization: result.Authorization,
	}, err
}

func (g *AgentToolGateway) invokeInteractive(ctx context.Context, request agentmodulehost.InteractiveToolInvocationRequest) (result AgentToolInvocationResult, err error) {
	ctx, span := telemetry.StartUseCase(ctx, "agent.interactive.tool.invoke", attribute.String("workspace.id", request.Principal.WorkspaceID), attribute.String("agent.session", request.SessionID), attribute.String("agent.interactive_run_id", request.RunID), attribute.String("agent.entrypoint", request.EntrypointKey), attribute.String("route.key", request.RouteKey), attribute.String("correlation.id", request.CorrelationID))
	defer func() { telemetry.EndUseCase(span, err, result.Status) }()
	if g == nil || g.dependencies.Authorization == nil {
		return result, apperror.New(apperror.KindUnavailable, "agent.tool.gateway_unavailable", nil, nil)
	}
	principal := runtimeHostPrincipal(request.Principal)
	authorized, err := g.dependencies.Authorization.AuthorizeInteractive(ctx, request.Context, principal)
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
	input, marshalErr := json.Marshal(request.Route.Input)
	maxInput := authorized.Agent.ExecutionLimits.MaxInputBytes
	if maxInput <= 0 {
		maxInput = 64 * 1024
	}
	if marshalErr != nil || len(input) > maxInput {
		return result, apperror.New(apperror.KindBadRequest, "agent.tool.input_invalid", marshalErr, nil)
	}
	if g.dependencies.RateLimiter != nil {
		decision, rateErr := g.dependencies.RateLimiter.Allow(ctx, "agent_interactive_tool:"+request.Principal.WorkspaceID+":"+request.RunID, maxCalls, time.Minute)
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
	invocation := AgentToolInvocationResult{Tool: tool, Authorization: authorizationEvidence}
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
		objectKey, recordID := agentToolString(request.Route.Input, "object_key"), agentToolString(request.Route.Input, "record_id")
		if objectKey != authorized.Context.ObjectKey {
			return result, apperror.New(apperror.KindForbidden, "agent.tool.object_denied", nil, nil)
		}
		invocation.Proposal = agentProposalDraft(actionKey, objectKey, recordID, agentToolMap(request.Route.Input["data"]), "agent_interactive", request.RunID, map[string]any{
			"interactive_run_id": request.RunID, "session_id": request.SessionID, "entrypoint_key": request.EntrypointKey,
			"route_key": request.RouteKey, "context_revision": request.Context.ContextRevision, "idempotency_key": request.IdempotencyKey,
		})
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
	return invocation, nil
}

func runtimeHostPrincipal(principal agentmodulehost.Principal) principalmodel.Principal {
	return principalmodel.Principal{Principal: identitysdk.Principal{
		Known: principal.Known, WorkspaceID: principal.WorkspaceID, UserID: principal.UserID,
		RoleKey: principal.RoleKey, AuthorizationRevision: principal.AuthorizationRevision,
	}, RequestID: principal.RequestID, CorrelationID: principal.CorrelationID, CausationID: principal.CausationID}
}

func interactiveAuthorizationEvidence(authorization AgentInteractiveAuthorization) agentmodel.AgentAuthorizationEvidence {
	return agentmodel.AgentAuthorizationEvidence{Decision: "allow", Code: "agent.authorization.interactive_tool_allowed", PolicyRevision: agentStableHash(map[string]any{"context_revision": authorization.Context.ContextRevision, "authorization_revision": authorization.Principal.AuthorizationRevision, "tools": authorization.AllowedTools, "actions": authorization.AllowedActions, "fields": authorization.VisibleFields}), ContextRevision: authorization.Context.ContextRevision, AuthorizationRevision: authorization.Principal.AuthorizationRevision, AllowedObjects: []string{authorization.Context.ObjectKey}, AllowedActions: authorization.AllowedActions, AllowedTools: authorization.AllowedTools}
}
