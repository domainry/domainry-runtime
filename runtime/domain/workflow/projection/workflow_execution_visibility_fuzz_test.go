package projection

import (
	"reflect"
	"strings"
	"testing"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func FuzzWorkflowExecutionRecordEvidenceInvariant(f *testing.F) {
	f.Add("order", "record-1")
	f.Add(" spaced ", " id ")
	f.Fuzz(func(t *testing.T, objectKey, recordID string) {
		objectKey, recordID = strings.TrimSpace(objectKey), strings.TrimSpace(recordID)
		execution := workflowmodel.WorkflowExecution{ObjectKey: objectKey, RecordID: recordID, Payload: map[string]any{"object_key": objectKey, "record_id": recordID}}
		ids := WorkflowExecutionRecordIDsForObject(execution, objectKey)
		if recordID == "" {
			if len(ids) != 0 {
				t.Fatalf("incomplete evidence produced IDs: %#v", ids)
			}
			return
		}
		if !WorkflowExecutionMatchesRecordFilter(execution, objectKey, recordID) || !reflect.DeepEqual(ids, []string{recordID}) {
			t.Fatalf("direct and payload evidence disagree: match=%v ids=%#v", WorkflowExecutionMatchesRecordFilter(execution, objectKey, recordID), ids)
		}
	})
}
