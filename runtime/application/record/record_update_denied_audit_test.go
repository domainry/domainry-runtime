// Record update-denial application audit tests.
package record

import (
	"context"
	"reflect"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestRecordUpdateDeniedAuditPolicy(t *testing.T) {
	var event, objectKey, recordID, summary string
	var metadata map[string]any
	audit := func(_ context.Context, gotEvent, gotObjectKey, gotRecordID string, _ principalmodel.Principal, gotSummary string, _, _ map[string]any, gotMetadata map[string]any) {
		event, objectKey, recordID, summary, metadata = gotEvent, gotObjectKey, gotRecordID, gotSummary, gotMetadata
	}

	recordAppendUpdateDeniedAudit(t.Context(), audit, "employee", "employee-1", principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.field.write_denied"}, " field permission ", map[string]any{"z": 1, " a ": 2, "": 3})

	if event != "record_update_denied" || objectKey != "employee" || recordID != "employee-1" || summary != "Update denied for employee record" {
		t.Fatalf("event=%q object=%q record=%q summary=%q", event, objectKey, recordID, summary)
	}
	if metadata["decision"] != "denied" || metadata["operation"] != "update" || metadata["reason"] != "field permission" || metadata["error_code"] != "backend.field.write_denied" {
		t.Fatalf("metadata = %#v", metadata)
	}
	if keys, ok := metadata["attempted_keys"].([]string); !ok || !reflect.DeepEqual(keys, []string{"a", "z"}) {
		t.Fatalf("attempted keys = %#v", metadata["attempted_keys"])
	}
}

func TestRecordUpdateDeniedAuditUsesInjectedErrorCode(t *testing.T) {
	var metadata map[string]any
	audit := func(_ context.Context, _, _, _ string, _ principalmodel.Principal, _ string, _, _ map[string]any, got map[string]any) {
		metadata = got
	}
	recordAppendUpdateDeniedAudit(t.Context(), audit, "customer", "c1", principalmodel.Principal{}, context.Canceled, "", nil)
	if metadata["error_code"] != "backend.internal" {
		t.Fatalf("metadata = %#v", metadata)
	}
}
