package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/telemetry"
	agentcatalog "github.com/domainry/domainry-runtime/runtime/domain/agent/contract/catalog"
	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	agentrepository "github.com/domainry/domainry-runtime/runtime/domain/agent/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/ratelimit"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

const (
	AgentToolQueryRecords = agentcatalog.ToolQueryRecords
	AgentToolGetRecord    = agentcatalog.ToolGetRecord
	AgentToolInvokeAction = agentcatalog.ToolInvokeAction
)

type AgentToolQueryPort interface {
	QueryAgentRecords(context.Context, string, map[string]any, AgentToolFieldScope, principalmodel.Principal) (any, error)
	GetAgentRecord(context.Context, string, string, AgentToolFieldScope, principalmodel.Principal) (any, error)
}

type AgentToolFieldScope struct{ VisibleFields map[string][]string }

type AgentToolActionPort interface {
	InvokeAgentAction(context.Context, AgentToolActionInvocation) (AgentToolActionInvocationResult, error)
}

type AgentToolActionInvocation struct {
	ActionKey, ObjectKey, RecordID string
	Input                          map[string]any
	Principal                      principalmodel.Principal
	RequestID, IdempotencyKey      string
}

type AgentToolActionInvocationResult struct {
	Record any
	Object any
}

type AgentToolProposalPort interface {
	CreateAgentActionProposal(context.Context, AgentToolProposalRequest) (AgentToolProposalResult, error)
}

type AgentToolProposalResult struct {
	ProposalID string
	Value      any
}

type AgentToolProposalRequest struct {
	ActionKey, ObjectKey, RecordID, ProcessID, NodeInstanceID, TaskRunID, IdempotencyKey string
	InteractiveRunID, SessionID, EntrypointKey, Surface, RouteKey, ContextRevision       string
	Attempt                                                                              int
	Input                                                                                map[string]any
	Identity                                                                             agentmodel.AgentExecutionIdentity
	Principal                                                                            principalmodel.Principal
}

type AgentToolRiskPolicy interface {
	RequiresAgentProposal(context.Context, string, principalmodel.Principal) (bool, string, error)
}

type AgentToolGatewayDependencies struct {
	Authorization   *AgentAuthorizationApplicationService
	Credentials     *AgentTaskCredentialApplicationService
	Queries         AgentToolQueryPort
	Actions         AgentToolActionPort
	Proposals       AgentToolProposalPort
	Risk            AgentToolRiskPolicy
	Ledger          agentrepository.AgentToolCallLedger
	RateLimiter     ratelimit.Limiter
	InteractiveRuns *AgentInteractiveRunApplicationService
	TaskRuns        *AgentTaskRunApplicationService
}

type AgentToolGateway struct{ dependencies AgentToolGatewayDependencies }

func NewAgentToolGateway(dependencies AgentToolGatewayDependencies) *AgentToolGateway {
	return &AgentToolGateway{dependencies: dependencies}
}

type AgentToolInvocationRequest struct {
	Credential                                                  string
	WorkspaceID, ProcessID, TaskRunID                           string
	Owner                                                       workerplatform.WorkerID
	FencingToken                                                workerplatform.FencingToken
	Initiator                                                   principalmodel.Principal
	Identity                                                    agentmodel.AgentTaskIdentity
	ExpectedRotationVersion                                     int
	TaskKey, TaskVersion                                        string
	NodeAllowedObjects, NodeAllowedActions, NodeAllowedOutcomes []string
	Tool                                                        string
	Input                                                       map[string]any
	IdempotencyKey                                              string
}

type AgentToolInvocationResult struct {
	Status        string                                `json:"status"`
	Tool          string                                `json:"tool"`
	CallRef       string                                `json:"call_ref"`
	Output        any                                   `json:"output,omitempty"`
	Proposal      any                                   `json:"proposal,omitempty"`
	Authorization agentmodel.AgentAuthorizationEvidence `json:"authorization"`
}

