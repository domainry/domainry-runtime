package integrationtest

import (
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestManualWorkflowCallerKeyReplaysAndRejectsPayloadConflict(t *testing.T) {
	workflow := definitionmodel.WorkflowSchema{
		Key: "manual_caller_idempotency", Name: "Manual caller idempotency", Enabled: true,
		Graph: &definitionmodel.WorkflowGraphSchema{Version: 2, Nodes: []definitionmodel.WorkflowGraphNode{
			{ID: "start", Type: "trigger", Name: "Start"},
		}},
	}
	store, services := workflowPolicyTestRuntime(t, workflow)
	defer store.Close()
	workflows := services.Applications().Workflows
	principal := workflowPolicyPrincipal("employee", workflow.Key)
	payload := map[string]any{"order_id": "order-1", "initiating_user_id": "forged-user", "initiating_role_key": "forged-role"}

	first, err := workflows.RunWorkflowWithKey(t.Context(), workflow.Key, payload, " manual-request-1 ", principal)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := workflows.RunWorkflowWithKey(t.Context(), workflow.Key, map[string]any{
		"order_id": "order-1", "initiating_user_id": "different-forgery", "initiating_role_key": "different-forgery",
	}, "manual-request-1", principal)
	if err != nil {
		t.Fatal(err)
	}
	if replay.Execution.ID != first.Execution.ID {
		t.Fatalf("replay created a second execution: first=%q replay=%q", first.Execution.ID, replay.Execution.ID)
	}
	if first.Execution.IdempotencyKey != "manual-request-1" {
		t.Fatalf("caller key was not persisted on execution: %q", first.Execution.IdempotencyKey)
	}
	if first.Execution.Payload["initiating_user_id"] != "employee" || first.Execution.Payload["initiating_role_key"] != "employee" {
		t.Fatalf("manual execution did not persist trusted initiator payload: %#v", first.Execution.Payload)
	}
	if payload["initiating_user_id"] != "forged-user" || payload["initiating_role_key"] != "forged-role" {
		t.Fatalf("caller payload mutated: %#v", payload)
	}
	executions, err := workflows.WorkflowExecutions(t.Context(), principal, "", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(executions) != 1 {
		t.Fatalf("same caller key replay created %d executions", len(executions))
	}

	_, err = workflows.RunWorkflowWithKey(t.Context(), workflow.Key, map[string]any{"order_id": "order-2"}, "manual-request-1", principal)
	if apperror.CodeOf(err) != idempotency.ErrorCodeKeyReused {
		t.Fatalf("payload conflict error=%v code=%q", err, apperror.CodeOf(err))
	}

	_, err = workflows.RunWorkflowWithKey(t.Context(), workflow.Key, map[string]any{"order_id": "order-1"}, "manual-request-1", workflowPolicyPrincipal("manager", workflow.Key))
	if apperror.CodeOf(err) != idempotency.ErrorCodeKeyReused {
		t.Fatalf("trusted initiator conflict error=%v code=%q", err, apperror.CodeOf(err))
	}
}
