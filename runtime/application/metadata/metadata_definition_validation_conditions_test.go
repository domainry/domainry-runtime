package metadata

import (
	"encoding/json"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
)

func TestMetadataDefinitionRequestPayloadCoversObjectPreferenceRuleSetAndReferenceIssues(t *testing.T) {
	runtime := &upsertMetadataRuntime{snapshot: metadatamodel.ApplicationSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{{Key: "order"}}}}
	service := NewApplicationSchemaService(ApplicationSchemaDependencies{Runtime: runtime, Repository: &upsertMetadataRepository{}})
	if _, issues, err := service.ValidateMetadataDefinitionRequestPayload(t.Context(), "action", "bad", metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{`)}); err != nil || len(issues) != 1 {
		t.Fatalf("malformed action issues=%#v err=%v", issues, err)
	}
	if normalized, issues, err := service.ValidateMetadataDefinitionRequestPayload(t.Context(), "object", "order", metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{"key":"order","name":"Order"}`)}); err != nil || len(issues) != 0 || len(normalized) == 0 {
		t.Fatalf("object normalized=%s issues=%#v err=%v", normalized, issues, err)
	}
	preference := json.RawMessage(`{"key":"policy.limit","name":"Limit","value_type":"integer","value":10,"effective_from":"2026-01-01"}`)
	if normalized, issues, err := service.ValidateMetadataDefinitionRequestPayload(t.Context(), "preference", "policy.limit", metadatamodel.MetadataDefinitionUpsertRequest{Payload: preference}); err != nil || len(issues) != 0 || len(normalized) == 0 {
		t.Fatalf("preference normalized=%s issues=%#v err=%v", normalized, issues, err)
	}
	ruleSet := ruleSetReferencePayload("integer", "boolean")
	if normalized, issues, err := service.ValidateMetadataDefinitionRequestPayload(t.Context(), "rule_set", "policy.limit", metadatamodel.MetadataDefinitionUpsertRequest{Payload: ruleSet}); err != nil || len(issues) != 0 || len(normalized) == 0 {
		t.Fatalf("rule set normalized=%s issues=%#v err=%v", normalized, issues, err)
	}
	if _, _, err := service.ValidateMetadataDefinitionRequestPayload(t.Context(), "rule_set", "policy.limit", metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{`)}); err == nil {
		t.Fatal("invalid rule set accepted")
	}

	action := definitionmodel.ActionSchema{Key: "order.resolve", ObjectKey: "order", Kind: "record_operation", RequiresPermission: "order.update", AuditEvent: "order.resolved"}
	payload := json.RawMessage(`{"key":"order.resolve","object_key":"order","kind":"record_operation","requires_permission":"order.update","audit_event":"order.resolved","config":{"steps":[]}}`)
	if normalized, issues, err := service.ValidateMetadataDefinitionRequestPayload(t.Context(), "action", action.Key, metadatamodel.MetadataDefinitionUpsertRequest{Payload: payload}); err != nil || normalized != nil || len(issues) == 0 {
		t.Fatalf("retired config normalized=%s issues=%#v err=%v", normalized, issues, err)
	}
	if _, err := service.ValidateMetadataDefinitionPayload(t.Context(), "action", metadatamodel.MetadataDefinitionUpsertRequest{Payload: payload}); err == nil {
		t.Fatal("retired action config accepted by mutation validator")
	}
	invalidAction, _ := json.Marshal(definitionmodel.ActionSchema{})
	if _, err := service.ValidateMetadataDefinitionPayload(t.Context(), "action", metadatamodel.MetadataDefinitionUpsertRequest{Payload: invalidAction}); err == nil {
		t.Fatal("invalid action accepted by mutation validator")
	}
}

func TestMetadataDefinitionPayloadCoversPreferenceRuleSetIdentityBindingAndValidationErrors(t *testing.T) {
	binding := profilebindingmodel.Binding{
		ContractVersion: profilebindingmodel.ContractVersion, MinReaderVersion: profilebindingmodel.MinimumReaderVersion,
		ObjectKey: "other", IdentityRelationField: "identity_user", Cardinality: "one_to_one", DefaultVisibility: "when_readable",
		BusinessIdentity: profilebindingmodel.BusinessIdentityBinding{Key: "member", SurfaceKeys: []string{"portal"}},
	}
	runtime := &upsertMetadataRuntime{snapshot: metadatamodel.ApplicationSchemaSnapshot{
		Objects: []definitionmodel.ObjectSchema{
			{Key: "identity_user"},
			{Key: "person"},
			{Key: "other", UX: map[string]any{"kind": "identity_profile_extension"}, Fields: []definitionmodel.FieldSchema{{Key: "identity_user", Type: "relation", Unique: true, Config: map[string]any{"object_key": "identity_user"}}}},
		},
		IdentityProfileExtensions: []profilebindingmodel.Binding{binding},
	}}
	service := NewApplicationSchemaService(ApplicationSchemaDependencies{Runtime: runtime})
	preference := metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{"key":"policy.limit","name":"Limit","value_type":"integer","value":10,"effective_from":"2026-01-01"}`)}
	if normalized, err := service.ValidateMetadataDefinitionPayload(t.Context(), "preference", preference); err != nil || len(normalized.Payload) == 0 {
		t.Fatalf("preference=%s err=%v", normalized.Payload, err)
	}
	if _, err := service.ValidateMetadataDefinitionPayload(t.Context(), "preference", metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{`)}); err == nil {
		t.Fatal("invalid preference accepted")
	}
	rule := metadatamodel.MetadataDefinitionUpsertRequest{Payload: ruleSetReferencePayload("integer", "boolean")}
	if normalized, err := service.ValidateMetadataDefinitionPayload(t.Context(), "rule_set", rule); err != nil || len(normalized.Payload) == 0 {
		t.Fatalf("rule=%s err=%v", normalized.Payload, err)
	}
	if _, err := service.ValidateMetadataDefinitionPayload(t.Context(), "rule_set", metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{`)}); err == nil {
		t.Fatal("invalid rule set accepted")
	}
	if _, err := service.ValidateMetadataDefinitionPayload(t.Context(), "identity_profile_binding", metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{`)}); err == nil {
		t.Fatal("invalid binding accepted")
	}
	if _, err := service.ValidateMetadataDefinitionPayload(t.Context(), "identity_profile_binding", metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{"object_key":"missing"}`)}); err == nil {
		t.Fatal("unknown binding object accepted")
	}
	bindingPayload, _ := json.Marshal(binding)
	if _, err := service.ValidateMetadataDefinitionPayload(t.Context(), "identity_profile_binding", metadatamodel.MetadataDefinitionUpsertRequest{Payload: bindingPayload}); err != nil {
		t.Fatalf("replacement binding: %v", err)
	}
	if _, err := service.ValidateMetadataDefinitionPayload(t.Context(), "unknown", metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{}`)}); err != nil {
		t.Fatalf("unknown passthrough: %v", err)
	}
	if _, err := service.ValidateMetadataDefinitionPayload(t.Context(), "view", metadatamodel.MetadataDefinitionUpsertRequest{Payload: json.RawMessage(`{}`)}); err == nil {
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
