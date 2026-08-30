package appschema

import (
	"encoding/json"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"testing"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestApplicationDefinitionRequestPayloadCoversObjectPreferenceRuleSetAndReferenceIssues(t *testing.T) {
	runtime := &upsertMetadataRuntime{snapshot: appschemamodel.ApplicationSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{{Key: "order"}}}}
	service := NewApplicationSchemaApplicationService(ApplicationSchemaDependencies{Runtime: runtime, Repository: &upsertMetadataRepository{}})
	if _, issues, err := service.ValidateApplicationDefinitionRequestPayload(t.Context(), "action", "bad", appschemamodel.ApplicationDefinitionUpsertRequest{Payload: json.RawMessage(`{`)}); err != nil || len(issues) != 1 {
		t.Fatalf("malformed action issues=%#v err=%v", issues, err)
	}
	if normalized, issues, err := service.ValidateApplicationDefinitionRequestPayload(t.Context(), "object", "order", appschemamodel.ApplicationDefinitionUpsertRequest{Payload: json.RawMessage(`{"key":"order","name":"Order"}`)}); err != nil || len(issues) != 0 || len(normalized) == 0 {
		t.Fatalf("object normalized=%s issues=%#v err=%v", normalized, issues, err)
	}
	preference := json.RawMessage(`{"key":"policy.limit","name":"Limit","value_type":"integer","value":10,"effective_from":"2026-01-01"}`)
	if normalized, issues, err := service.ValidateApplicationDefinitionRequestPayload(t.Context(), "preference", "policy.limit", appschemamodel.ApplicationDefinitionUpsertRequest{Payload: preference}); err != nil || len(issues) != 0 || len(normalized) == 0 {
		t.Fatalf("preference normalized=%s issues=%#v err=%v", normalized, issues, err)
	}
	ruleSet := ruleSetReferencePayload("integer", "boolean")
	if normalized, issues, err := service.ValidateApplicationDefinitionRequestPayload(t.Context(), "rule_set", "policy.limit", appschemamodel.ApplicationDefinitionUpsertRequest{Payload: ruleSet}); err != nil || len(issues) != 0 || len(normalized) == 0 {
		t.Fatalf("rule set normalized=%s issues=%#v err=%v", normalized, issues, err)
	}
	if _, _, err := service.ValidateApplicationDefinitionRequestPayload(t.Context(), "rule_set", "policy.limit", appschemamodel.ApplicationDefinitionUpsertRequest{Payload: json.RawMessage(`{`)}); err == nil {
		t.Fatal("invalid rule set accepted")
	}

	action := definitionmodel.ActionSchema{Key: "order.resolve", ObjectKey: "order", Kind: "record_operation", RequiresPermission: "order.update", AuditEvent: "order.resolved"}
	payload := json.RawMessage(`{"key":"order.resolve","object_key":"order","kind":"record_operation","requires_permission":"order.update","audit_event":"order.resolved","config":{"steps":[]}}`)
	if normalized, issues, err := service.ValidateApplicationDefinitionRequestPayload(t.Context(), "action", action.Key, appschemamodel.ApplicationDefinitionUpsertRequest{Payload: payload}); err != nil || normalized != nil || len(issues) == 0 {
		t.Fatalf("retired config normalized=%s issues=%#v err=%v", normalized, issues, err)
	}
	if _, err := service.ValidateApplicationDefinitionPayload(t.Context(), "action", appschemamodel.ApplicationDefinitionUpsertRequest{Payload: payload}); err == nil {
		t.Fatal("retired action config accepted by mutation validator")
	}
	invalidAction, _ := json.Marshal(definitionmodel.ActionSchema{})
	if _, err := service.ValidateApplicationDefinitionPayload(t.Context(), "action", appschemamodel.ApplicationDefinitionUpsertRequest{Payload: invalidAction}); err == nil {
		t.Fatal("invalid action accepted by mutation validator")
	}
}

