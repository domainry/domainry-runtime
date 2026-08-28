package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	agentrepository "github.com/domainry/domainry-runtime/runtime/domain/agent/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type agentInteractiveRunRepositoryStub struct {
	run                 agentmodel.AgentInteractiveRun
	replayed            bool
	updated             bool
	handoff             agentmodel.AgentInteractiveRun
	duplicate           bool
	createErr           error
	getErr              error
	listErr             error
	saveErr             error
	handoffErr          error
	preserveRunOnSave   bool
	preserveRunOnCreate bool
}

func (s *agentInteractiveRunRepositoryStub) CreateInteractiveRun(_ context.Context, run agentmodel.AgentInteractiveRun) (agentmodel.AgentInteractiveRun, bool, error) {
	if s.preserveRunOnCreate {
		return s.run, s.replayed, s.createErr
	}
	s.run = run
	return run, s.replayed, s.createErr
}
func (s *agentInteractiveRunRepositoryStub) GetInteractiveRun(context.Context, string, string) (agentmodel.AgentInteractiveRun, bool, error) {
	return s.run, s.run.ID != "", s.getErr
}
func (s *agentInteractiveRunRepositoryStub) ListInteractiveRuns(context.Context, string, string, string, agentrepository.AgentInteractiveRunFilter) ([]agentmodel.AgentInteractiveRun, error) {
	return []agentmodel.AgentInteractiveRun{s.run}, s.listErr
}
func (s *agentInteractiveRunRepositoryStub) SaveInteractiveRun(_ context.Context, run agentmodel.AgentInteractiveRun, _ int64) (bool, error) {
	if !s.preserveRunOnSave {
		s.run = run
	}
	return s.updated, s.saveErr
}
func (s *agentInteractiveRunRepositoryStub) CommitInteractiveTaskHandoff(_ context.Context, run agentmodel.AgentInteractiveRun, _ int64, task agentmodel.AgentTaskRun) (agentmodel.AgentInteractiveRun, bool, error) {
	s.handoff = run
	s.handoff.Status, s.handoff.TaskRunID, s.handoff.ProcessID = agentmodel.AgentInteractiveRunHandedOff, task.ID, task.ProcessID
	s.run = s.handoff
	return s.handoff, s.duplicate, s.handoffErr
}

func TestAgentInteractiveRunLifecycleAndPrincipalIsolation(t *testing.T) {
	now := time.Date(2026, 8, 4, 15, 0, 0, 0, time.UTC)
	repository := &agentInteractiveRunRepositoryStub{updated: true}
	service := NewAgentInteractiveRunApplicationService(repository, agentTaskClock{now: now}, agentCredentialIDStub{})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1", UserID: "user-1"}, SurfaceKey: "business_workspace", CorrelationID: "correlation-1"}, accessfixture.Bundle{Key: "operator"})
	principal.AuthorizationRevision = "auth-1"
	trusted := agentmodel.GlobalAgentContext{Surface: principal.SurfaceKey, RouteKey: "customer.detail", EntrypointKey: "assistant.global", AgentKey: "customer-agent", ContextRevision: "context-1", Principal: agentmodel.AgentPrincipalReference{WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, RoleKey: principal.RoleKey, AuthorizationRevision: "auth-1"}}
	run, replayed, err := service.Create(t.Context(), AgentInteractiveRunCreateRequest{SessionID: "session-1", AgentKey: "customer-agent", EntrypointKey: trusted.EntrypointKey, IdempotencyKey: "message-1", Context: trusted, Principal: principal})
	if err != nil || replayed || run.ID != "interactive_run_nonce" || run.ContextRevision != trusted.ContextRevision || run.Authorization.AuthorizationRevision != "auth-1" {
		t.Fatalf("run=%#v replayed=%v err=%v", run, replayed, err)
	}
	if restored, found, err := service.Get(t.Context(), run.ID, principal); err != nil || !found || restored.ID != run.ID {
		t.Fatalf("restored=%#v found=%v err=%v", restored, found, err)
	}
	otherRole := principal
	otherRole.RoleKey = "admin"
	if _, _, err := service.Get(t.Context(), run.ID, otherRole); apperror.CodeOf(err) != "agent.interactive.principal_denied" {
		t.Fatalf("role isolation=%v", err)
	}
	revoked := principal
	revoked.AuthorizationRevision = "auth-2"
	if _, _, err := service.Get(t.Context(), run.ID, revoked); apperror.CodeOf(err) != "agent.interactive.principal_denied" {
		t.Fatalf("stale authorization restored history=%v", err)
	}
	if listed, err := service.List(t.Context(), revoked, agentrepository.AgentInteractiveRunFilter{}); err != nil || len(listed) != 0 {
		t.Fatalf("stale authorization listed history=%#v err=%v", listed, err)
	}
	run.CreatedAt = now.Add(-2 * time.Second)
	completed, err := service.Complete(t.Context(), run, agentmodel.AgentInteractiveRunCompleted, map[string]any{"answer": true}, "")
	metrics := service.metricsSnapshot()
	if err != nil || completed.Status != agentmodel.AgentInteractiveRunCompleted || completed.CompletedAt == nil || metrics.Created != 1 || metrics.Completed != 1 || metrics.PermissionDenied != 2 || metrics.LatencyMilliseconds != 2000 || service.OpenMetrics(t.Context()) == "" {
		t.Fatalf("completed=%#v err=%v", completed, err)
	}
}

