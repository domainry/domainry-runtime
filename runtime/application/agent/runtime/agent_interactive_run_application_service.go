package runtime

import (
	"context"
	"fmt"
	"strings"
	"sync"

	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	agentrepository "github.com/domainry/domainry-runtime/runtime/domain/agent/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

type AgentInteractiveRunApplicationService struct {
	repository agentrepository.AgentInteractiveRunRepository
	clock      workerplatform.Clock
	ids        workerplatform.IdentifierGenerator
	wakeup     AgentTaskWakeup
	metricsMu  sync.Mutex
	metrics    AgentInteractiveRunMetrics
}

type AgentInteractiveRunMetrics struct {
	Created, Completed, HandedOff, PermissionDenied, ToolCalls uint64
	LatencyMilliseconds                                        uint64
}

func NewAgentInteractiveRunApplicationService(repository agentrepository.AgentInteractiveRunRepository, clock workerplatform.Clock, ids workerplatform.IdentifierGenerator) *AgentInteractiveRunApplicationService {
	if clock == nil {
		clock = workerplatform.SystemClock{}
	}
	if ids == nil {
		ids = workerplatform.CryptoIdentifierGenerator{}
	}
	return &AgentInteractiveRunApplicationService{repository: repository, clock: clock, ids: ids}
}

type AgentInteractiveRunCreateRequest struct {
	SessionID      string
	AgentKey       string
	EntrypointKey  string
	IdempotencyKey string
	Context        agentmodel.GlobalAgentContext
	Principal      principalmodel.Principal
}

func (s *AgentInteractiveRunApplicationService) Create(ctx context.Context, request AgentInteractiveRunCreateRequest) (agentmodel.AgentInteractiveRun, bool, error) {
	if err := ctx.Err(); err != nil {
		return agentmodel.AgentInteractiveRun{}, false, err
	}
	if s == nil || s.repository == nil {
		return agentmodel.AgentInteractiveRun{}, false, apperror.New(apperror.KindUnavailable, "agent.interactive.repository_unavailable", nil, nil)
	}
	workspace, err := principalmodel.NewWorkspaceID(request.Principal.WorkspaceID)
	if err != nil || !request.Principal.Known || request.Context.Principal.WorkspaceID != workspace.String() || request.Context.Principal.UserID != request.Principal.UserID || request.Context.Principal.RoleKey != request.Principal.RoleKey {
		return agentmodel.AgentInteractiveRun{}, false, apperror.New(apperror.KindForbidden, "agent.interactive.context_denied", err, nil)
	}
	now := s.clock.Now().UTC()
	run := agentmodel.AgentInteractiveRun{
		ID: "interactive_run_" + s.ids.NewID(), SessionID: strings.TrimSpace(request.SessionID), WorkspaceID: workspace.String(), UserID: request.Principal.UserID, RoleKey: request.Principal.RoleKey,
		Surface: request.Context.Surface, RouteKey: request.Context.RouteKey, AgentKey: strings.TrimSpace(request.Context.AgentKey), EntrypointKey: strings.TrimSpace(request.EntrypointKey), ContextRevision: request.Context.ContextRevision, Context: request.Context,
		Authorization: agentmodel.AgentAuthorizationEvidence{Decision: "allow", Code: "agent.authorization.interactive_allowed", ContextRevision: request.Context.ContextRevision, AuthorizationRevision: request.Context.Principal.AuthorizationRevision},
		Status:        agentmodel.AgentInteractiveRunRunning, CorrelationID: request.Principal.CorrelationID, IdempotencyKey: strings.TrimSpace(request.IdempotencyKey), CreatedAt: now, UpdatedAt: now, Revision: 1,
	}
	if !run.ValidForCreate() {
		return agentmodel.AgentInteractiveRun{}, false, apperror.New(apperror.KindBadRequest, "agent.interactive.contract_invalid", nil, nil)
	}
	created, replayed, err := s.repository.CreateInteractiveRun(ctx, run)
	if err == nil {
		s.addMetric(func(metrics *AgentInteractiveRunMetrics) { metrics.Created++ })
	}
	return created, replayed, err
}

func (s *AgentInteractiveRunApplicationService) Get(ctx context.Context, runID string, principal principalmodel.Principal) (agentmodel.AgentInteractiveRun, bool, error) {
	if s == nil || s.repository == nil {
		return agentmodel.AgentInteractiveRun{}, false, apperror.New(apperror.KindUnavailable, "agent.interactive.repository_unavailable", nil, nil)
	}
	workspace, err := principalmodel.NewWorkspaceID(principal.WorkspaceID)
	if err != nil || !principal.Known {
		s.ObservePermissionDenied(ctx)
		return agentmodel.AgentInteractiveRun{}, false, apperror.New(apperror.KindForbidden, "agent.interactive.principal_denied", err, nil)
	}
	run, found, err := s.repository.GetInteractiveRun(ctx, workspace.String(), strings.TrimSpace(runID))
	if err != nil || !found {
		return run, found, err
	}
	if run.UserID != principal.UserID || run.RoleKey != principal.RoleKey || run.Surface != principal.SurfaceKey || agentInteractiveAuthorizationStale(run, principal) {
		s.ObservePermissionDenied(ctx)
		return agentmodel.AgentInteractiveRun{}, false, apperror.New(apperror.KindForbidden, "agent.interactive.principal_denied", nil, nil)
	}
	return run, true, nil
}

func (s *AgentInteractiveRunApplicationService) List(ctx context.Context, principal principalmodel.Principal, filter agentrepository.AgentInteractiveRunFilter) ([]agentmodel.AgentInteractiveRun, error) {
	workspace, err := principalmodel.NewWorkspaceID(principal.WorkspaceID)
	if s == nil || s.repository == nil || err != nil || !principal.Known {
		return nil, apperror.New(apperror.KindForbidden, "agent.interactive.principal_denied", err, nil)
	}
	runs, err := s.repository.ListInteractiveRuns(ctx, workspace.String(), principal.UserID, principal.RoleKey, filter)
	if err != nil {
		return nil, err
	}
	visible := make([]agentmodel.AgentInteractiveRun, 0, len(runs))
	for _, run := range runs {
		if run.Surface == principal.SurfaceKey && !agentInteractiveAuthorizationStale(run, principal) {
			visible = append(visible, run)
		}
	}
	return visible, nil
}

func agentInteractiveAuthorizationStale(run agentmodel.AgentInteractiveRun, principal principalmodel.Principal) bool {
	stored, current := strings.TrimSpace(run.Authorization.AuthorizationRevision), strings.TrimSpace(principal.AuthorizationRevision)
	return stored != "" && current != "" && stored != current
}

func (s *AgentInteractiveRunApplicationService) Complete(ctx context.Context, run agentmodel.AgentInteractiveRun, status agentmodel.AgentInteractiveRunStatus, result map[string]any, errorCode string) (agentmodel.AgentInteractiveRun, error) {
	if s == nil || s.repository == nil || run.Status != agentmodel.AgentInteractiveRunRunning || !status.Terminal() || status == agentmodel.AgentInteractiveRunHandedOff {
		return agentmodel.AgentInteractiveRun{}, apperror.New(apperror.KindConflict, "agent.interactive.transition_invalid", nil, nil)
	}
	expected := run.Revision
	now := s.clock.Now().UTC()
	run.Status, run.StructuredResult, run.ErrorCode, run.UpdatedAt, run.CompletedAt, run.Revision = status, result, strings.TrimSpace(errorCode), now, &now, run.Revision+1
	updated, err := s.repository.SaveInteractiveRun(ctx, run, expected)
	if err != nil {
		return agentmodel.AgentInteractiveRun{}, err
	}
	if !updated {
		return agentmodel.AgentInteractiveRun{}, apperror.New(apperror.KindConflict, "agent.interactive.revision_conflict", nil, nil)
	}
	s.addMetric(func(metrics *AgentInteractiveRunMetrics) {
		metrics.Completed++
		if elapsed := now.Sub(run.CreatedAt).Milliseconds(); elapsed > 0 {
			metrics.LatencyMilliseconds += uint64(elapsed)
		}
	})
	return run, nil
}

func (s *AgentInteractiveRunApplicationService) HandoffTask(ctx context.Context, run agentmodel.AgentInteractiveRun, route AgentRouteResult, task agentmodel.AgentTaskRun) (agentmodel.AgentInteractiveRun, bool, error) {
	if s == nil || s.repository == nil || run.Status != agentmodel.AgentInteractiveRunRunning || route.RouteType != agentmodel.AgentRouteTask || strings.TrimSpace(route.TargetKey) != task.TaskKey || strings.TrimSpace(route.TargetVersion) != task.TaskVersion || strings.TrimSpace(route.IdempotencyKey) == "" {
		return agentmodel.AgentInteractiveRun{}, false, apperror.New(apperror.KindConflict, "agent.interactive.handoff_invalid", nil, nil)
	}
	task.InteractiveRunID = run.ID
	run.RouteType, run.RoutedTargetKey, run.RoutedTargetVersion = route.RouteType, route.TargetKey, route.TargetVersion
	handedOff, replayed, err := s.repository.CommitInteractiveTaskHandoff(ctx, run, run.Revision, task)
	if err == nil {
		s.addMetric(func(metrics *AgentInteractiveRunMetrics) { metrics.HandedOff++ })
		if s.wakeup != nil {
			s.wakeup(AgentTaskLocator{WorkspaceID: task.WorkspaceID, RunID: task.ID})
		}
	}
	return handedOff, replayed, err
}

func (s *AgentInteractiveRunApplicationService) HandoffWorkflow(ctx context.Context, run agentmodel.AgentInteractiveRun, route AgentRouteResult, processID string) (agentmodel.AgentInteractiveRun, bool, error) {
	if s == nil || s.repository == nil || run.Status != agentmodel.AgentInteractiveRunRunning || route.RouteType != agentmodel.AgentRouteWorkflow || strings.TrimSpace(route.TargetKey) == "" || strings.TrimSpace(route.IdempotencyKey) == "" || strings.TrimSpace(processID) == "" {
		return agentmodel.AgentInteractiveRun{}, false, apperror.New(apperror.KindConflict, "agent.interactive.handoff_invalid", nil, nil)
	}
	expected := run.Revision
	now := s.clock.Now().UTC()
	run.Status, run.RouteType, run.RoutedTargetKey, run.RoutedTargetVersion = agentmodel.AgentInteractiveRunHandedOff, route.RouteType, route.TargetKey, route.TargetVersion
	run.ProcessID, run.UpdatedAt, run.CompletedAt, run.Revision = strings.TrimSpace(processID), now, &now, run.Revision+1
	updated, err := s.repository.SaveInteractiveRun(ctx, run, expected)
	if err != nil {
		return agentmodel.AgentInteractiveRun{}, false, err
	}
	if updated {
		s.addMetric(func(metrics *AgentInteractiveRunMetrics) { metrics.HandedOff++ })
		return run, false, nil
	}
	current, found, err := s.repository.GetInteractiveRun(ctx, run.WorkspaceID, run.ID)
	if err != nil {
		return agentmodel.AgentInteractiveRun{}, false, err
	}
	if found && current.Status == agentmodel.AgentInteractiveRunHandedOff && current.RouteType == route.RouteType && current.RoutedTargetKey == route.TargetKey && current.ProcessID == processID {
		s.addMetric(func(metrics *AgentInteractiveRunMetrics) { metrics.HandedOff++ })
		return current, true, nil
	}
	return agentmodel.AgentInteractiveRun{}, false, apperror.New(apperror.KindConflict, "agent.interactive.handoff_conflict", nil, nil)
}

func (s *AgentInteractiveRunApplicationService) RecordToolInvocation(ctx context.Context, run agentmodel.AgentInteractiveRun, evidence agentmodel.AgentTaskToolInvocationEvidence) (agentmodel.AgentInteractiveRun, error) {
	if s == nil || s.repository == nil || run.Status != agentmodel.AgentInteractiveRunRunning || strings.TrimSpace(evidence.Ref) == "" || strings.TrimSpace(evidence.Tool) == "" {
		return agentmodel.AgentInteractiveRun{}, apperror.New(apperror.KindConflict, "agent.interactive.tool_evidence_invalid", nil, nil)
	}
	expected := run.Revision
	run.ToolCallCount++
	run.ToolInvocations = append(run.ToolInvocations, evidence)
	run.UpdatedAt, run.Revision = s.clock.Now().UTC(), run.Revision+1
	updated, err := s.repository.SaveInteractiveRun(ctx, run, expected)
	if err != nil {
		return agentmodel.AgentInteractiveRun{}, err
	}
	if !updated {
		return agentmodel.AgentInteractiveRun{}, apperror.New(apperror.KindConflict, "agent.interactive.revision_conflict", nil, nil)
	}
	s.addMetric(func(metrics *AgentInteractiveRunMetrics) { metrics.ToolCalls++ })
	return run, nil
}

func (s *AgentInteractiveRunApplicationService) ObservePermissionDenied(context.Context) {
	if s != nil {
		s.addMetric(func(metrics *AgentInteractiveRunMetrics) { metrics.PermissionDenied++ })
	}
}

func (s *AgentInteractiveRunApplicationService) addMetric(update func(*AgentInteractiveRunMetrics)) {
	s.metricsMu.Lock()
	defer s.metricsMu.Unlock()
	update(&s.metrics)
}

func (s *AgentInteractiveRunApplicationService) metricsSnapshot() AgentInteractiveRunMetrics {
	if s == nil {
		return AgentInteractiveRunMetrics{}
	}
	s.metricsMu.Lock()
	defer s.metricsMu.Unlock()
	return s.metrics
}

func (s *AgentInteractiveRunApplicationService) OpenMetrics(context.Context) string {
	metrics := s.metricsSnapshot()
	return fmt.Sprintf("# HELP domainry_agent_interactive_events Interactive Agent events by outcome.\n# TYPE domainry_agent_interactive_events counter\ndomainry_agent_interactive_events{event=\"created\"} %d\ndomainry_agent_interactive_events{event=\"completed\"} %d\ndomainry_agent_interactive_events{event=\"handoff\"} %d\ndomainry_agent_interactive_events{event=\"permission_denied\"} %d\ndomainry_agent_interactive_tool_calls_total %d\ndomainry_agent_interactive_latency_milliseconds_total %d\n", metrics.Created, metrics.Completed, metrics.HandedOff, metrics.PermissionDenied, metrics.ToolCalls, metrics.LatencyMilliseconds)
}
