package policy

import (
	"regexp"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func TestWorkflowExecutionPolicy(t *testing.T) {
	workflow := definitionmodel.WorkflowSchema{
		RunAs: " operator ", Retry: &definitionmodel.WorkflowRetryPolicy{MaxAttempts: 5},
		IdempotencyKeys: []string{"record_id", "event"},
	}
	if WorkflowRunAs(workflow) != "operator" || WorkflowMaxAttempts(workflow) != 5 {
		t.Fatal("workflow execution policy did not normalize run-as or retry attempts")
	}
	key := WorkflowIdempotencyKey(workflow, map[string]any{"record_id": "customer-1", "event": "approved"})
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(key) {
		t.Fatalf("idempotency key=%q", key)
	}
	if reordered := WorkflowIdempotencyKey(definitionmodel.WorkflowSchema{Key: workflow.Key, IdempotencyKeys: []string{"event", "record_id"}}, map[string]any{"event": "approved", "record_id": "customer-1"}); reordered != key {
		t.Fatalf("canonical projection changed with key order: %q != %q", reordered, key)
	}
	if !WorkflowExecutionRetryScheduled(workflowmodel.WorkflowExecution{Status: "failed", Attempt: 1, MaxAttempts: 3, NextRunAt: "2026-07-18T10:00:00Z"}) {
		t.Fatal("retry should be scheduled before max attempts")
	}
	if WorkflowExecutionRetryScheduled(workflowmodel.WorkflowExecution{Status: "failed", Attempt: 3, MaxAttempts: 3, NextRunAt: "2026-07-18T10:00:00Z"}) {
		t.Fatal("retry must stop at max attempts")
	}
}
