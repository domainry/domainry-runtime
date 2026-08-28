package integration

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"context"
	"errors"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"testing"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestOwnerTaskProjectionMapsPayloadWithoutRecordDependencies(t *testing.T) {
	now := time.Date(2026, 7, 17, 8, 0, 0, 0, time.UTC)
	activity := definitionmodel.ObjectSchema{Key: "activity", Fields: []definitionmodel.FieldSchema{
		{Key: "subject", Type: "text"}, {Key: "owner_id", Type: "text"}, {Key: "department_id", Type: "text"},
		{Key: "activity_type", Type: "select", Validation: definitionmodel.FieldValidation{Options: []string{"email", "other"}}},
		{Key: "status", Type: "select", Config: map[string]any{"value_domain": map[string]any{"items": []any{map[string]any{"key": "open"}, map[string]any{"key": "closed"}}}}},
		{Key: "due_date", Type: "date"}, {Key: "notes", Type: "text"}, {Key: "customer_id", Type: "relation"},
	}}
	payload := map[string]any{"title": " Follow up customer ", "activity_type": "EMAIL", "source": "mail", "target_object": "customer", "target_record_id": "customer-1"}
	related, email := IntegrationOwnerTaskReferenceCandidates(payload)
	if email != "" || related["related_customer"] != "customer-1" {
		t.Fatalf("references=%+v email=%q", related, email)
	}
	data, err := IntegrationBuildOwnerTaskProjection(IntegrationOwnerTaskProjectionRequest{
		Activity: activity, Event: integrationmodel.IntegrationEvent{Provider: "mail", EventType: "message.received", ExternalID: "event-1"},
		Mapping: integrationmodel.IntegrationEventMappingSchema{Key: "mail_owner_task"}, Payload: payload,
		Principal: principalmodel.Principal{Principal: identitysdk.Principal{UserID: "owner-1", DepartmentID: "sales"}}, Related: related, Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	if data["subject"] != "Follow up customer" || data["owner_id"] != "owner-1" || data["department_id"] != "sales" {
		t.Fatalf("identity projection=%+v", data)
	}
	if data["activity_type"] != "email" || data["status"] != "open" || data["due_date"] != "2026-07-18" || data["customer_id"] != "customer-1" {
		t.Fatalf("contract projection=%+v", data)
	}
	if notes, _ := data["notes"].(string); notes == "" || len(notes) > 2000 {
		t.Fatalf("notes=%q", notes)
	}
}

func TestOwnerTaskProjectionRequiresOwner(t *testing.T) {
	_, err := IntegrationBuildOwnerTaskProjection(IntegrationOwnerTaskProjectionRequest{Activity: definitionmodel.ObjectSchema{Key: "activity"}, Payload: map[string]any{}, Principal: principalmodel.Principal{}})
	var appError *apperror.AppError
	if !errors.As(err, &appError) || appError.Code != "backend.integration.event_mapping.owner_task_missing_owner" {
		t.Fatalf("error=%v", err)
	}
}

func TestIntegrationEnrichOwnerTaskReferencesOwnsEmailLookupStrategy(t *testing.T) {
	related := IntegrationEnrichOwnerTaskReferences(t.Context(), map[string]any{"email": "person@example.com"}, func(_ context.Context, objectKey, fieldKey, value string) (recordmodel.Record, bool) {
		if fieldKey != "email" || value != "person@example.com" {
			t.Fatalf("lookup %s.%s=%s", objectKey, fieldKey, value)
		}
		if objectKey == "contact" {
			return recordmodel.Record{ID: "contact-1", Data: map[string]any{"customer": "customer-1"}}, true
		}
		return recordmodel.Record{}, false
	})
	if related["related_contact"] != "contact-1" || related["related_customer"] != "customer-1" {
		t.Fatalf("related=%#v", related)
	}
}

func TestOwnerTaskProjectionHelperEdges(t *testing.T) {
	for _, target := range []string{"customer", "contact", "opportunity", "unknown"} {
		related, _ := IntegrationOwnerTaskReferenceCandidates(map[string]any{"target_object": target, "target_record_id": "record"})
		if target != "unknown" && related["related_"+target] != "record" {
			t.Fatalf("target %q references=%#v", target, related)
		}
	}
	if related, _ := IntegrationOwnerTaskReferenceCandidates(map[string]any{"target_object": "customer"}); related["related_customer"] != "" {
		t.Fatalf("target without id references=%#v", related)
	}
	for _, payload := range []map[string]any{
		{"target_object": "contact", "target_record_id": "target", "related_contact": "explicit"},
		{"target_object": "opportunity", "target_record_id": "target", "related_opportunity": "explicit"},
	} {
		if related, _ := IntegrationOwnerTaskReferenceCandidates(payload); related["related_contact"] == "target" || related["related_opportunity"] == "target" {
			t.Fatalf("explicit target reference overwritten=%#v", related)
		}
	}
	if related := IntegrationEnrichOwnerTaskReferences(t.Context(), map[string]any{}, func(context.Context, string, string, string) (recordmodel.Record, bool) {
		return recordmodel.Record{}, false
	}); len(related) != 3 {
		t.Fatalf("no-email references=%#v", related)
	}
	explicit := IntegrationEnrichOwnerTaskReferences(t.Context(), map[string]any{"email": "person@example.com", "related_contact": "contact", "related_customer": "customer"}, func(context.Context, string, string, string) (recordmodel.Record, bool) {
		return recordmodel.Record{ID: "unexpected"}, true
	})
	if explicit["related_contact"] != "contact" || explicit["related_customer"] != "customer" {
		t.Fatalf("explicit enrichment=%#v", explicit)
	}
	explicitCustomer := IntegrationEnrichOwnerTaskReferences(t.Context(), map[string]any{"email": "person@example.com", "related_customer": "customer"}, func(_ context.Context, objectKey, _, _ string) (recordmodel.Record, bool) {
		if objectKey == "contact" {
			return recordmodel.Record{ID: "contact", Data: map[string]any{"customer": "ignored"}}, true
		}
		return recordmodel.Record{}, false
	})
	if explicitCustomer["related_contact"] != "contact" || explicitCustomer["related_customer"] != "customer" {
		t.Fatalf("explicit customer enrichment=%#v", explicitCustomer)
	}
	related, email := IntegrationOwnerTaskReferenceCandidates(map[string]any{"related_customer": "explicit", "target_object": "customer", "target_record_id": "target", "sender": map[string]any{"email": "person@example.com"}})
	if related["related_customer"] != "explicit" || email != "person@example.com" {
		t.Fatalf("explicit references=%#v email=%q", related, email)
	}
	if related := IntegrationEnrichOwnerTaskReferences(t.Context(), map[string]any{"email": "person@example.com"}, nil); related["related_customer"] != "" {
		t.Fatalf("nil lookup references=%#v", related)
	}
	related = IntegrationEnrichOwnerTaskReferences(t.Context(), map[string]any{"email": "person@example.com"}, func(_ context.Context, objectKey, _, _ string) (recordmodel.Record, bool) {
		if objectKey == "customer" {
			return recordmodel.Record{ID: "customer"}, true
		}
		return recordmodel.Record{}, false
	})
	if related["related_customer"] != "customer" || related["related_contact"] != "" {
		t.Fatalf("customer enrichment=%#v", related)
	}

	activity := definitionmodel.ObjectSchema{Key: "activity", Fields: []definitionmodel.FieldSchema{
		{Key: "owner", Type: "text"}, {Key: "assignee", Type: "text"}, {Key: "department", Type: "text"},
		{Key: "activity_type", Type: "select", Validation: definitionmodel.FieldValidation{Options: []string{"CALL", "note", "other", "custom"}}},
		{Key: "status", Type: "select", Config: map[string]any{"options": []any{"open", map[string]any{"key": "closed"}, 7}}},
		{Key: "due_at", Type: "datetime"}, {Key: "content", Type: "text"}, {Key: "contact", Type: "relation"},
	}}
	projection, err := IntegrationBuildOwnerTaskProjection(IntegrationOwnerTaskProjectionRequest{
		Activity: activity, Event: integrationmodel.IntegrationEvent{Provider: "provider"}, Mapping: integrationmodel.IntegrationEventMappingSchema{Key: "mapping"},
		Payload: map[string]any{"owner": "payload-owner", "department": "payload-department", "activity_type": "call", "due_date": "2026-07-20", "source": "mail", "summary": "summary"},
		Related: map[string]string{"related_contact": "contact", "unknown": "ignored"}, Now: time.Now(),
	})
	if err != nil || projection["owner"] != "payload-owner" || projection["assignee"] != "payload-owner" || projection["department"] != "payload-department" || projection["activity_type"] != "CALL" || projection["status"] != "open" || projection["due_at"] != "2026-07-20T00:00:00Z" || projection["contact"] != "contact" {
		t.Fatalf("projection=%#v err=%v", projection, err)
	}
	if _, err := IntegrationBuildOwnerTaskProjection(IntegrationOwnerTaskProjectionRequest{Activity: definitionmodel.ObjectSchema{}, Principal: principalmodel.Principal{Principal: identitysdk.Principal{UserID: "owner"}}}); apperror.CodeOf(err) != "backend.integration.event_mapping.owner_task_activity_missing" {
		t.Fatalf("missing activity error=%v", err)
	}
	fallback, err := IntegrationBuildOwnerTaskProjection(IntegrationOwnerTaskProjectionRequest{Activity: definitionmodel.ObjectSchema{Key: "activity"}, Event: integrationmodel.IntegrationEvent{Provider: "provider"}, Principal: principalmodel.Principal{Principal: identitysdk.Principal{UserID: "owner"}}})
	if err != nil || fallback["subject"] != "External follow-up: provider" {
		t.Fatalf("fallback projection=%#v err=%v", fallback, err)
	}

	for input, want := range map[string]string{"call": "CALL", "email": "CALL", "meeting": "CALL", "note": "note", "other": "other", "custom": "custom", "": "other"} {
		if got := ownerTaskActivityType(activity, map[string]any{"activity_type": input}); got != want {
			t.Fatalf("activity type %q=%q want=%q", input, got, want)
		}
	}
	if got := ownerTaskDepartmentValue(nil, principalmodel.Principal{Principal: identitysdk.Principal{DepartmentID: "department"}}); got != "department" {
		t.Fatalf("principal department=%q", got)
	}
	if got := ownerTaskDepartmentValue(nil, principalmodel.Principal{}); got != "company" {
		t.Fatalf("default department=%q", got)
	}
	if got := ownerTaskDueValue(map[string]any{"due_date": "not-a-date"}, definitionmodel.FieldSchema{Type: "datetime"}, time.Time{}); got != "not-a-date" {
		t.Fatalf("raw due value=%q", got)
	}
	if got := ownerTaskDueValue(map[string]any{"due_date": "2026-07-20"}, definitionmodel.FieldSchema{Type: "date"}, time.Time{}); got != "2026-07-20" {
		t.Fatalf("date due value=%q", got)
	}
	if got := ownerTaskDueValue(nil, definitionmodel.FieldSchema{Type: "date"}, time.Date(2026, 7, 20, 1, 0, 0, 0, time.UTC)); got != "2026-07-21" {
		t.Fatalf("default date=%q", got)
	}
	if got := ownerTaskDueValue(nil, definitionmodel.FieldSchema{Type: "datetime"}, time.Time{}); got == "" {
		t.Fatal("zero-time datetime missing")
	}

	if got := ownerTaskSelectValue(definitionmodel.ObjectSchema{}, "missing", nil, "fallback"); got != "fallback" {
		t.Fatalf("missing select=%q", got)
	}
	if got := ownerTaskSelectValue(definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{Key: "value"}}}, "value", nil, "fallback"); got != "fallback" {
		t.Fatalf("optionless select=%q", got)
	}
	configField := definitionmodel.FieldSchema{Config: map[string]any{"valueDomain": map[string]any{"items": []any{map[string]any{"value": "one"}, map[string]any{"label": "two"}}}}}
	if options := ownerTaskFieldOptions(configField); len(options) != 2 || options[1] != "two" {
		t.Fatalf("config options=%#v", options)
	}
	if options := ownerTaskFieldOptions(definitionmodel.FieldSchema{Config: map[string]any{"value_domain": map[string]any{"items": "invalid"}}}); options != nil {
		t.Fatalf("invalid value-domain options=%#v", options)
	}
	if options := dictionaryOptionKeys("invalid"); options != nil {
		t.Fatalf("invalid dictionary options=%#v", options)
	}
	if got := firstString(nil, " ", "value"); got != "value" {
		t.Fatalf("first string=%q", got)
	}
	if got := compactStrings([]string{" one ", "", "one", "two"}); len(got) != 2 {
		t.Fatalf("compact strings=%#v", got)
	}
	if keys := ownerTaskFieldKeys(definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{Key: "owner"}}}, "owner", "owner"); len(keys) != 1 {
		t.Fatalf("duplicate owner keys=%#v", keys)
	}
	if notes := ownerTaskNotes(integrationmodel.IntegrationEvent{}, integrationmodel.IntegrationEventMappingSchema{}, map[string]any{}, nil); strings.Contains(notes, "source=") {
		t.Fatalf("source-less notes=%q", notes)
	}
	if got := integrationPayloadPathString(map[string]any{"nested": "text"}, "nested.value"); got != "" {
		t.Fatalf("invalid nested path=%q", got)
	}
	if got := integrationPayloadPathString(map[string]any{}, "missing"); got != "" {
		t.Fatalf("missing path=%q", got)
	}
	if got := integrationPayloadPathString(map[string]any{"nil": nil}, "nil"); got != "" {
		t.Fatalf("nil path=%q", got)
	}
	opportunityActivity := definitionmodel.ObjectSchema{Key: "activity", Fields: []definitionmodel.FieldSchema{{Key: "subject"}, {Key: "owner_id"}, {Key: "opportunity_id"}}}
	opportunityProjection, err := IntegrationBuildOwnerTaskProjection(IntegrationOwnerTaskProjectionRequest{Activity: opportunityActivity, Principal: principalmodel.Principal{Principal: identitysdk.Principal{UserID: "owner"}}, Related: map[string]string{"related_opportunity": "opportunity"}})
	if err != nil || opportunityProjection["opportunity_id"] != "opportunity" {
		t.Fatalf("opportunity projection=%#v err=%v", opportunityProjection, err)
	}
	if got := truncateString("  long value  ", 4); got != "long" {
		t.Fatalf("truncated=%q", got)
	}
	if got := truncateString(" value ", 0); got != "value" {
		t.Fatalf("unlimited=%q", got)
	}
}