func TestAgentInteractiveRunTaskHandoffIsTypedAndStable(t *testing.T) {
	repository := &agentInteractiveRunRepositoryStub{}
	service := NewAgentInteractiveRunApplicationService(repository, nil, nil)
	run := agentmodel.AgentInteractiveRun{ID: "interactive", WorkspaceID: "workspace", Status: agentmodel.AgentInteractiveRunRunning, Revision: 2}
	task := agentmodel.AgentTaskRun{ID: "task", WorkspaceID: "workspace", ProcessID: "process", TaskKey: "customer.review", TaskVersion: "1.0.0"}
	route := AgentRouteResult{RouteType: agentmodel.AgentRouteTask, TargetKey: task.TaskKey, TargetVersion: task.TaskVersion, IdempotencyKey: "handoff-1"}
	handoff, duplicate, err := service.HandoffTask(t.Context(), run, route, task)
	if err != nil || duplicate || handoff.TaskRunID != task.ID || repository.run.RoutedTargetKey != task.TaskKey {
		t.Fatalf("handoff=%#v duplicate=%v err=%v", handoff, duplicate, err)
	}
	if service.metricsSnapshot().HandedOff != 1 {
		t.Fatalf("handoff metrics=%#v", service.metricsSnapshot())
	}
	route.RouteType = agentmodel.AgentRouteWorkflow
	if _, _, err := service.HandoffTask(t.Context(), run, route, task); apperror.CodeOf(err) != "agent.interactive.handoff_invalid" {
		t.Fatalf("untyped handoff=%v", err)
	}
	validRoute := AgentRouteResult{RouteType: agentmodel.AgentRouteTask, TargetKey: task.TaskKey, TargetVersion: task.TaskVersion, IdempotencyKey: "handoff-1"}
	for name, mutate := range map[string]func(*agentmodel.AgentInteractiveRun, *AgentRouteResult, *agentmodel.AgentTaskRun){
		"nil": func(*agentmodel.AgentInteractiveRun, *AgentRouteResult, *agentmodel.AgentTaskRun) {},
		"status": func(value *agentmodel.AgentInteractiveRun, _ *AgentRouteResult, _ *agentmodel.AgentTaskRun) {
			value.Status = agentmodel.AgentInteractiveRunCompleted
		},
		"target": func(_ *agentmodel.AgentInteractiveRun, value *AgentRouteResult, _ *agentmodel.AgentTaskRun) {
			value.TargetKey = "other"
		},
		"version": func(_ *agentmodel.AgentInteractiveRun, value *AgentRouteResult, _ *agentmodel.AgentTaskRun) {
			value.TargetVersion = "other"
		},
		"idempotency": func(_ *agentmodel.AgentInteractiveRun, value *AgentRouteResult, _ *agentmodel.AgentTaskRun) {
			value.IdempotencyKey = ""
		},
	} {
		t.Run(name, func(t *testing.T) {
			candidate, candidateRoute, candidateTask := run, validRoute, task
			mutate(&candidate, &candidateRoute, &candidateTask)
			target := NewAgentInteractiveRunApplicationService(&agentInteractiveRunRepositoryStub{}, nil, nil)
			if name == "nil" {
				target = nil
			}
			if _, _, err := target.HandoffTask(t.Context(), candidate, candidateRoute, candidateTask); apperror.CodeOf(err) != "agent.interactive.handoff_invalid" {
				t.Fatalf("error=%v", err)
			}
		})
	}
	if _, _, err := NewAgentInteractiveRunApplicationService(nil, nil, nil).HandoffTask(t.Context(), run, validRoute, task); apperror.CodeOf(err) != "agent.interactive.handoff_invalid" {
		t.Fatalf("missing repository=%v", err)
	}
	if _, _, err := NewAgentInteractiveRunApplicationService(&agentInteractiveRunRepositoryStub{handoffErr: errors.New("handoff")}, nil, nil).HandoffTask(t.Context(), run, validRoute, task); err == nil {
		t.Fatal("handoff repository error missing")
	}
}