func TestApplicationDefinitionPayloadCoversPreferenceRuleSetIdentityBindingAndValidationErrors(t *testing.T) {
	binding := profilebindingmodel.Binding{
		ContractVersion: profilebindingmodel.ContractVersion, MinReaderVersion: profilebindingmodel.MinimumReaderVersion,
		ObjectKey: "other", IdentityRelationField: "identity_user", Cardinality: "one_to_one", DefaultVisibility: "when_readable",
		BusinessIdentity: profilebindingmodel.BusinessIdentityBinding{Key: "member", SurfaceKeys: []string{"portal"}},
	}
	runtime := &upsertMetadataRuntime{snapshot: appschemamodel.ApplicationSchemaSnapshot{
		Objects: []definitionmodel.ObjectSchema{
			{Key: "identity_user"},
			{Key: "person"},
			{Key: "other", UX: map[string]any{"kind": "identity_profile_extension"}, Fields: []definitionmodel.FieldSchema{{Key: "identity_user", Type: "relation", Unique: true, Config: map[string]any{"object_key": "identity_user"}}}},
		},
		IdentityProfileExtensions: []profilebindingmodel.Binding{binding},
	}}
	service := NewApplicationSchemaApplicationService(ApplicationSchemaDependencies{Runtime: runtime})
	preference := appschemamodel.ApplicationDefinitionUpsertRequest{Payload: json.RawMessage(`{"key":"policy.limit","name":"Limit","value_type":"integer","value":10,"effective_from":"2026-01-01"}`)}
	if normalized, err := service.ValidateApplicationDefinitionPayload(t.Context(), "preference", preference); err != nil || len(normalized.Payload) == 0 {
		t.Fatalf("preference=%s err=%v", normalized.Payload, err)
	}
	if _, err := service.ValidateApplicationDefinitionPayload(t.Context(), "preference", appschemamodel.ApplicationDefinitionUpsertRequest{Payload: json.RawMessage(`{`)}); err == nil {
		t.Fatal("invalid preference accepted")
	}
	rule := appschemamodel.ApplicationDefinitionUpsertRequest{Payload: ruleSetReferencePayload("integer", "boolean")}
	if normalized, err := service.ValidateApplicationDefinitionPayload(t.Context(), "rule_set", rule); err != nil || len(normalized.Payload) == 0 {
		t.Fatalf("rule=%s err=%v", normalized.Payload, err)
	}
	if _, err := service.ValidateApplicationDefinitionPayload(t.Context(), "rule_set", appschemamodel.ApplicationDefinitionUpsertRequest{Payload: json.RawMessage(`{`)}); err == nil {
		t.Fatal("invalid rule set accepted")
	}
	if _, err := service.ValidateApplicationDefinitionPayload(t.Context(), "identity_profile_binding", appschemamodel.ApplicationDefinitionUpsertRequest{Payload: json.RawMessage(`{`)}); err == nil {
		t.Fatal("invalid binding accepted")
	}
	if _, err := service.ValidateApplicationDefinitionPayload(t.Context(), "identity_profile_binding", appschemamodel.ApplicationDefinitionUpsertRequest{Payload: json.RawMessage(`{"object_key":"missing"}`)}); err == nil {
		t.Fatal("unknown binding object accepted")
	}
	bindingPayload, _ := json.Marshal(binding)
	if _, err := service.ValidateApplicationDefinitionPayload(t.Context(), "identity_profile_binding", appschemamodel.ApplicationDefinitionUpsertRequest{Payload: bindingPayload}); err != nil {
		t.Fatalf("replacement binding: %v", err)
	}
	if _, err := service.ValidateApplicationDefinitionPayload(t.Context(), "unknown", appschemamodel.ApplicationDefinitionUpsertRequest{Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatalf("unknown passthrough: %v", err)
	}
	if _, err := service.ValidateApplicationDefinitionPayload(t.Context(), "view", appschemamodel.ApplicationDefinitionUpsertRequest{Payload: json.RawMessage(`{}`)}); err == nil {
		t.Fatal("invalid view accepted")
	}
}

func TestCandidateReplaceCoversReplacementAndAppend(t *testing.T) {
	keyOf := func(value string) string { return value }
	if got := candidateReplace([]string{"one", "one", "two"}, "one", "new", keyOf); len(got) != 2 || got[0] != "new" {
		t.Fatalf("replaced=%#v", got)
	}
	if got := candidateReplace([]string{"one"}, "two", "new", keyOf); len(got) != 2 {
		t.Fatalf("appended=%#v", got)
	}
}
