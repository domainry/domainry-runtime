package runtime

import (
	"context"
	"errors"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentmodel "github.com/domainry/domainry-agent-sdk/state"
	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/logging"
	"github.com/domainry/domainry-foundation/telemetry"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type AgentInteractiveWorkflowStarter interface {
	StartInteractiveAgentWorkflow(context.Context, string, map[string]any, string, string, principalmodel.Principal) (string, error)
}

type AgentInteractiveExecutionDependencies struct {
	Runs      *AgentInteractiveRunApplicationService
	Authorize *AgentAuthorizationApplicationService
	Runner    agentsdk.InteractiveRunner
	Dispatch  *AgentTaskDispatchApplicationService
	Workflows AgentInteractiveWorkflowStarter
	Tools     *AgentToolGateway
}

type AgentInteractiveExecutionApplicationService struct {
	dependencies AgentInteractiveExecutionDependencies
}

func NewAgentInteractiveExecutionApplicationService(dependencies AgentInteractiveExecutionDependencies) *AgentInteractiveExecutionApplicationService {
	return &AgentInteractiveExecutionApplicationService{dependencies: dependencies}
}

type AgentInteractiveExecutionRequest struct {
	SessionID, IdempotencyKey, Message string
	Context                            agentsdk.GlobalContext
	Principal                          principalmodel.Principal
}

type AgentInteractiveExecutionResult struct {
	Run    agentmodel.AgentInteractiveRun `json:"run"`
	Result agentsdk.InteractiveResult     `json:"result"`
}

func (s *AgentInteractiveExecutionApplicationService) Execute(ctx context.Context, request AgentInteractiveExecutionRequest) (executionResult AgentInteractiveExecutionResult, err error) {
	ctx, span := telemetry.StartUseCase(ctx, "agent.interactive.execute", attribute.String("workspace.id", request.Principal.WorkspaceID), attribute.String("agent.session", request.SessionID), attribute.String("agent.entrypoint", request.Context.EntrypointKey), attribute.String("product.surface", request.Context.Surface), attribute.String("route.key", request.Context.RouteKey), attribute.String("correlation.id", request.Principal.CorrelationID))
	defer func() { telemetry.EndUseCase(span, err, string(executionResult.Run.Status)) }()
	if s == nil || s.dependencies.Runs == nil || s.dependencies.Authorize == nil || s.dependencies.Runner == nil {
		return AgentInteractiveExecutionResult{}, apperror.New(apperror.KindUnavailable, "agent.interactive.runner_unavailable", nil, nil)
	}
	if strings.TrimSpace(request.Message) == "" || strings.TrimSpace(request.IdempotencyKey) == "" {
		return AgentInteractiveExecutionResult{}, apperror.New(apperror.KindBadRequest, "agent.interactive.request_invalid", nil, nil)
	}
	authorized, err := s.dependencies.Authorize.AuthorizeInteractive(ctx, request.Context, request.Principal)
	if err != nil {
		s.dependencies.Runs.ObservePermissionDenied(ctx)
		return AgentInteractiveExecutionResult{}, err
	}
	run, replayed, err := s.dependencies.Runs.Create(ctx, AgentInteractiveRunCreateRequest{
		SessionID: request.SessionID, EntrypointKey: authorized.Context.EntrypointKey, IdempotencyKey: request.IdempotencyKey, Context: authorized.Context, Principal: authorized.Principal,
	})
	if err != nil {
		return AgentInteractiveExecutionResult{}, err
	}
	logging.FromContext(ctx).Info("agent interactive run accepted", logging.Fields(map[string]any{"workspace_id": run.WorkspaceID, "session_id": run.SessionID, "interactive_run_id": run.ID, "entrypoint_key": run.EntrypointKey, "surface": run.Surface, "route_key": run.RouteKey, "correlation_id": run.CorrelationID})...)
	if replayed && run.Status.Terminal() {
		return restoredInteractiveExecution(run), nil
	}
	limits := authorized.Agent.ExecutionLimits
	timeout := time.Duration(limits.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	workCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	result, runErr := s.dependencies.Runner.Run(workCtx, agentsdk.InteractiveRequest{
		RunID: run.ID, SessionID: run.SessionID, Context: authorized.Context, Message: strings.TrimSpace(request.Message), Candidates: authorized.Candidates,
		IdempotencyKey: request.IdempotencyKey, MaxSteps: limits.MaxSteps, MaxToolCalls: limits.MaxToolCalls, Deadline: time.Now().UTC().Add(timeout),
	})
	if runErr != nil {
		code := apperror.CodeOf(runErr)
		if errors.Is(runErr, context.DeadlineExceeded) || errors.Is(workCtx.Err(), context.DeadlineExceeded) {
			code = "agent.interactive.timeout"
			runErr = apperror.New(apperror.KindUnavailable, code, runErr, nil)
		}
		failed, completeErr := s.dependencies.Runs.Complete(ctx, run, agentmodel.AgentInteractiveRunFailed, nil, code)
		if completeErr != nil {
			return AgentInteractiveExecutionResult{}, completeErr
		}
		return AgentInteractiveExecutionResult{Run: failed, Result: result}, runErr
	}
	result.RunID = run.ID
	if result.Route != nil {
		route, routeErr := ValidateAgentRouteResult(*result.Route, authorized.Candidates)
		if routeErr != nil {
			return s.failInteractiveExecution(ctx, run, result, routeErr)
		}
		result.Route = &route
		switch route.RouteType {
		case agentsdk.AgentRouteTask:
			return s.handoffInteractiveTask(ctx, run, result, route, authorized)
		case agentsdk.AgentRouteWorkflow:
			return s.handoffInteractiveWorkflow(ctx, run, result, route, authorized.Principal)
		case agentsdk.AgentRouteInteractiveQuery, agentsdk.AgentRouteProposal:
			if s.dependencies.Tools == nil {
				return s.failInteractiveExecution(ctx, run, result, apperror.New(apperror.KindUnavailable, "agent.interactive.tool_gateway_unavailable", nil, nil))
			}
			toolResult, toolErr := s.dependencies.Tools.InvokeInteractive(ctx, AgentInteractiveToolInvocationRequest{Run: run, Route: route, IdempotencyKey: route.IdempotencyKey})
			if toolErr != nil {
				return s.failInteractiveExecution(ctx, run, result, toolErr)
			}
			run = toolResult.Run
			result.Structured = map[string]any{"tool": toolResult.Invocation.Tool, "status": toolResult.Invocation.Status, "output": toolResult.Invocation.Output, "proposal": toolResult.Invocation.Proposal, "call_ref": toolResult.Invocation.CallRef}
		}
	}
	run.ExternalRunID, run.Model, run.Usage = strings.TrimSpace(result.ExternalRunID), strings.TrimSpace(result.Model), result.Usage
	completed, err := s.dependencies.Runs.Complete(ctx, run, agentmodel.AgentInteractiveRunCompleted, result.Structured, "")
	return AgentInteractiveExecutionResult{Run: completed, Result: result}, err
}

func (s *AgentInteractiveExecutionApplicationService) handoffInteractiveTask(ctx context.Context, run agentmodel.AgentInteractiveRun, result agentsdk.InteractiveResult, route agentsdk.RouteResult, authorized AgentInteractiveAuthorization) (AgentInteractiveExecutionResult, error) {
	if s.dependencies.Dispatch == nil {
		return s.failInteractiveExecution(ctx, run, result, apperror.New(apperror.KindUnavailable, "agent.interactive.task_handoff_unavailable", nil, nil))
	}
	task, err := s.dependencies.Dispatch.PrepareInteractive(ctx, AgentInteractiveTaskDispatchRequest{
		InteractiveRunID: run.ID, WorkspaceID: run.WorkspaceID, TaskKey: route.TargetKey, TaskVersion: route.TargetVersion,
		IdempotencyKey: run.ID + ":" + route.IdempotencyKey, Identity: agentsdk.AgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityInherit}, Input: route.Input,
		Initiator: authorized.Principal, CorrelationID: run.CorrelationID,
	})
	if err != nil {
		return s.failInteractiveExecution(ctx, run, result, err)
	}
	handedOff, _, err := s.dependencies.Runs.HandoffTask(ctx, run, route, task)
	if err != nil {
		return AgentInteractiveExecutionResult{}, err
	}
	result.Status, result.Handoff = "handed_off", &agentsdk.InteractiveAgentHandoff{ContractVersion: agentsdk.InteractiveHandoffContractVersion, RouteType: route.RouteType, TargetKey: route.TargetKey, Input: route.Input, IdempotencyKey: route.IdempotencyKey, TaskRunID: handedOff.TaskRunID}
	logging.FromContext(ctx).Info("agent interactive task handed off", logging.Fields(map[string]any{"workspace_id": handedOff.WorkspaceID, "session_id": handedOff.SessionID, "interactive_run_id": handedOff.ID, "entrypoint_key": handedOff.EntrypointKey, "surface": handedOff.Surface, "process_id": handedOff.ProcessID, "task_run_id": handedOff.TaskRunID, "correlation_id": handedOff.CorrelationID})...)
	return AgentInteractiveExecutionResult{Run: handedOff, Result: result}, nil
}

func (s *AgentInteractiveExecutionApplicationService) handoffInteractiveWorkflow(ctx context.Context, run agentmodel.AgentInteractiveRun, result agentsdk.InteractiveResult, route agentsdk.RouteResult, principal principalmodel.Principal) (AgentInteractiveExecutionResult, error) {
	if s.dependencies.Workflows == nil {
		return s.failInteractiveExecution(ctx, run, result, apperror.New(apperror.KindUnavailable, "agent.interactive.workflow_handoff_unavailable", nil, nil))
	}
	processID, err := s.dependencies.Workflows.StartInteractiveAgentWorkflow(ctx, route.TargetKey, route.Input, run.ID, route.IdempotencyKey, principal)
	if err != nil {
		return s.failInteractiveExecution(ctx, run, result, err)
	}
	handedOff, _, err := s.dependencies.Runs.HandoffWorkflow(ctx, run, route, processID)
	if err != nil {
		return AgentInteractiveExecutionResult{}, err
	}
	result.Status, result.Handoff = "handed_off", &agentsdk.InteractiveAgentHandoff{ContractVersion: agentsdk.InteractiveHandoffContractVersion, RouteType: route.RouteType, TargetKey: route.TargetKey, Input: route.Input, IdempotencyKey: route.IdempotencyKey, ProcessID: processID}
	logging.FromContext(ctx).Info("agent interactive workflow handed off", logging.Fields(map[string]any{"workspace_id": handedOff.WorkspaceID, "session_id": handedOff.SessionID, "interactive_run_id": handedOff.ID, "entrypoint_key": handedOff.EntrypointKey, "surface": handedOff.Surface, "process_id": handedOff.ProcessID, "correlation_id": handedOff.CorrelationID})...)
	return AgentInteractiveExecutionResult{Run: handedOff, Result: result}, nil
}

func (s *AgentInteractiveExecutionApplicationService) failInteractiveExecution(ctx context.Context, run agentmodel.AgentInteractiveRun, result agentsdk.InteractiveResult, cause error) (AgentInteractiveExecutionResult, error) {
	failed, err := s.dependencies.Runs.Complete(ctx, run, agentmodel.AgentInteractiveRunFailed, nil, apperror.CodeOf(cause))
	if err != nil {
		return AgentInteractiveExecutionResult{}, err
	}
	return AgentInteractiveExecutionResult{Run: failed, Result: result}, cause
}

func restoredInteractiveExecution(run agentmodel.AgentInteractiveRun) AgentInteractiveExecutionResult {
	result := agentsdk.InteractiveResult{RunID: run.ID, Status: string(run.Status), Structured: run.StructuredResult}
	if run.Status == agentmodel.AgentInteractiveRunHandedOff {
		result.Handoff = &agentsdk.InteractiveAgentHandoff{ContractVersion: agentsdk.InteractiveHandoffContractVersion, RouteType: run.RouteType, TargetKey: run.RoutedTargetKey, IdempotencyKey: run.IdempotencyKey, ProcessID: run.ProcessID, TaskRunID: run.TaskRunID}
	}
	return AgentInteractiveExecutionResult{Run: run, Result: result}
}