func TestAgentInteractiveRunNilMetricsAreSafe(t *testing.T) {
	var service *AgentInteractiveRunApplicationService
	service.ObservePermissionDenied(t.Context())
	if service.metricsSnapshot() != (AgentInteractiveRunMetrics{}) || service.OpenMetrics(t.Context()) == "" {
		t.Fatalf("nil metrics=%#v", service.metricsSnapshot())
	}
}

func TestAgentInteractiveRunBoundaryMatrix(t *testing.T) {
	now := time.Date(2026, 8, 4, 15, 0, 0, 0, time.UTC)
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1", UserID: "user-1", AuthorizationRevision: "auth-1"}, SurfaceKey: "business_workspace"}, accessfixture.Bundle{Key: "operator"})
	trusted := agentmodel.GlobalAgentContext{Surface: principal.SurfaceKey, RouteKey: "customer.detail", EntrypointKey: "assistant.global", AgentKey: "customer-agent", ContextRevision: "context-1", Principal: agentmodel.AgentPrincipalReference{WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, RoleKey: principal.RoleKey, AuthorizationRevision: principal.AuthorizationRevision}}
	request := AgentInteractiveRunCreateRequest{SessionID: "session-1", EntrypointKey: trusted.EntrypointKey, IdempotencyKey: "message-1", Context: trusted, Principal: principal}
	wantErr := errors.New("repository failure")

	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := NewAgentInteractiveRunApplicationService(&agentInteractiveRunRepositoryStub{}, nil, nil).Create(cancelled, request); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled create=%v", err)
	}
	if _, _, err := (*AgentInteractiveRunApplicationService)(nil).Create(t.Context(), request); apperror.CodeOf(err) != "agent.interactive.repository_unavailable" {
		t.Fatalf("nil create=%v", err)
	}
	if _, _, err := NewAgentInteractiveRunApplicationService(nil, nil, nil).Create(t.Context(), request); apperror.CodeOf(err) != "agent.interactive.repository_unavailable" {
		t.Fatalf("missing repository create=%v", err)
	}
	for name, mutate := range map[string]func(*AgentInteractiveRunCreateRequest){
		"workspace":         func(value *AgentInteractiveRunCreateRequest) { value.Principal.WorkspaceID = "" },
		"unknown":           func(value *AgentInteractiveRunCreateRequest) { value.Principal.Known = false },
		"context workspace": func(value *AgentInteractiveRunCreateRequest) { value.Context.Principal.WorkspaceID = "other" },
		"context user":      func(value *AgentInteractiveRunCreateRequest) { value.Context.Principal.UserID = "other" },
		"context role":      func(value *AgentInteractiveRunCreateRequest) { value.Context.Principal.RoleKey = "other" },
	} {
		t.Run("create denied "+name, func(t *testing.T) {
			candidate := request
			mutate(&candidate)
			if _, _, err := NewAgentInteractiveRunApplicationService(&agentInteractiveRunRepositoryStub{}, nil, nil).Create(t.Context(), candidate); apperror.CodeOf(err) != "agent.interactive.context_denied" {
				t.Fatalf("error=%v", err)
			}
		})
	}
	invalid := request
	invalid.SessionID = ""
	if _, _, err := NewAgentInteractiveRunApplicationService(&agentInteractiveRunRepositoryStub{}, nil, nil).Create(t.Context(), invalid); apperror.CodeOf(err) != "agent.interactive.contract_invalid" {
		t.Fatalf("invalid create=%v", err)
	}
	if _, _, err := NewAgentInteractiveRunApplicationService(&agentInteractiveRunRepositoryStub{createErr: wantErr}, nil, nil).Create(t.Context(), request); !errors.Is(err, wantErr) {
		t.Fatalf("create repository=%v", err)
	}

	run := agentmodel.AgentInteractiveRun{ID: "run-1", WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, RoleKey: principal.RoleKey, Surface: principal.SurfaceKey, Status: agentmodel.AgentInteractiveRunRunning, Authorization: agentmodel.AgentAuthorizationEvidence{AuthorizationRevision: principal.AuthorizationRevision}, CreatedAt: now, UpdatedAt: now, Revision: 1}
	if _, _, err := (*AgentInteractiveRunApplicationService)(nil).Get(t.Context(), run.ID, principal); apperror.CodeOf(err) != "agent.interactive.repository_unavailable" {
		t.Fatalf("nil get=%v", err)
	}
	if _, _, err := NewAgentInteractiveRunApplicationService(nil, nil, nil).Get(t.Context(), run.ID, principal); apperror.CodeOf(err) != "agent.interactive.repository_unavailable" {
		t.Fatalf("missing repository get=%v", err)
	}
	for name, mutate := range map[string]func(*principalmodel.Principal){
		"workspace": func(value *principalmodel.Principal) { value.WorkspaceID = "" },
		"unknown":   func(value *principalmodel.Principal) { value.Known = false },
	} {
		t.Run("get denied "+name, func(t *testing.T) {
			candidate := principal
			mutate(&candidate)
			if _, _, err := NewAgentInteractiveRunApplicationService(&agentInteractiveRunRepositoryStub{run: run}, nil, nil).Get(t.Context(), run.ID, candidate); apperror.CodeOf(err) != "agent.interactive.principal_denied" {
				t.Fatalf("error=%v", err)
			}
		})
	}
	if _, _, err := NewAgentInteractiveRunApplicationService(&agentInteractiveRunRepositoryStub{run: run, getErr: wantErr}, nil, nil).Get(t.Context(), run.ID, principal); !errors.Is(err, wantErr) {
		t.Fatalf("get repository=%v", err)
	}
	if _, found, err := NewAgentInteractiveRunApplicationService(&agentInteractiveRunRepositoryStub{}, nil, nil).Get(t.Context(), run.ID, principal); err != nil || found {
		t.Fatalf("missing found=%v err=%v", found, err)
	}
	for name, mutate := range map[string]func(*agentmodel.AgentInteractiveRun){
		"user":    func(value *agentmodel.AgentInteractiveRun) { value.UserID = "other" },
		"role":    func(value *agentmodel.AgentInteractiveRun) { value.RoleKey = "other" },
		"surface": func(value *agentmodel.AgentInteractiveRun) { value.Surface = "other" },
	} {
		t.Run("stored scope "+name, func(t *testing.T) {
			candidate := run
			mutate(&candidate)
			if _, _, err := NewAgentInteractiveRunApplicationService(&agentInteractiveRunRepositoryStub{run: candidate}, nil, nil).Get(t.Context(), run.ID, principal); apperror.CodeOf(err) != "agent.interactive.principal_denied" {
				t.Fatalf("error=%v", err)
			}
		})
	}
	if _, err := (*AgentInteractiveRunApplicationService)(nil).List(t.Context(), principal, agentrepository.AgentInteractiveRunFilter{}); apperror.CodeOf(err) != "agent.interactive.principal_denied" {
		t.Fatalf("nil list=%v", err)
	}
	if _, err := NewAgentInteractiveRunApplicationService(nil, nil, nil).List(t.Context(), principal, agentrepository.AgentInteractiveRunFilter{}); apperror.CodeOf(err) != "agent.interactive.principal_denied" {
		t.Fatalf("missing repository list=%v", err)
	}
	unknown := principal
	unknown.Known = false
	if _, err := NewAgentInteractiveRunApplicationService(&agentInteractiveRunRepositoryStub{}, nil, nil).List(t.Context(), unknown, agentrepository.AgentInteractiveRunFilter{}); apperror.CodeOf(err) != "agent.interactive.principal_denied" {
		t.Fatalf("unknown list=%v", err)
	}
	badWorkspace := principal
	badWorkspace.WorkspaceID = ""
	if _, err := NewAgentInteractiveRunApplicationService(&agentInteractiveRunRepositoryStub{}, nil, nil).List(t.Context(), badWorkspace, agentrepository.AgentInteractiveRunFilter{}); apperror.CodeOf(err) != "agent.interactive.principal_denied" {
		t.Fatalf("invalid workspace list=%v", err)
	}
	if _, err := NewAgentInteractiveRunApplicationService(&agentInteractiveRunRepositoryStub{listErr: wantErr}, nil, nil).List(t.Context(), principal, agentrepository.AgentInteractiveRunFilter{}); !errors.Is(err, wantErr) {
		t.Fatalf("list repository=%v", err)
	}
	offSurface := run
	offSurface.Surface = "other"
	if listed, err := NewAgentInteractiveRunApplicationService(&agentInteractiveRunRepositoryStub{run: offSurface}, nil, nil).List(t.Context(), principal, agentrepository.AgentInteractiveRunFilter{}); err != nil || len(listed) != 0 {
		t.Fatalf("off surface list=%#v err=%v", listed, err)
	}
	if listed, err := NewAgentInteractiveRunApplicationService(&agentInteractiveRunRepositoryStub{run: run}, nil, nil).List(t.Context(), principal, agentrepository.AgentInteractiveRunFilter{}); err != nil || len(listed) != 1 {
		t.Fatalf("visible list=%#v err=%v", listed, err)
	}

	service := NewAgentInteractiveRunApplicationService(&agentInteractiveRunRepositoryStub{run: run, updated: true}, agentTaskClock{now: now}, nil)
	for name, mutate := range map[string]func(*agentmodel.AgentInteractiveRun, *agentmodel.AgentInteractiveRunStatus){
		"nil service": func(*agentmodel.AgentInteractiveRun, *agentmodel.AgentInteractiveRunStatus) {},
		"status": func(value *agentmodel.AgentInteractiveRun, _ *agentmodel.AgentInteractiveRunStatus) {
			value.Status = agentmodel.AgentInteractiveRunCompleted
		},
		"nonterminal": func(_ *agentmodel.AgentInteractiveRun, status *agentmodel.AgentInteractiveRunStatus) {
			*status = agentmodel.AgentInteractiveRunRunning
		},
		"handoff": func(_ *agentmodel.AgentInteractiveRun, status *agentmodel.AgentInteractiveRunStatus) {
			*status = agentmodel.AgentInteractiveRunHandedOff
		},
	} {
		t.Run("complete "+name, func(t *testing.T) {
			candidate, status := run, agentmodel.AgentInteractiveRunCompleted
			mutate(&candidate, &status)
			target := service
			if name == "nil service" {
				target = nil
			}
			if _, err := target.Complete(t.Context(), candidate, status, nil, ""); apperror.CodeOf(err) != "agent.interactive.transition_invalid" {
				t.Fatalf("error=%v", err)
			}
		})
	}
	if _, err := NewAgentInteractiveRunApplicationService(nil, nil, nil).Complete(t.Context(), run, agentmodel.AgentInteractiveRunCompleted, nil, ""); apperror.CodeOf(err) != "agent.interactive.transition_invalid" {
		t.Fatalf("complete missing repository=%v", err)
	}
	if _, err := NewAgentInteractiveRunApplicationService(&agentInteractiveRunRepositoryStub{saveErr: wantErr}, nil, nil).Complete(t.Context(), run, agentmodel.AgentInteractiveRunCompleted, nil, ""); !errors.Is(err, wantErr) {
		t.Fatalf("complete save=%v", err)
	}
	if _, err := NewAgentInteractiveRunApplicationService(&agentInteractiveRunRepositoryStub{}, nil, nil).Complete(t.Context(), run, agentmodel.AgentInteractiveRunCompleted, nil, ""); apperror.CodeOf(err) != "agent.interactive.revision_conflict" {
		t.Fatalf("complete conflict=%v", err)
	}
}

