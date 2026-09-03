package appschema

import (
	"encoding/json"
	"testing"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
)

func TestProfileBindingValidationMaterializesBackendProtocolDefaults(t *testing.T) {
	runtime := localizedLifecycleRuntimeStub{snapshot: appschemamodel.ApplicationSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{{
		Key: "staff_profile", UX: map[string]any{"kind": "identity_profile_extension"},
		Fields: []definitionmodel.FieldSchema{{
			Key: "identity_user", Type: "relation", Unique: true, Config: map[string]any{"object_key": "identity_user"},
		}},
	}}}}
	service := NewApplicationSchemaApplicationService(ApplicationSchemaDependencies{Runtime: runtime})
	payload := json.RawMessage(`{"object_key":"staff_profile","identity_relation_field":"identity_user","business_identity":{"key":"staff"},"default_visibility":"when_readable"}`)
	normalized, err := service.ValidateApplicationDefinitionPayload(t.Context(), "identity_profile_binding", appschemamodel.ApplicationDefinitionUpsertRequest{Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	var binding profilebindingmodel.Binding
	if err := json.Unmarshal(normalized.Payload, &binding); err != nil {
		t.Fatal(err)
	}
	if binding.ContractVersion != profilebindingmodel.ContractVersion || binding.MinReaderVersion != profilebindingmodel.MinimumReaderVersion || binding.Cardinality != "one_to_one" {
		t.Fatalf("backend defaults were not materialized: %#v", binding)
	}
}

func TestFieldValidationMaterializesRouteOwnedKeyAndRejectsMismatch(t *testing.T) {
	runtime := localizedLifecycleRuntimeStub{snapshot: appschemamodel.ApplicationSchemaSnapshot{Objects: []definitionmodel.ObjectSchema{{Key: "order", Fields: []definitionmodel.FieldSchema{}}}}}
	service := NewApplicationSchemaApplicationService(ApplicationSchemaDependencies{Runtime: runtime})
	request := appschemamodel.ApplicationDefinitionUpsertRequest{ObjectKey: "order", Payload: json.RawMessage(`{"name":"Status","type":"text"}`)}
	normalized, issues, err := service.ValidateApplicationDefinitionRequestPayload(t.Context(), "field", "order.status", request)
	if err != nil || len(issues) != 0 {
		t.Fatalf("normalized=%s issues=%v err=%v", normalized, issues, err)
	}
	var field definitionmodel.FieldSchema
	if json.Unmarshal(normalized, &field) != nil || field.Key != "status" {
		t.Fatalf("route key was not materialized: %s", normalized)
	}
	request.Payload = json.RawMessage(`{"key":"other","name":"Status","type":"text"}`)
	if _, _, err := service.ValidateApplicationDefinitionRequestPayload(t.Context(), "field", "order.status", request); err == nil {
		t.Fatal("mismatched compatibility key was accepted")
	}
}
