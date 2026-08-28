package record

import (
	"context"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
)

func TestRecordApplicationAuditCallbacksCoverDisabledAndEnabledPaths(t *testing.T) {
	service := &RecordApplicationService{}
	object := definitionmodel.ObjectSchema{Key: "customer"}
	record := recordmodel.Record{ID: "customer-1"}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{UserID: "user"}}
	service.auditFieldDenials(t.Context(), object, record, "read", nil, principal)
	service.auditScopeDenial(t.Context(), object, record.ID, principal)
	events := []string{}
	service.audit = func(_ context.Context, event, objectKey, recordID string, _ principalmodel.Principal, _ string, _, _, metadata map[string]any) {
		if objectKey != object.Key || recordID != record.ID || metadata == nil {
			t.Fatalf("event=%s object=%s record=%s metadata=%v", event, objectKey, recordID, metadata)
		}
		events = append(events, event)
	}
	service.auditFieldDenials(t.Context(), object, record, "read", []recordservice.RecordFieldPolicyDecision{{FieldKey: "secret", RuleKey: "mask-secret"}}, principal)
	service.auditScopeDenial(t.Context(), object, record.ID, principal)
	if len(events) != 2 || events[0] != "field_access_denied" || events[1] != "record_scope_access_denied" {
		t.Fatalf("events=%v", events)
	}
}
