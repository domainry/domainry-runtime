package projection

import (
	"reflect"
	"testing"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func TestWorkflowExecutionMatchesRecordFilterAcrossAllEvidenceLocations(t *testing.T) {
	tests := []struct {
		name      string
		execution workflowmodel.WorkflowExecution
		objectKey string
		recordID  string
		want      bool
	}{
		{name: "direct", execution: workflowmodel.WorkflowExecution{ObjectKey: "order", RecordID: "1"}, objectKey: "order", recordID: "1", want: true},
		{name: "payload", execution: workflowmodel.WorkflowExecution{Payload: map[string]any{"object_key": " order ", "record_id": 2}}, objectKey: "order", recordID: "2", want: true},
		{name: "trigger result", execution: workflowmodel.WorkflowExecution{Result: map[string]any{"trigger_object_key": "order", "trigger_record_id": "3"}}, objectKey: "order", recordID: "3", want: true},
		{name: "created result", execution: workflowmodel.WorkflowExecution{Result: map[string]any{"created_object_key": "invoice", "created_record_id": "4"}}, objectKey: "invoice", recordID: "4", want: true},
		{name: "updated result", execution: workflowmodel.WorkflowExecution{Result: map[string]any{"updated_object_key": "order", "updated_record_id": "5"}}, objectKey: "order", recordID: "5", want: true},
		{name: "object only", execution: workflowmodel.WorkflowExecution{ObjectKey: "order", RecordID: "1"}, objectKey: "order", want: true},
		{name: "record only", execution: workflowmodel.WorkflowExecution{ObjectKey: "order", RecordID: "1"}, recordID: "1", want: true},
		{name: "mismatch", execution: workflowmodel.WorkflowExecution{ObjectKey: "order", RecordID: "1"}, objectKey: "invoice", recordID: "1", want: false},
		{name: "empty", execution: workflowmodel.WorkflowExecution{}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := WorkflowExecutionMatchesRecordFilter(test.execution, test.objectKey, test.recordID); got != test.want {
				t.Fatalf("match = %v, want %v", got, test.want)
			}
		})
	}
}

func TestWorkflowExecutionRecordIDsForObjectDeduplicatesInEvidenceOrder(t *testing.T) {
	execution := workflowmodel.WorkflowExecution{
		ObjectKey: "order", RecordID: " 1 ",
		Payload: map[string]any{"object_key": "order", "record_id": "1"},
		Result: map[string]any{
			"trigger_object_key": "order", "trigger_record_id": 2,
			"created_object_key": "invoice", "created_record_id": "ignored",
			"updated_object_key": "order", "updated_record_id": " 3 ",
		},
	}
	if got, want := WorkflowExecutionRecordIDsForObject(execution, "order"), []string{"1", "2", "3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("record ids = %#v, want %#v", got, want)
	}
	if got := WorkflowExecutionRecordIDsForObject(execution, "missing"); len(got) != 0 || got == nil {
		t.Fatalf("missing object ids = %#v, want non-nil empty", got)
	}
	withBlankAndMismatch := workflowmodel.WorkflowExecution{ObjectKey: "order", RecordID: "", Payload: map[string]any{"object_key": "other", "record_id": "id"}}
	if got := WorkflowExecutionRecordIDsForObject(withBlankAndMismatch, "order"); len(got) != 0 {
		t.Fatalf("blank/mismatched IDs = %#v", got)
	}
}

func TestWorkflowMapStringValueHandlesNilAndWhitespace(t *testing.T) {
	if got := workflowMapStringValue(nil, "id"); got != "" {
		t.Fatalf("nil map value = %q", got)
	}
	if got := workflowMapStringValue(map[string]any{"id": nil}, "id"); got != "" {
		t.Fatalf("nil value = %q", got)
	}
	if got := workflowMapStringValue(map[string]any{"id": " 42 "}, "id"); got != "42" {
		t.Fatalf("trimmed value = %q", got)
	}
}

func TestWorkflowExecutionObjectRecordMatchEdges(t *testing.T) {
	for _, tc := range []struct {
		candidateObject, candidateRecord, object, record string
		want                                             bool
	}{
		{"order", "1", "order", "2", false},
		{"order", "1", "order", "1", true},
		{"", "1", "", "1", true},
		{"", "", "", "", false},
	} {
		if got := workflowExecutionObjectRecordMatch(tc.candidateObject, tc.candidateRecord, tc.object, tc.record); got != tc.want {
			t.Errorf("match %#v = %v, want %v", tc, got, tc.want)
		}
	}
}
