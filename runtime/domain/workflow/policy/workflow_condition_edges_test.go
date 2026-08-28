package policy

import (
	"context"
	"errors"
	"testing"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func TestWorkflowRemainingExecutionConditionCombinations(t *testing.T) {
	if got := WorkflowChangedFieldsFromTrigger(definitionmodel.WorkflowSchema{TriggerContract: &definitionmodel.WorkflowTriggerContract{}}, "record_updated:order."); len(got) != 0 {
		t.Fatalf("blank changed fields = %#v", got)
	}
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	if WorkflowExecutionDue(workflowmodel.WorkflowExecution{Status: "failed", MaxAttempts: 2, Attempt: 1}, now) {
		t.Fatal("failed execution without retry time is due")
	}
	execution := workflowmodel.WorkflowExecution{MaxAttempts: 2, Attempt: 1}
	WorkflowMarkFailed(&execution, definitionmodel.WorkflowSchema{}, errors.New("failed"), now)
	if execution.Status != "failed" || execution.NextRunAt == "" {
		t.Fatalf("scheduled failed execution = %#v", execution)
	}
}

func TestWorkflowRemainingRecordConditionCombinations(t *testing.T) {
	if !structuredWorkflowConditionMatches(map[string]any{"field": ""}, nil) {
		t.Fatal("blank structured condition did not match")
	}
	if _, ok := daysAgoCutoff(context.Background(), "-1"); ok {
		t.Fatal("negative days ago accepted")
	}
	if _, ok := daysAheadCutoff(context.Background(), "bad"); ok {
		t.Fatal("invalid days ahead accepted")
	}
	for _, test := range []struct {
		value any
		want  bool
		ok    bool
	}{{"true", true, true}, {"1", true, true}, {"yes", true, true}, {"false", false, true}, {"0", false, true}, {"no", false, true}} {
		got, ok := boolAny(test.value)
		if got != test.want || ok != test.ok {
			t.Fatalf("bool %#v = %v/%v", test.value, got, ok)
		}
	}
}
