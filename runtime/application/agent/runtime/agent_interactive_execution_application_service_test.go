package runtime

import (
	"context"
	"errors"
	"testing"
	"time"

	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type interactiveAgentRunnerFunc func(context.Context, InteractiveAgentRunRequest) (InteractiveAgentResult, error)

func (fn interactiveAgentRunnerFunc) Run(ctx context.Context, request InteractiveAgentRunRequest) (InteractiveAgentResult, error) {
	return fn(ctx, request)
}

type interactiveWorkflowStarterStub struct {
	workflowKey, interactiveRunID, idempotencyKey string
	principal                                     principalmodel.Principal
	err                                           error
}

func (s *interactiveWorkflowStarterStub) StartInteractiveAgentWorkflow(_ context.Context, workflowKey string, _ map[string]any, interactiveRunID, idempotencyKey string, principal principalmodel.Principal) (string, error) {
	s.workflowKey, s.interactiveRunID, s.idempotencyKey, s.principal = workflowKey, interactiveRunID, idempotencyKey, principal
	return "process-1", s.err
}

func interactiveExecutionFixture(t *testing.T) (*AgentAuthorizationApplicationService, principalmodel.Principal, agentmodel.GlobalAgentContext, *agentInteractiveRunRepositoryStub) {
	t.Helper()
	authorization, principal, _, _ := agentAuthorizationFixture()
	principal.SurfaceKey = "business_workspace"
	trusted, err := authorization.ResolveGlobalContext(t.Context(), GlobalAgentContextRequest{Principal: principal, EntrypointKey: "assistant.global", Surface: principal.SurfaceKey, RouteKey: "workspace.customer", ObjectKey: "customer"})
	if err != nil {
		t.Fatal(err)
	}
	return authorization, principal, trusted, &agentInteractiveRunRepositoryStub{updated: true}
}

func TestInteractiveExecutionHandsOffTaskWithoutHoldingRequestOpen(t *testing.T) {
	authorization, principal, trusted, repository := interactiveExecutionFixture(t)
	runs := NewAgentInteractiveRunApplicationService(repository, agentTaskClock{now: time.Date(2026, 8, 4, 16, 0, 0, 0, time.UTC)}, agentCredentialIDStub{})
	dispatch := NewAgentTaskDispatchApplicationService(authorization, agentTaskClock{now: time.Date(2026, 8, 4, 16, 0, 0, 0, time.UTC)}, agentCredentialIDStub{})
	runner := interactiveAgentRunnerFunc(func(_ context.Context, request InteractiveAgentRunRequest) (InteractiveAgentResult, error) {
		if len(request.Candidates) != 2 || request.Context.ContextRevision != trusted.ContextRevision || request.Deadline.IsZero() {
			t.Fatalf("runner request=%#v", request)
		}
		return InteractiveAgentResult{Route: &AgentRouteResult{RouteType: agentmodel.AgentRouteTask, TargetKey: "customer.review", TargetVersion: "1.0.0", Input: map[string]any{"record_id": "customer-1"}, IdempotencyKey: "handoff-1"}}, nil
	})
	service := NewAgentInteractiveExecutionApplicationService(AgentInteractiveExecutionDependencies{Runs: runs, Authorize: authorization, Runner: runner, Dispatch: dispatch})
	result, err := service.Execute(t.Context(), AgentInteractiveExecutionRequest{SessionID: "session-1", IdempotencyKey: "message-1", Message: "review customer", Context: trusted, Principal: principal})
	if err != nil || result.Run.Status != agentmodel.AgentInteractiveRunHandedOff || result.Result.Handoff == nil || result.Result.Handoff.TaskRunID != "agent_task_nonce" || repository.handoff.TaskRunID != "agent_task_nonce" {
		t.Fatalf("result=%#v repository=%#v err=%v", result, repository, err)
	}
}

func TestInteractiveExecutionStartsOnlyAuthorizedWorkflowAndPersistsLink(t *testing.T) {
	authorization, principal, trusted, repository := interactiveExecutionFixture(t)
	runs := NewAgentInteractiveRunApplicationService(repository, agentTaskClock{now: time.Now()}, agentCredentialIDStub{})
	workflows := &interactiveWorkflowStarterStub{}
	runner := interactiveAgentRunnerFunc(func(context.Context, InteractiveAgentRunRequest) (InteractiveAgentResult, error) {
		return InteractiveAgentResult{Route: &AgentRouteResult{RouteType: agentmodel.AgentRouteWorkflow, TargetKey: "customer.flow", Input: map[string]any{"record_id": "customer-1"}, IdempotencyKey: "workflow-1"}}, nil
	})
	service := NewAgentInteractiveExecutionApplicationService(AgentInteractiveExecutionDependencies{Runs: runs, Authorize: authorization, Runner: runner, Workflows: workflows})
	result, err := service.Execute(t.Context(), AgentInteractiveExecutionRequest{SessionID: "session-1", IdempotencyKey: "message-1", Message: "start workflow", Context: trusted, Principal: principal})
	if err != nil || result.Run.ProcessID != "process-1" || result.Result.Handoff == nil || workflows.workflowKey != "customer.flow" || workflows.interactiveRunID != result.Run.ID || workflows.idempotencyKey != "workflow-1" || workflows.principal.AuthorizationRevision != "operator-rev-2" {
		t.Fatalf("result=%#v workflows=%#v err=%v", result, workflows, err)
	}
}

func TestInteractiveExecutionRejectsForgedRouteAndClassifiesTimeout(t *testing.T) {
	for name, runner := range map[string]interactiveAgentRunnerFunc{
		"forged route": func(context.Context, InteractiveAgentRunRequest) (InteractiveAgentResult, error) {
			return InteractiveAgentResult{Route: &AgentRouteResult{RouteType: agentmodel.AgentRouteTask, TargetKey: "forged", TargetVersion: "v1", IdempotencyKey: "one"}}, nil
		},
		"timeout": func(context.Context, InteractiveAgentRunRequest) (InteractiveAgentResult, error) {
			return InteractiveAgentResult{}, context.DeadlineExceeded
		},
	} {
		t.Run(name, func(t *testing.T) {
			authorization, principal, trusted, repository := interactiveExecutionFixture(t)
			service := NewAgentInteractiveExecutionApplicationService(AgentInteractiveExecutionDependencies{Runs: NewAgentInteractiveRunApplicationService(repository, nil, nil), Authorize: authorization, Runner: runner})
			result, err := service.Execute(t.Context(), AgentInteractiveExecutionRequest{SessionID: "session", IdempotencyKey: "message", Message: "request", Context: trusted, Principal: principal})
			if err == nil || result.Run.Status != agentmodel.AgentInteractiveRunFailed {
				t.Fatalf("result=%#v err=%v", result, err)
			}
			if name == "timeout" && apperror.CodeOf(err) != "agent.interactive.timeout" {
				t.Fatalf("timeout code=%q err=%v", apperror.CodeOf(err), err)
			}
		})
	}
	unavailable := NewAgentInteractiveExecutionApplicationService(AgentInteractiveExecutionDependencies{})
	if _, err := unavailable.Execute(t.Context(), AgentInteractiveExecutionRequest{}); apperror.CodeOf(err) != "agent.interactive.runner_unavailable" {
		t.Fatalf("unavailable=%v", err)
	}
}

func TestInteractiveExecutionDependencyRequestAuthorizationAndCreateBoundaries(t *testing.T) {
	authorization, principal, trusted, repository := interactiveExecutionFixture(t)
	runs := NewAgentInteractiveRunApplicationService(repository, nil, nil)
	runner := interactiveAgentRunnerFunc(func(context.Context, InteractiveAgentRunRequest) (InteractiveAgentResult, error) {
		return InteractiveAgentResult{}, nil
	})
	for name, service := range map[string]*AgentInteractiveExecutionApplicationService{
		"nil":       nil,
		"runs":      NewAgentInteractiveExecutionApplicationService(AgentInteractiveExecutionDependencies{}),
		"authorize": NewAgentInteractiveExecutionApplicationService(AgentInteractiveExecutionDependencies{Runs: runs}),
		"runner":    NewAgentInteractiveExecutionApplicationService(AgentInteractiveExecutionDependencies{Runs: runs, Authorize: authorization}),
	} {
		if _, err := service.Execute(t.Context(), AgentInteractiveExecutionRequest{}); apperror.CodeOf(err) != "agent.interactive.runner_unavailable" {
			t.Fatalf("%s=%v", name, err)
		}
	}
	service := NewAgentInteractiveExecutionApplicationService(AgentInteractiveExecutionDependencies{Runs: runs, Authorize: authorization, Runner: runner})
	for name, request := range map[string]AgentInteractiveExecutionRequest{
		"message": {IdempotencyKey: "key"},
		"key":     {Message: "message"},
	} {
		if _, err := service.Execute(t.Context(), request); apperror.CodeOf(err) != "agent.interactive.request_invalid" {
			t.Fatalf("%s=%v", name, err)
		}
	}
	badContext := trusted
	badContext.ContextRevision = "stale"
	if _, err := service.Execute(t.Context(), AgentInteractiveExecutionRequest{SessionID: "session", IdempotencyKey: "key", Message: "message", Context: badContext, Principal: principal}); err == nil || runs.metricsSnapshot().PermissionDenied != 1 {
		t.Fatalf("authorization=%v metrics=%#v", err, runs.metricsSnapshot())
	}
	repository.createErr = errors.New("create")
	if _, err := service.Execute(t.Context(), AgentInteractiveExecutionRequest{SessionID: "session", IdempotencyKey: "key", Message: "message", Context: trusted, Principal: principal}); !errors.Is(err, repository.createErr) {
		t.Fatalf("create=%v", err)
	}
}

func TestInteractiveExecutionReplayTimeoutCompletionAndRouteNilBoundaries(t *testing.T) {
	requestFor := func(principal principalmodel.Principal, trusted agentmodel.GlobalAgentContext) AgentInteractiveExecutionRequest {
		return AgentInteractiveExecutionRequest{SessionID: "session", IdempotencyKey: "key", Message: " message ", Context: trusted, Principal: principal}
	}
	t.Run("replayed running resumes", func(t *testing.T) {
		authorization, principal, trusted, repository := interactiveExecutionFixture(t)
		repository.replayed, repository.preserveRunOnCreate = true, true
		repository.run = agentmodel.AgentInteractiveRun{ID: "existing", WorkspaceID: principal.WorkspaceID, SessionID: "session", UserID: principal.UserID, RoleKey: principal.RoleKey, Status: agentmodel.AgentInteractiveRunRunning, Context: trusted, ContextRevision: trusted.ContextRevision, Revision: 1}
		called := false
		service := NewAgentInteractiveExecutionApplicationService(AgentInteractiveExecutionDependencies{Runs: NewAgentInteractiveRunApplicationService(repository, nil, nil), Authorize: authorization, Runner: interactiveAgentRunnerFunc(func(context.Context, InteractiveAgentRunRequest) (InteractiveAgentResult, error) {
			called = true
			return InteractiveAgentResult{}, nil
		})})
		if _, err := service.Execute(t.Context(), requestFor(principal, trusted)); err != nil || !called {
			t.Fatalf("called=%v err=%v", called, err)
		}
	})
	t.Run("immediate provider error", func(t *testing.T) {
		authorization, principal, trusted, repository := interactiveExecutionFixture(t)
		service := NewAgentInteractiveExecutionApplicationService(AgentInteractiveExecutionDependencies{Runs: NewAgentInteractiveRunApplicationService(repository, nil, nil), Authorize: authorization, Runner: interactiveAgentRunnerFunc(func(context.Context, InteractiveAgentRunRequest) (InteractiveAgentResult, error) {
			return InteractiveAgentResult{}, errors.New("provider")
		})})
		if _, err := service.Execute(t.Context(), requestFor(principal, trusted)); err == nil {
			t.Fatal("expected provider error")
		}
	})
	for name, status := range map[string]agentmodel.AgentInteractiveRunStatus{"completed": agentmodel.AgentInteractiveRunCompleted, "handed": agentmodel.AgentInteractiveRunHandedOff} {
		authorization, principal, trusted, repository := interactiveExecutionFixture(t)
		repository.replayed = true
		repository.preserveRunOnCreate = true
		repository.run = agentmodel.AgentInteractiveRun{ID: "existing", WorkspaceID: principal.WorkspaceID, Status: status, StructuredResult: map[string]any{"ok": true}, RouteType: agentmodel.AgentRouteTask, RoutedTargetKey: "customer.review", TaskRunID: "task"}
		service := NewAgentInteractiveExecutionApplicationService(AgentInteractiveExecutionDependencies{Runs: NewAgentInteractiveRunApplicationService(repository, nil, nil), Authorize: authorization, Runner: interactiveAgentRunnerFunc(func(context.Context, InteractiveAgentRunRequest) (InteractiveAgentResult, error) {
			t.Fatal("runner called on replay")
			return InteractiveAgentResult{}, nil
		})})
		result, err := service.Execute(t.Context(), requestFor(principal, trusted))
		if err != nil || result.Run.ID != "existing" {
			t.Fatalf("%s replay=%#v err=%v", name, result, err)
		}
		if name == "handed" && result.Result.Handoff == nil {
			t.Fatal("handoff not restored")
		}
	}
	t.Run("explicit timeout and route nil", func(t *testing.T) {
		authorization, principal, trusted, repository := interactiveExecutionFixture(t)
		schema := authorization.schema.(*agentSchemaProviderStub)
		schema.full.Agents[0].ExecutionLimits.TimeoutSeconds = 1
		visible := schema.filtered["operator:operator"]
		visible.Agents[0].ExecutionLimits.TimeoutSeconds = 1
		schema.filtered["operator:operator"] = visible
		service := NewAgentInteractiveExecutionApplicationService(AgentInteractiveExecutionDependencies{Runs: NewAgentInteractiveRunApplicationService(repository, nil, nil), Authorize: authorization, Runner: interactiveAgentRunnerFunc(func(_ context.Context, request InteractiveAgentRunRequest) (InteractiveAgentResult, error) {
			if request.MaxSteps != 0 {
				t.Fatalf("request=%#v", request)
			}
			return InteractiveAgentResult{ExternalRunID: " external ", Model: " model ", Usage: map[string]any{"tokens": 1}, Structured: map[string]any{"answer": true}}, nil
		})})
		result, err := service.Execute(t.Context(), requestFor(principal, trusted))
		if err != nil || result.Run.Status != agentmodel.AgentInteractiveRunCompleted || result.Run.ExternalRunID != "external" {
			t.Fatalf("result=%#v err=%v", result, err)
		}
	})
	t.Run("work context timeout and complete error", func(t *testing.T) {
		authorization, principal, trusted, repository := interactiveExecutionFixture(t)
		schema := authorization.schema.(*agentSchemaProviderStub)
		schema.full.Agents[0].ExecutionLimits.TimeoutSeconds = 1
		visible := schema.filtered["operator:operator"]
		visible.Agents[0].ExecutionLimits.TimeoutSeconds = 1
		schema.filtered["operator:operator"] = visible
		repository.saveErr = errors.New("complete")
		service := NewAgentInteractiveExecutionApplicationService(AgentInteractiveExecutionDependencies{Runs: NewAgentInteractiveRunApplicationService(repository, nil, nil), Authorize: authorization, Runner: interactiveAgentRunnerFunc(func(ctx context.Context, _ InteractiveAgentRunRequest) (InteractiveAgentResult, error) {
			<-ctx.Done()
			return InteractiveAgentResult{}, errors.New("provider ended")
		})})
		if _, err := service.Execute(t.Context(), requestFor(principal, trusted)); !errors.Is(err, repository.saveErr) {
			t.Fatalf("complete error=%v", err)
		}
	})
}

func TestInteractiveExecutionHandoffAndToolFailureBoundaries(t *testing.T) {
	request := func(principal principalmodel.Principal, trusted agentmodel.GlobalAgentContext) AgentInteractiveExecutionRequest {
		return AgentInteractiveExecutionRequest{SessionID: "session", IdempotencyKey: "key", Message: "message", Context: trusted, Principal: principal}
	}
	for name, test := range map[string]struct {
		route     AgentRouteResult
		configure func(*AgentInteractiveExecutionDependencies, *agentInteractiveRunRepositoryStub)
	}{
		"task missing": {route: AgentRouteResult{RouteType: agentmodel.AgentRouteTask, TargetKey: "customer.review", TargetVersion: "1.0.0", IdempotencyKey: "route"}, configure: func(_ *AgentInteractiveExecutionDependencies, _ *agentInteractiveRunRepositoryStub) {}},
		"task prepare": {route: AgentRouteResult{RouteType: agentmodel.AgentRouteTask, TargetKey: "customer.review", TargetVersion: "1.0.0", IdempotencyKey: "route"}, configure: func(d *AgentInteractiveExecutionDependencies, _ *agentInteractiveRunRepositoryStub) {
			d.Dispatch = NewAgentTaskDispatchApplicationService(nil, nil, nil)
		}},
		"task handoff": {route: AgentRouteResult{RouteType: agentmodel.AgentRouteTask, TargetKey: "customer.review", TargetVersion: "1.0.0", IdempotencyKey: "route"}, configure: func(d *AgentInteractiveExecutionDependencies, r *agentInteractiveRunRepositoryStub) {
			d.Dispatch = NewAgentTaskDispatchApplicationService(d.Authorize, nil, nil)
			r.handoffErr = errors.New("handoff")
		}},
		"workflow missing": {route: AgentRouteResult{RouteType: agentmodel.AgentRouteWorkflow, TargetKey: "customer.flow", IdempotencyKey: "route"}, configure: func(_ *AgentInteractiveExecutionDependencies, _ *agentInteractiveRunRepositoryStub) {}},
		"workflow start": {route: AgentRouteResult{RouteType: agentmodel.AgentRouteWorkflow, TargetKey: "customer.flow", IdempotencyKey: "route"}, configure: func(d *AgentInteractiveExecutionDependencies, _ *agentInteractiveRunRepositoryStub) {
			d.Workflows = &interactiveWorkflowStarterStub{err: errors.New("start")}
		}},
		"workflow handoff": {route: AgentRouteResult{RouteType: agentmodel.AgentRouteWorkflow, TargetKey: "customer.flow", IdempotencyKey: "route"}, configure: func(d *AgentInteractiveExecutionDependencies, r *agentInteractiveRunRepositoryStub) {
			d.Workflows = &interactiveWorkflowStarterStub{}
			r.saveErr = errors.New("handoff")
		}},
		"tool missing": {route: AgentRouteResult{RouteType: agentmodel.AgentRouteInteractiveQuery, TargetKey: "customer_agent", TargetVersion: "1.0.0", Input: map[string]any{"tool": AgentToolQueryRecords, "object_key": "customer"}, IdempotencyKey: "route"}, configure: func(_ *AgentInteractiveExecutionDependencies, _ *agentInteractiveRunRepositoryStub) {}},
	} {
		t.Run(name, func(t *testing.T) {
			authorization, principal, trusted, repository := interactiveExecutionFixture(t)
			schema := authorization.schema.(*agentSchemaProviderStub)
			schema.full.AgentEntrypoints[0].RoutingContract.AllowedRouteTypes = []string{agentmodel.AgentRouteTask, agentmodel.AgentRouteWorkflow, agentmodel.AgentRouteInteractiveQuery}
			visible := schema.filtered["operator:operator"]
			visible.AgentEntrypoints = schema.full.AgentEntrypoints
			schema.filtered["operator:operator"] = visible
			dependencies := AgentInteractiveExecutionDependencies{Runs: NewAgentInteractiveRunApplicationService(repository, nil, nil), Authorize: authorization, Runner: interactiveAgentRunnerFunc(func(context.Context, InteractiveAgentRunRequest) (InteractiveAgentResult, error) {
				value := test.route
				return InteractiveAgentResult{Route: &value}, nil
			})}
			test.configure(&dependencies, repository)
			if _, err := NewAgentInteractiveExecutionApplicationService(dependencies).Execute(t.Context(), request(principal, trusted)); err == nil {
				t.Fatal("expected error")
			}
		})
	}
	t.Run("tool invocation and final completion errors", func(t *testing.T) {
		authorization, principal, trusted, repository := interactiveExecutionFixture(t)
		schema := authorization.schema.(*agentSchemaProviderStub)
		schema.full.AgentEntrypoints[0].RoutingContract.AllowedRouteTypes = []string{agentmodel.AgentRouteInteractiveQuery}
		visible := schema.filtered["operator:operator"]
		visible.AgentEntrypoints = schema.full.AgentEntrypoints
		schema.filtered["operator:operator"] = visible
		runs := NewAgentInteractiveRunApplicationService(repository, nil, nil)
		gateway := NewAgentToolGateway(AgentToolGatewayDependencies{Authorization: authorization, Queries: &agentToolQueryStub{err: errors.New("query")}, InteractiveRuns: runs})
		route := AgentRouteResult{RouteType: agentmodel.AgentRouteInteractiveQuery, TargetKey: "customer_agent", TargetVersion: "1.0.0", Input: map[string]any{"tool": AgentToolQueryRecords, "object_key": "customer"}, IdempotencyKey: "route"}
		service := NewAgentInteractiveExecutionApplicationService(AgentInteractiveExecutionDependencies{Runs: runs, Authorize: authorization, Runner: interactiveAgentRunnerFunc(func(context.Context, InteractiveAgentRunRequest) (InteractiveAgentResult, error) {
			return InteractiveAgentResult{Route: &route}, nil
		}), Tools: gateway})
		if _, err := service.Execute(t.Context(), request(principal, trusted)); err == nil {
			t.Fatal("expected tool error")
		}
		repository.saveErr = errors.New("fail complete")
		if _, err := service.Execute(t.Context(), AgentInteractiveExecutionRequest{SessionID: "session", IdempotencyKey: "key-2", Message: "message", Context: trusted, Principal: principal}); !errors.Is(err, repository.saveErr) {
			t.Fatalf("fail complete=%v", err)
		}
	})
	t.Run("tool invocation succeeds", func(t *testing.T) {
		authorization, principal, trusted, repository := interactiveExecutionFixture(t)
		schema := authorization.schema.(*agentSchemaProviderStub)
		schema.full.AgentEntrypoints[0].RoutingContract.AllowedRouteTypes = []string{agentmodel.AgentRouteInteractiveQuery}
		visible := schema.filtered["operator:operator"]
		visible.AgentEntrypoints = schema.full.AgentEntrypoints
		schema.filtered["operator:operator"] = visible
		runs := NewAgentInteractiveRunApplicationService(repository, nil, nil)
		gateway := NewAgentToolGateway(AgentToolGatewayDependencies{Authorization: authorization, Queries: &agentToolQueryStub{}, InteractiveRuns: runs})
		route := AgentRouteResult{RouteType: agentmodel.AgentRouteInteractiveQuery, TargetKey: "customer_agent", TargetVersion: "1.0.0", Input: map[string]any{"tool": AgentToolQueryRecords, "object_key": "customer"}, IdempotencyKey: "route"}
		service := NewAgentInteractiveExecutionApplicationService(AgentInteractiveExecutionDependencies{Runs: runs, Authorize: authorization, Runner: interactiveAgentRunnerFunc(func(context.Context, InteractiveAgentRunRequest) (InteractiveAgentResult, error) {
			return InteractiveAgentResult{Route: &route}, nil
		}), Tools: gateway})
		result, err := service.Execute(t.Context(), request(principal, trusted))
		if err != nil || result.Run.Status != agentmodel.AgentInteractiveRunCompleted || result.Result.Structured["status"] != "executed" {
			t.Fatalf("result=%#v err=%v", result, err)
		}
	})
}