func (g *AgentToolGateway) Invoke(ctx context.Context, request AgentToolInvocationRequest) (result AgentToolInvocationResult, err error) {
	ctx, span := telemetry.StartUseCase(ctx, "agent.tool.invoke", attribute.String("workspace.id", request.WorkspaceID), attribute.String("workflow.process_id", request.ProcessID), attribute.String("agent.task_run_id", request.TaskRunID), attribute.String("agent.tool", request.Tool))
	defer func() { telemetry.EndUseCase(span, err, result.Status) }()
	if g == nil || g.dependencies.Authorization == nil || g.dependencies.Credentials == nil || g.dependencies.Ledger == nil {
		return result, apperror.New(apperror.KindUnavailable, "agent.tool.gateway_unavailable", nil, nil)
	}
	request.Tool = strings.TrimSpace(request.Tool)
	if _, supported := agentcatalog.Lookup(request.Tool); !supported {
		return result, apperror.New(apperror.KindForbidden, "agent.tool.not_allowed", nil, nil)
	}
	claims, err := g.dependencies.Credentials.Verify(ctx, request.Credential, AgentTaskCredentialScope{WorkspaceID: request.WorkspaceID, ProcessID: request.ProcessID, TaskRunID: request.TaskRunID, Tool: request.Tool})
	if err != nil {
		return result, err
	}
	authorization, err := g.dependencies.Authorization.AuthorizeTask(ctx, AgentTaskAuthorizationRequest{Initiator: request.Initiator, Identity: request.Identity, ExpectedRotationVersion: request.ExpectedRotationVersion, TaskKey: request.TaskKey, TaskVersion: request.TaskVersion, NodeAllowedObjects: request.NodeAllowedObjects, NodeAllowedActions: request.NodeAllowedActions, NodeAllowedOutcomes: request.NodeAllowedOutcomes})
	if err != nil {
		return result, err
	}
	if claims.Principal.UserID != authorization.Principal.UserID || claims.Principal.RoleKey != authorization.Principal.RoleKey {
		return result, apperror.New(apperror.KindForbidden, "agent.credential.principal_denied", nil, nil)
	}
	if !agentContains(authorization.AllowedTools, request.Tool) {
		return result, apperror.New(apperror.KindForbidden, "agent.tool.not_allowed", nil, nil)
	}
	input, err := json.Marshal(request.Input)
	maxInput := authorization.Task.ExecutionLimits.MaxInputBytes
	if maxInput <= 0 {
		maxInput = 64 * 1024
	}
	if err != nil || len(input) > maxInput {
		return result, apperror.New(apperror.KindBadRequest, "agent.tool.input_invalid", err, nil)
	}
	if g.dependencies.RateLimiter != nil {
		decision, rateErr := g.dependencies.RateLimiter.Allow(ctx, "agent_tool:"+request.WorkspaceID+":"+request.TaskRunID, maxAgentToolCalls(authorization.Task), time.Minute)
		if rateErr != nil {
			return result, rateErr
		}
		if !decision.Allowed {
			return result, apperror.New(apperror.KindRateLimited, "agent.tool.rate_limited", nil, nil)
		}
	}
	callRef, _, err := g.dependencies.Ledger.BeginAgentToolCall(ctx, agentrepository.AgentToolCallStart{WorkspaceID: request.WorkspaceID, ProcessID: request.ProcessID, TaskRunID: request.TaskRunID, Tool: request.Tool, InputHash: agentStableHash(request.Input), Owner: request.Owner.String(), FencingToken: int64(request.FencingToken), MaxToolCalls: maxAgentToolCalls(authorization.Task), CostUnits: agentToolCostUnits(request.Tool), MaxCostUnits: agentTaskCostBudgetUnits(authorization.Task.ExecutionLimits.CostBudget), Authorization: authorization.Evidence})
	if err != nil {
		return result, err
	}
	result = AgentToolInvocationResult{Tool: request.Tool, CallRef: callRef, Authorization: authorization.Evidence}
	ledgerFinished := false
	finishLedger := func() error {
		if ledgerFinished {
			return nil
		}
		code := apperror.CodeOf(err)
		status := result.Status
		if err != nil {
			status = "failed"
		}
		finishErr := g.dependencies.Ledger.FinishAgentToolCall(ctx, agentrepository.AgentToolCallFinish{WorkspaceID: request.WorkspaceID, TaskRunID: request.TaskRunID, CallRef: callRef, Status: status, ErrorCode: code, Owner: request.Owner.String(), FencingToken: int64(request.FencingToken), Evidence: map[string]any{"output_hash": agentStableHash(map[string]any{"output": result.Output, "proposal": result.Proposal})}})
		ledgerFinished = finishErr == nil
		return finishErr
	}
	defer func() {
		if finishErr := finishLedger(); err == nil && finishErr != nil {
			err = finishErr
		}
	}()
	timeout := time.Duration(authorization.Task.ExecutionLimits.TimeoutSeconds) * time.Second
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	switch request.Tool {
	case AgentToolQueryRecords:
		if g.dependencies.Queries == nil {
			return result, apperror.New(apperror.KindUnavailable, "agent.tool.query_unavailable", nil, nil)
		}
		objectKey := agentToolString(request.Input, "object_key")
		if !agentContains(authorization.AllowedObjects, objectKey) {
			return result, apperror.New(apperror.KindForbidden, "agent.tool.object_denied", nil, nil)
		}
		result.Output, err = g.dependencies.Queries.QueryAgentRecords(ctx, objectKey, agentToolMap(request.Input["query"]), AgentToolFieldScope{VisibleFields: authorization.VisibleFields}, authorization.Principal)
		result.Status = "executed"
	case AgentToolGetRecord:
		if g.dependencies.Queries == nil {
			return result, apperror.New(apperror.KindUnavailable, "agent.tool.query_unavailable", nil, nil)
		}
		objectKey, recordID := agentToolString(request.Input, "object_key"), agentToolString(request.Input, "record_id")
		if !agentContains(authorization.AllowedObjects, objectKey) || recordID == "" {
			return result, apperror.New(apperror.KindForbidden, "agent.tool.record_denied", nil, nil)
		}
		result.Output, err = g.dependencies.Queries.GetAgentRecord(ctx, objectKey, recordID, AgentToolFieldScope{VisibleFields: authorization.VisibleFields}, authorization.Principal)
		result.Status = "executed"
	case AgentToolInvokeAction:
		actionKey, objectKey, recordID := agentToolString(request.Input, "action_key"), agentToolString(request.Input, "object_key"), agentToolString(request.Input, "record_id")
		if !agentContains(authorization.AllowedActions, actionKey) || !agentContains(authorization.AllowedObjects, objectKey) {
			return result, apperror.New(apperror.KindForbidden, "agent.tool.action_denied", nil, nil)
		}
		if authorization.Task.SideEffectMode == agentmodel.AgentTaskSideEffectAnalysisOnly {
			return result, apperror.New(apperror.KindForbidden, "agent.tool.write_denied", nil, nil)
		}
		if strings.TrimSpace(request.IdempotencyKey) == "" {
			return result, apperror.New(apperror.KindBadRequest, "agent.tool.idempotency_required", nil, nil)
		}
		if g.dependencies.Risk == nil {
			return result, apperror.New(apperror.KindUnavailable, "agent.tool.risk_policy_unavailable", nil, nil)
		}
		requiresProposal, _, riskErr := g.dependencies.Risk.RequiresAgentProposal(ctx, actionKey, authorization.Principal)
		if riskErr != nil {
			return result, riskErr
		}
		data := agentToolMap(request.Input["data"])
		if authorization.Task.SideEffectMode == agentmodel.AgentTaskSideEffectProposalOnly || requiresProposal {
			if g.dependencies.Proposals == nil {
				return result, apperror.New(apperror.KindUnavailable, "agent.tool.proposal_unavailable", nil, nil)
			}
			if g.dependencies.TaskRuns == nil {
				return result, apperror.New(apperror.KindUnavailable, "agent.task.approval_lifecycle_unavailable", nil, nil)
			}
			taskRun, found, loadErr := g.dependencies.TaskRuns.Get(ctx, request.WorkspaceID, request.TaskRunID)
			if loadErr != nil {
				return result, loadErr
			}
			if !found {
				return result, apperror.New(apperror.KindNotFound, "agent.task.not_found", nil, nil)
			}
			var proposal AgentToolProposalResult
			proposal, err = g.dependencies.Proposals.CreateAgentActionProposal(ctx, AgentToolProposalRequest{ActionKey: actionKey, ObjectKey: objectKey, RecordID: recordID, ProcessID: request.ProcessID, NodeInstanceID: taskRun.NodeInstanceID, TaskRunID: request.TaskRunID, Attempt: taskRun.Attempt, IdempotencyKey: request.IdempotencyKey, Input: data, Identity: authorization.Identity, Principal: authorization.Principal})
			result.Proposal = proposal.Value
			result.Status = "proposal_required"
			if err == nil {
				if err = finishLedger(); err == nil {
					var run agentmodel.AgentTaskRun
					var found bool
					run, found, err = g.dependencies.TaskRuns.Get(ctx, request.WorkspaceID, request.TaskRunID)
					if err == nil && !found {
						err = apperror.New(apperror.KindNotFound, "agent.task.not_found", nil, nil)
					}
					if err == nil {
						_, err = g.dependencies.TaskRuns.WaitForApproval(ctx, run, request.Owner, request.FencingToken, proposal.ProposalID, run.Evidence)
					}
				}
			}
		} else {
			if g.dependencies.Actions == nil {
				return result, apperror.New(apperror.KindUnavailable, "agent.tool.action_unavailable", nil, nil)
			}
			result.Output, err = g.dependencies.Actions.InvokeAgentAction(ctx, AgentToolActionInvocation{ActionKey: actionKey, ObjectKey: objectKey, RecordID: recordID, Input: data, Principal: authorization.Principal, RequestID: authorization.Principal.RequestID, IdempotencyKey: strings.TrimSpace(request.IdempotencyKey)})
			result.Status = "executed"
		}
	default:
		return result, apperror.New(apperror.KindForbidden, "agent.tool.not_allowed", nil, nil)
	}
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return result, apperror.New(apperror.KindUnavailable, "agent.tool.timeout", err, nil)
		}
		return result, err
	}
	encoded, encodeErr := json.Marshal(map[string]any{"output": result.Output, "proposal": result.Proposal})
	maxOutput := authorization.Task.ExecutionLimits.MaxOutputBytes
	if maxOutput <= 0 {
		maxOutput = 64 * 1024
	}
	if encodeErr != nil || len(encoded) > maxOutput {
		return result, apperror.New(apperror.KindBadRequest, "agent.tool.output_invalid", encodeErr, nil)
	}
	return result, nil
}

func maxAgentToolCalls(task agentmodel.AgentTaskDefinition) int {
	if task.ExecutionLimits.MaxToolCalls > 0 {
		return task.ExecutionLimits.MaxToolCalls
	}
	return 20
}

func agentToolCostUnits(tool string) int {
	switch strings.TrimSpace(tool) {
	case AgentToolInvokeAction:
		return 5
	case AgentToolQueryRecords:
		return 2
	default:
		return 1
	}
}

func agentTaskCostBudgetUnits(budget string) int {
	switch strings.ToLower(strings.TrimSpace(budget)) {
	case "low":
		return 5
	case "high":
		return 50
	default:
		return 20
	}
}
func agentToolString(input map[string]any, key string) string {
	value, exists := input[key]
	if !exists || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}
func agentToolMap(value any) map[string]any {
	if typed, ok := value.(map[string]any); ok {
		return typed
	}
	return map[string]any{}
}
