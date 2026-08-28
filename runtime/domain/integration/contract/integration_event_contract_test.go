package integrationcontract

import (
	"testing"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func TestIntegrationEventContractPublishesTypedFiniteBindings(t *testing.T) {
	mapping := integrationmodel.IntegrationEventMappingSchema{
		ActionInput:  map[string]string{"decision": "review.decision"},
		RecordIDPath: "record.id",
		EventFields: []integrationmodel.IntegrationEventFieldSchema{
			{Path: "review.decision", Type: "select", Options: []string{"approved", "rejected"}, Required: true},
			{Path: "record.id", Type: "relation", Required: true},
		},
	}
	bindings, issues := IntegrationEventBindings(mapping)
	if len(issues) != 0 {
		t.Fatalf("contract issues=%#v", issues)
	}
	if fact := bindings["$event.review.decision"]; fact.Type != "text" || len(fact.Values) != 2 {
		t.Fatalf("decision fact=%#v", fact)
	}
	if issues := IntegrationValidateEventPayload(mapping, map[string]any{"review": map[string]any{"decision": "approved"}, "record": map[string]any{"id": "record-1"}}); len(issues) != 0 {
		t.Fatalf("valid payload issues=%#v", issues)
	}
	issues = IntegrationValidateEventPayload(mapping, map[string]any{"review": map[string]any{"decision": "other"}})
	codes := map[string]bool{}
	for _, issue := range issues {
		codes[issue.Code] = true
	}
	if !codes["integration.event_field_value_option_invalid"] || !codes["integration.event_field_required"] {
		t.Fatalf("invalid payload issues=%#v", issues)
	}
}

func TestIntegrationEventContractRejectsUndeclaredDuplicateAndMismatchedPaths(t *testing.T) {
	mapping := integrationmodel.IntegrationEventMappingSchema{
		WorkflowInput: map[string]string{"count": "payload.count", "missing": "payload.missing"},
		RecordIDPath:  "payload.count",
		EventFields: []integrationmodel.IntegrationEventFieldSchema{
			{Path: "payload.count", Type: "integer"},
			{Path: "payload.count", Type: "integer"},
			{Path: "bad..path", Type: "text"},
			{Path: "payload.unknown", Type: "opaque"},
		},
	}
	_, issues := IntegrationEventBindings(mapping)
	codes := map[string]bool{}
	for _, issue := range issues {
		codes[issue.Code] = true
	}
	for _, code := range []string{"integration.event_field_path_duplicate", "integration.event_field_path_invalid", "integration.event_field_type_invalid", "integration.event_path_undeclared", "integration.event_path_type_invalid"} {
		if !codes[code] {
			t.Fatalf("missing %s in %#v", code, issues)
		}
	}
}
