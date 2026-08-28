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

func TestAgentTaskDispatchPreparesAuthorizedDurableRun(t *testing.T) {
	authorization, initiator, _, _ := agentAuthorizationFixture()
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	service := NewAgentTaskDispatchApplicationService(authorization, agentTaskClock{now: now}, agentCredentialIDStub{})
	run, err := service.Prepare(t.Context(), AgentTaskDispatchRequest{
		WorkspaceID: "workspace-1", ProcessID: "process-1", NodeInstanceID: "node-instance-1", NodeID: "review", Iteration: 2,
		DefinitionSnapshotHash: "definition-hash", ManifestHash: "manifest-hash", TaskKey: "customer.review", TaskVersion: "1.0.0",
		Identity: agentmodel.AgentTaskIdentity{Mode: agentmodel.AgentTaskIdentityInherit}, Input: map[string]any{"record_id": "customer-1"},
		AllowedObjects: []string{"customer"}, AllowedActions: []string{"customer.update"}, AllowedOutcomes: []string{"success"},
		TimeoutSeconds: 30, MaxAttempts: 3, Initiator: initiator, CorrelationID: "correlation-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if run.ID != "agent_task_nonce" || run.IdempotencyKey != "process-1:review:2" || run.Status != agentmodel.AgentTaskRunPending || run.MaxAttempts != 3 || run.TimeoutSeconds != 30 {
		t.Fatalf("run contract = %#v", run)
	}
	if run.Identity.Initiator.AuthorizationRevision != "operator-rev-1" || run.Identity.Execution.AuthorizationRevision != "operator-rev-2" || len(run.Evidence.Authorization) != 1 || run.Evidence.ManifestHash != "manifest-hash" || run.Evidence.DefinitionSnapshotHash != "definition-hash" {
		t.Fatalf("identity/evidence = %#v", run)
	}
}

func TestAgentTaskDispatchFailsClosedAtBoundaries(t *testing.T) {
	authorization, initiator, _, _ := agentAuthorizationFixture()
	service := NewAgentTaskDispatchApplicationService(authorization, agentTaskClock{now: time.Now()}, agentCredentialIDStub{})
	valid := AgentTaskDispatchRequest{WorkspaceID: initiator.WorkspaceID, ProcessID: "process", NodeInstanceID: "node", NodeID: "task", Iteration: 1, TaskKey: "customer.review", TaskVersion: "1.0.0", Identity: agentmodel.AgentTaskIdentity{Mode: agentmodel.AgentTaskIdentityInherit}, Initiator: initiator}
	for name, mutate := range map[string]func(*AgentTaskDispatchRequest){
		"workspace": func(r *AgentTaskDispatchRequest) { r.WorkspaceID = "other" },
		"process":   func(r *AgentTaskDispatchRequest) { r.ProcessID = "" },
		"iteration": func(r *AgentTaskDispatchRequest) { r.Iteration = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			request := valid
			mutate(&request)
			if _, err := service.Prepare(t.Context(), request); apperror.CodeOf(err) != "agent.task.dispatch_invalid" {
				t.Fatalf("error=%v", err)
			}
		})
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.Prepare(cancelled, valid); err == nil {
		t.Fatal("expected authorization resolver to retain cancellation")
	}
	if _, err := NewAgentTaskDispatchApplicationService(nil, nil, nil).Prepare(t.Context(), AgentTaskDispatchRequest{Initiator: principalmodel.Principal{}}); apperror.CodeOf(err) != "agent.task.dispatch_unavailable" {
		t.Fatalf("unavailable=%v", err)
	}
}

func TestAgentTaskDispatchPreparesStandaloneInteractiveHandoff(t *testing.T) {
	authorization, initiator, _, _ := agentAuthorizationFixture()
	dispatch := NewAgentTaskDispatchApplicationService(authorization, agentTaskClock{now: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)}, agentCredentialIDStub{})
	run, err := dispatch.PrepareInteractive(t.Context(), AgentInteractiveTaskDispatchRequest{
		InteractiveRunID: "interactive-1", WorkspaceID: initiator.WorkspaceID, TaskKey: "customer.review", TaskVersion: "1.0.0", IdempotencyKey: "interactive-1:handoff-1",
		Identity: agentmodel.AgentTaskIdentity{Mode: agentmodel.AgentTaskIdentityInherit}, AllowedObjects: []string{"customer"}, AllowedActions: []string{"customer.update"}, AllowedOutcomes: []string{"success"}, Initiator: initiator,
	})
	if err != nil || run.InteractiveRunID != "interactive-1" || run.ProcessID != "" || run.NodeInstanceID != "" || run.IdempotencyKey != "interactive-1:handoff-1" || run.Identity.Execution.UserID != initiator.UserID {
		t.Fatalf("run=%#v err=%v", run, err)
	}
	if _, err := dispatch.PrepareInteractive(t.Context(), AgentInteractiveTaskDispatchRequest{WorkspaceID: initiator.WorkspaceID, Initiator: initiator}); apperror.CodeOf(err) != "agent.task.dispatch_invalid" {
		t.Fatalf("invalid err=%v", err)
	}
}