func TestAgentInteractiveRunWorkflowHandoffAndToolEvidenceEdges(t *testing.T) {
	wantErr := errors.New("repository failure")
	now := time.Date(2026, 8, 4, 15, 0, 0, 0, time.UTC)
	run := agentmodel.AgentInteractiveRun{ID: "run-1", WorkspaceID: "workspace-1", Status: agentmodel.AgentInteractiveRunRunning, CreatedAt: now, UpdatedAt: now, Revision: 1}
	route := AgentRouteResult{RouteType: agentmodel.AgentRouteWorkflow, TargetKey: "customer.follow_up", TargetVersion: "1", IdempotencyKey: "handoff-1"}
	for name, mutate := range map[string]func(*agentmodel.AgentInteractiveRun, *AgentRouteResult, *string){
		"nil": func(*agentmodel.AgentInteractiveRun, *AgentRouteResult, *string) {},
		"status": func(value *agentmodel.AgentInteractiveRun, _ *AgentRouteResult, _ *string) {
			value.Status = agentmodel.AgentInteractiveRunCompleted
		},
		"route type": func(_ *agentmodel.AgentInteractiveRun, value *AgentRouteResult, _ *string) {
			value.RouteType = agentmodel.AgentRouteTask
		},
		"target":      func(_ *agentmodel.AgentInteractiveRun, value *AgentRouteResult, _ *string) { value.TargetKey = "" },
		"idempotency": func(_ *agentmodel.AgentInteractiveRun, value *AgentRouteResult, _ *string) { value.IdempotencyKey = "" },
		"process":     func(_ *agentmodel.AgentInteractiveRun, _ *AgentRouteResult, value *string) { *value = "" },
	} {
		t.Run("handoff "+name, func(t *testing.T) {
			candidate, candidateRoute, processID := run, route, "process-1"
			mutate(&candidate, &candidateRoute, &processID)
			service := NewAgentInteractiveRunApplicationService(&agentInteractiveRunRepositoryStub{}, nil, nil)
			if name == "nil" {
				service = nil
			}
			if _, _, err := service.HandoffWorkflow(t.Context(), candidate, candidateRoute, processID); apperror.CodeOf(err) != "agent.interactive.handoff_invalid" {
				t.Fatalf("error=%v", err)
			}
		})
	}
	if _, _, err := NewAgentInteractiveRunApplicationService(nil, nil, nil).HandoffWorkflow(t.Context(), run, route, "process-1"); apperror.CodeOf(err) != "agent.interactive.handoff_invalid" {
		t.Fatalf("missing repository=%v", err)
	}
	if _, _, err := NewAgentInteractiveRunApplicationService(&agentInteractiveRunRepositoryStub{updated: true, saveErr: wantErr}, nil, nil).HandoffWorkflow(t.Context(), run, route, "process-1"); !errors.Is(err, wantErr) {
		t.Fatalf("save error=%v", err)
	}
	updatedRepo := &agentInteractiveRunRepositoryStub{updated: true}
	if handed, replayed, err := NewAgentInteractiveRunApplicationService(updatedRepo, agentTaskClock{now: now}, nil).HandoffWorkflow(t.Context(), run, route, "process-1"); err != nil || replayed || handed.ProcessID != "process-1" {
		t.Fatalf("updated handoff=%#v replay=%v err=%v", handed, replayed, err)
	}
	for name, stored := range map[string]agentmodel.AgentInteractiveRun{
		"missing": {},
		"status":  {ID: run.ID, Status: agentmodel.AgentInteractiveRunCompleted, RouteType: route.RouteType, RoutedTargetKey: route.TargetKey, ProcessID: "process-1"},
		"type":    {ID: run.ID, Status: agentmodel.AgentInteractiveRunHandedOff, RouteType: agentmodel.AgentRouteTask, RoutedTargetKey: route.TargetKey, ProcessID: "process-1"},
		"target":  {ID: run.ID, Status: agentmodel.AgentInteractiveRunHandedOff, RouteType: route.RouteType, RoutedTargetKey: "other", ProcessID: "process-1"},
		"process": {ID: run.ID, Status: agentmodel.AgentInteractiveRunHandedOff, RouteType: route.RouteType, RoutedTargetKey: route.TargetKey, ProcessID: "other"},
	} {
		t.Run("handoff conflict "+name, func(t *testing.T) {
			if _, _, err := NewAgentInteractiveRunApplicationService(&agentInteractiveRunRepositoryStub{run: stored, preserveRunOnSave: true}, agentTaskClock{now: now}, nil).HandoffWorkflow(t.Context(), run, route, "process-1"); apperror.CodeOf(err) != "agent.interactive.handoff_conflict" {
				t.Fatalf("error=%v", err)
			}
		})
	}
	if _, _, err := NewAgentInteractiveRunApplicationService(&agentInteractiveRunRepositoryStub{run: run, getErr: wantErr, preserveRunOnSave: true}, agentTaskClock{now: now}, nil).HandoffWorkflow(t.Context(), run, route, "process-1"); !errors.Is(err, wantErr) {
		t.Fatalf("replay get=%v", err)
	}
	replay := agentmodel.AgentInteractiveRun{ID: run.ID, Status: agentmodel.AgentInteractiveRunHandedOff, RouteType: route.RouteType, RoutedTargetKey: route.TargetKey, ProcessID: "process-1"}
	if restored, replayed, err := NewAgentInteractiveRunApplicationService(&agentInteractiveRunRepositoryStub{run: replay, preserveRunOnSave: true}, agentTaskClock{now: now}, nil).HandoffWorkflow(t.Context(), run, route, "process-1"); err != nil || !replayed || restored.ProcessID != "process-1" {
		t.Fatalf("replay=%#v replayed=%v err=%v", restored, replayed, err)
	}

	evidence := agentmodel.AgentTaskToolInvocationEvidence{Ref: "tool-1", Tool: "query_records"}
	for name, mutate := range map[string]func(*agentmodel.AgentInteractiveRun, *agentmodel.AgentTaskToolInvocationEvidence){
		"nil": func(*agentmodel.AgentInteractiveRun, *agentmodel.AgentTaskToolInvocationEvidence) {},
		"status": func(value *agentmodel.AgentInteractiveRun, _ *agentmodel.AgentTaskToolInvocationEvidence) {
			value.Status = agentmodel.AgentInteractiveRunCompleted
		},
		"ref": func(_ *agentmodel.AgentInteractiveRun, value *agentmodel.AgentTaskToolInvocationEvidence) {
			value.Ref = ""
		},
		"tool": func(_ *agentmodel.AgentInteractiveRun, value *agentmodel.AgentTaskToolInvocationEvidence) {
			value.Tool = ""
		},
	} {
		t.Run("tool "+name, func(t *testing.T) {
			candidate, candidateEvidence := run, evidence
			mutate(&candidate, &candidateEvidence)
			service := NewAgentInteractiveRunApplicationService(&agentInteractiveRunRepositoryStub{}, nil, nil)
			if name == "nil" {
				service = nil
			}
			if _, err := service.RecordToolInvocation(t.Context(), candidate, candidateEvidence); apperror.CodeOf(err) != "agent.interactive.tool_evidence_invalid" {
				t.Fatalf("error=%v", err)
			}
		})
	}
	if _, err := NewAgentInteractiveRunApplicationService(nil, nil, nil).RecordToolInvocation(t.Context(), run, evidence); apperror.CodeOf(err) != "agent.interactive.tool_evidence_invalid" {
		t.Fatalf("missing repository=%v", err)
	}
	if _, err := NewAgentInteractiveRunApplicationService(&agentInteractiveRunRepositoryStub{saveErr: wantErr}, nil, nil).RecordToolInvocation(t.Context(), run, evidence); !errors.Is(err, wantErr) {
		t.Fatalf("save error=%v", err)
	}
	if _, err := NewAgentInteractiveRunApplicationService(&agentInteractiveRunRepositoryStub{}, nil, nil).RecordToolInvocation(t.Context(), run, evidence); apperror.CodeOf(err) != "agent.interactive.revision_conflict" {
		t.Fatalf("save conflict=%v", err)
	}
	saved, err := NewAgentInteractiveRunApplicationService(&agentInteractiveRunRepositoryStub{updated: true}, nil, nil).RecordToolInvocation(t.Context(), run, evidence)
	if err != nil || saved.ToolCallCount != 1 || len(saved.ToolInvocations) != 1 {
		t.Fatalf("saved=%#v err=%v", saved, err)
	}
}
