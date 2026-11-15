package workflows

import (
	"encoding/json"
	"testing"

	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func assertWorkflowOperationRoundTrip[T any](t *testing.T, value T) {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var cached any
	if err = json.Unmarshal(raw, &cached); err != nil {
		t.Fatal(err)
	}
	for _, operation := range []operationsapplication.OperationsOwnerExecutionResult{{Value: value}, {Value: cached, Replayed: true}} {
		got, ok := workflowOperationValue[T](operation)
		if !ok {
			t.Fatal("committed replay lost response type")
		}
		encoded, err := json.Marshal(got)
		if err != nil || string(encoded) != string(raw) {
			t.Fatalf("response changed: %s / %s (%v)", encoded, raw, err)
		}
	}
}

func TestWorkflowOperationValuesSurviveDurableJSONReplay(t *testing.T) {
	assertWorkflowOperationRoundTrip(t, workflowapplication.OpsWorkflowProcessDTO{ID: "process", Status: "completed", WorkflowKey: "flow", DefinitionVersion: 7, RetryCount: 2, CreatedAt: "2026-11-15T00:00:00Z", UpdatedAt: "2026-11-15T00:01:00Z"})
	assertWorkflowOperationRoundTrip(t, workflowmodel.WorkflowRunResult{WorkflowKey: "flow", Status: "completed", Execution: workflowmodel.WorkflowExecution{ExecutionID: "execution", Status: "completed"}})
	assertWorkflowOperationRoundTrip(t, workflowmodel.WorkflowResolveResult{Status: "resolved", Execution: workflowmodel.WorkflowExecution{ExecutionID: "execution", Status: "resolved"}})
	for _, operation := range []operationsapplication.OperationsOwnerExecutionResult{{Value: map[string]any{"id": "process"}}, {Replayed: true, Value: nil}, {Replayed: true, Value: map[string]any{"id": "process", "private_payload": "must-not-leak"}}, {Replayed: true, Value: map[string]any{"retry_count": "invalid"}}} {
		if _, ok := workflowOperationValue[workflowapplication.OpsWorkflowProcessDTO](operation); ok {
			t.Fatal("invalid replay value accepted", operation)
		}
	}
}
