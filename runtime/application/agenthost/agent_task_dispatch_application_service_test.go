package agenthost

import (
	"context"
	"errors"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-foundation/apperror"
)

func TestAgentTaskDispatchPreparesAuthorizedSDKRequest(t *testing.T) {
	authorization, initiator, _, _ := agentAuthorizationFixture()
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	service := NewAgentTaskDispatchApplicationService(authorization, agentTaskClock{now: now}, agentCredentialIDStub{})
	request, err := service.PrepareRequest(t.Context(), AgentTaskDispatchRequest{
		WorkspaceID: "workspace-1", ProcessID: "process-1", NodeInstanceID: "node-instance-1", NodeID: "review", Iteration: 2,
		TaskKey: "customer.review", TaskVersion: "1.0.0", Identity: agentsdk.AgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityInherit},
		Input: map[string]any{"record_id": "customer-1"}, AllowedObjects: []string{"customer"},
		AllowedActions: []string{"customer.update"}, AllowedOutcomes: []string{"success"},
		TimeoutSeconds: 30, MaxAttempts: 3, Initiator: initiator, CorrelationID: "correlation-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if request.TaskRunID != "agent_task_nonce" || request.IdempotencyKey != "process-1:review:2" || request.MaxAttempts != 3 {
		t.Fatalf("SDK request contract = %#v", request)
	}
	if request.ProcessID != "process-1" || request.NodeInstanceID != "node-instance-1" || request.WorkspaceID != "workspace-1" || request.CorrelationID != "correlation-1" {
		t.Fatalf("workflow correlation = %#v", request)
	}
	if request.Identity.Initiator.AuthorizationRevision != "operator-rev-1" || request.Identity.Execution.AuthorizationRevision != "operator-rev-2" {
		t.Fatalf("identity = %#v", request.Identity)
	}
	if !request.Deadline.Equal(now.Add(30*time.Second)) || len(request.AllowedTools) == 0 || request.Input["record_id"] != "customer-1" {
		t.Fatalf("limits/capabilities = %#v", request)
	}
}

func TestAgentTaskDispatchFailsClosedAtBoundaries(t *testing.T) {
	authorization, initiator, _, _ := agentAuthorizationFixture()
	service := NewAgentTaskDispatchApplicationService(authorization, agentTaskClock{now: time.Now()}, agentCredentialIDStub{})
	valid := AgentTaskDispatchRequest{WorkspaceID: initiator.WorkspaceID, ProcessID: "process", NodeInstanceID: "node", NodeID: "task", Iteration: 1, TaskKey: "customer.review", TaskVersion: "1.0.0", Identity: agentsdk.AgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityInherit}, Initiator: initiator}
	for name, mutate := range map[string]func(*AgentTaskDispatchRequest){
		"workspace": func(r *AgentTaskDispatchRequest) { r.WorkspaceID = "other" },
		"process":   func(r *AgentTaskDispatchRequest) { r.ProcessID = "" },
		"node":      func(r *AgentTaskDispatchRequest) { r.NodeInstanceID = "" },
		"iteration": func(r *AgentTaskDispatchRequest) { r.Iteration = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			request := valid
			mutate(&request)
			if _, err := service.PrepareRequest(t.Context(), request); apperror.CodeOf(err) != "agent.task.dispatch_invalid" {
				t.Fatalf("error=%v", err)
			}
		})
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.PrepareRequest(cancelled, valid); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled=%v", err)
	}
	if _, err := NewAgentTaskDispatchApplicationService(nil, nil, nil).PrepareRequest(t.Context(), valid); apperror.CodeOf(err) != "agent.task.dispatch_unavailable" {
		t.Fatalf("unavailable=%v", err)
	}
}

func TestAgentTaskDispatchRequestDefaultsAndAuthorization(t *testing.T) {
	authorization, initiator, _, _ := agentAuthorizationFixture()
	service := NewAgentTaskDispatchApplicationService(authorization, agentTaskClock{now: time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)}, agentCredentialIDStub{})
	if defaults := NewAgentTaskDispatchApplicationService(authorization, nil, nil); defaults.clock == nil || defaults.ids == nil {
		t.Fatal("default dispatch dependencies missing")
	}
	valid := AgentTaskDispatchRequest{WorkspaceID: initiator.WorkspaceID, ProcessID: "process", NodeInstanceID: "node", NodeID: "task", Iteration: 1, TaskKey: "customer.review", TaskVersion: "1.0.0", Identity: agentsdk.AgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityInherit}, Initiator: initiator}
	if _, err := (*AgentTaskDispatchApplicationService)(nil).PrepareRequest(t.Context(), valid); apperror.CodeOf(err) != "agent.task.dispatch_unavailable" {
		t.Fatalf("nil service=%v", err)
	}
	unauthorized := valid
	unauthorized.TaskVersion = "missing"
	if _, err := service.PrepareRequest(t.Context(), unauthorized); apperror.CodeOf(err) != "agent.authorization.task_unpublished" {
		t.Fatalf("authorization=%v", err)
	}
	provided := valid
	provided.RunID = "provided"
	request, err := service.PrepareRequest(t.Context(), provided)
	if err != nil || request.TaskRunID != "provided" || request.MaxAttempts != 1 || !request.Deadline.IsZero() {
		t.Fatalf("defaults request=%#v err=%v", request, err)
	}
	if _, err := NewAgentTaskDispatchApplicationService(authorization, agentTaskClock{}, agentCredentialIDStub{}).PrepareRequest(t.Context(), AgentTaskDispatchRequest{
		WorkspaceID: initiator.WorkspaceID, ProcessID: "process", NodeInstanceID: "node", NodeID: "task", Iteration: 1,
		TaskKey: "customer.review", TaskVersion: "1.0.0", Identity: agentsdk.AgentTaskIdentity{Mode: agentsdk.AgentTaskIdentityInherit}, Initiator: initiator, TimeoutSeconds: 30,
	}); apperror.CodeOf(err) != "agent.task.dispatch_invalid" {
		t.Fatalf("zero clock=%v", err)
	}
}