func TestAgentTaskDispatchBoundaryMatrix(t *testing.T) {
	authorization, initiator, _, _ := agentAuthorizationFixture()
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	service := NewAgentTaskDispatchApplicationService(authorization, agentTaskClock{now: now}, agentCredentialIDStub{})
	if defaults := NewAgentTaskDispatchApplicationService(authorization, nil, nil); defaults.clock == nil || defaults.ids == nil {
		t.Fatal("default dispatch dependencies missing")
	}
	valid := AgentTaskDispatchRequest{WorkspaceID: initiator.WorkspaceID, ProcessID: "process", NodeInstanceID: "node", NodeID: "task", Iteration: 1, TaskKey: "customer.review", TaskVersion: "1.0.0", Identity: agentmodel.AgentTaskIdentity{Mode: agentmodel.AgentTaskIdentityInherit}, Initiator: initiator}
	if _, err := (*AgentTaskDispatchApplicationService)(nil).Prepare(t.Context(), valid); apperror.CodeOf(err) != "agent.task.dispatch_unavailable" {
		t.Fatalf("nil prepare=%v", err)
	}
	invalidWorkspace := valid
	invalidWorkspace.WorkspaceID = ""
	if _, err := service.Prepare(t.Context(), invalidWorkspace); apperror.CodeOf(err) != "agent.task.dispatch_invalid" {
		t.Fatalf("invalid workspace=%v", err)
	}
	missingNode := valid
	missingNode.NodeInstanceID = ""
	if _, err := service.Prepare(t.Context(), missingNode); apperror.CodeOf(err) != "agent.task.dispatch_invalid" {
		t.Fatalf("missing node=%v", err)
	}
	unauthorized := valid
	unauthorized.TaskVersion = "missing"
	if _, err := service.Prepare(t.Context(), unauthorized); apperror.CodeOf(err) != "agent.authorization.task_unpublished" {
		t.Fatalf("authorization=%v", err)
	}
	provided := valid
	provided.RunID, provided.MaxAttempts, provided.TimeoutSeconds = "provided", 0, 0
	run, err := service.Prepare(t.Context(), provided)
	if err != nil || run.ID != "provided" || run.MaxAttempts != 1 || run.TimeoutSeconds != 0 {
		t.Fatalf("defaults run=%#v err=%v", run, err)
	}
	if _, err := NewAgentTaskDispatchApplicationService(authorization, agentTaskClock{}, agentCredentialIDStub{}).Prepare(t.Context(), valid); apperror.CodeOf(err) != "agent.task.dispatch_invalid" {
		t.Fatalf("zero clock run=%v", err)
	}

	interactive := AgentInteractiveTaskDispatchRequest{WorkspaceID: initiator.WorkspaceID, InteractiveRunID: "interactive", IdempotencyKey: "idem", TaskKey: "customer.review", TaskVersion: "1.0.0", Identity: agentmodel.AgentTaskIdentity{Mode: agentmodel.AgentTaskIdentityInherit}, Initiator: initiator}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.PrepareInteractive(cancelled, interactive); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled interactive=%v", err)
	}
	if _, err := (*AgentTaskDispatchApplicationService)(nil).PrepareInteractive(t.Context(), interactive); apperror.CodeOf(err) != "agent.task.dispatch_unavailable" {
		t.Fatalf("nil interactive=%v", err)
	}
	if _, err := NewAgentTaskDispatchApplicationService(nil, nil, nil).PrepareInteractive(t.Context(), interactive); apperror.CodeOf(err) != "agent.task.dispatch_unavailable" {
		t.Fatalf("missing auth interactive=%v", err)
	}
	for name, mutate := range map[string]func(*AgentInteractiveTaskDispatchRequest){
		"workspace invalid":  func(value *AgentInteractiveTaskDispatchRequest) { value.WorkspaceID = "" },
		"workspace mismatch": func(value *AgentInteractiveTaskDispatchRequest) { value.WorkspaceID = "other" },
		"run":                func(value *AgentInteractiveTaskDispatchRequest) { value.InteractiveRunID = "" },
		"idempotency":        func(value *AgentInteractiveTaskDispatchRequest) { value.IdempotencyKey = "" },
		"authorization":      func(value *AgentInteractiveTaskDispatchRequest) { value.TaskVersion = "missing" },
	} {
		t.Run(name, func(t *testing.T) {
			candidate := interactive
			mutate(&candidate)
			if _, err := service.PrepareInteractive(t.Context(), candidate); err == nil {
				t.Fatal("boundary accepted")
			}
		})
	}
	interactive.RunID, interactive.MaxAttempts, interactive.TimeoutSeconds = "provided", 0, 0
	run, err = service.PrepareInteractive(t.Context(), interactive)
	if err != nil || run.ID != "provided" || run.MaxAttempts != 1 || run.TimeoutSeconds != 0 {
		t.Fatalf("interactive defaults=%#v err=%v", run, err)
	}
	interactive.MaxAttempts, interactive.TimeoutSeconds = 2, 30
	if run, err := service.PrepareInteractive(t.Context(), interactive); err != nil || run.MaxAttempts != 2 || run.TimeoutSeconds != 30 {
		t.Fatalf("interactive explicit limits=%#v err=%v", run, err)
	}
	if _, err := NewAgentTaskDispatchApplicationService(authorization, agentTaskClock{}, agentCredentialIDStub{}).PrepareInteractive(t.Context(), interactive); apperror.CodeOf(err) != "agent.task.dispatch_invalid" {
		t.Fatalf("interactive zero clock=%v", err)
	}
}
