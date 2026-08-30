package validation

import (
	"encoding/json"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
)

func TestMetadataValidateObjectDefinitionOwnsSmallObjectShell(t *testing.T) {
	normalized, err := ApplicationSchemaValidateObjectDefinition("order", json.RawMessage(`{"key":" order ","name":" Order "}`))
	if err != nil || string(normalized) != `{"key":"order","name":"Order","description":"","fields":[]}` {
		t.Fatalf("normalized=%s err=%v", normalized, err)
	}
	for _, test := range []struct {
		name    string
		payload string
		code    string
	}{
		{name: "invalid json", payload: `{`, code: "backend.metadata.object_definition_invalid"},
		{name: "key mismatch", payload: `{"key":"invoice","name":"Invoice"}`, code: "backend.metadata.object_key_mismatch"},
		{name: "name required", payload: `{"key":"order"}`, code: "backend.metadata.object_name_required"},
		{name: "nested field rejected", payload: `{"key":"order","name":"Order","fields":[{"key":"total"}]}`, code: "backend.metadata.object_shell_only"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := ApplicationSchemaValidateObjectDefinition("order", json.RawMessage(test.payload))
			if got := apperror.CodeOf(err); got != test.code {
				t.Fatalf("code=%q want=%q err=%v", got, test.code, err)
			}
		})
	}
}

func TestMetadataValidateObjectDefinitionPreservesProfileUX(t *testing.T) {
	normalized, err := ApplicationSchemaValidateObjectDefinition("technician_profile", json.RawMessage(`{"key":"technician_profile","name":"Technician profile","ux":{"kind":"identity_profile_extension","config":{"identity_relation_field":"identity_user"},"display":{"title_field":"certificate_no"}}}`))
	if err != nil || string(normalized) != `{"key":"technician_profile","name":"Technician profile","description":"","fields":[],"ux":{"config":{"identity_relation_field":"identity_user"},"display":{"title_field":"certificate_no"},"kind":"identity_profile_extension"}}` {
		t.Fatalf("normalized=%s err=%v", normalized, err)
	}
}

func TestMetadataValidateObjectDefinitionNormalizesLifecyclePolicy(t *testing.T) {
	normalized, err := ApplicationSchemaValidateObjectDefinition("ledger_entry", json.RawMessage(`{"key":"ledger_entry","name":"Ledger entry","lifecycle_policy":{"mode":" immutable_after_state ","state_field":" status ","immutable_states":[" confirmed ","reversed"]}}`))
	if err != nil || string(normalized) != `{"key":"ledger_entry","name":"Ledger entry","description":"","fields":[],"lifecycle_policy":{"mode":"immutable_after_state","state_field":"status","immutable_states":["confirmed","reversed"]}}` {
		t.Fatalf("normalized=%s err=%v", normalized, err)
	}
	for _, payload := range []string{
		`{"key":"ledger_entry","name":"Ledger entry","lifecycle_policy":{"mode":"unknown"}}`,
		`{"key":"ledger_entry","name":"Ledger entry","lifecycle_policy":{"mode":"append_only","state_field":"status"}}`,
		`{"key":"ledger_entry","name":"Ledger entry","lifecycle_policy":{"mode":"immutable_after_state","state_field":"status"}}`,
		`{"key":"ledger_entry","name":"Ledger entry","lifecycle_policy":{"mode":"immutable_after_state","state_field":"status","immutable_states":["confirmed"," confirmed "]}}`,
	} {
		_, err := ApplicationSchemaValidateObjectDefinition("ledger_entry", json.RawMessage(payload))
		if got := apperror.CodeOf(err); got != "backend.metadata.object_lifecycle_policy_invalid" {
			t.Fatalf("payload=%s code=%q err=%v", payload, got, err)
		}
	}
}

func TestMetadataValidateObjectDefinitionNormalizesLedgerPolicy(t *testing.T) {
	normalized, err := ApplicationSchemaValidateObjectDefinition("financial_entry", json.RawMessage(`{"key":"financial_entry","name":"Financial entry","lifecycle_policy":{"mode":"append_only"},"ledger_policy":{"integrity":" sha256_chain ","signature":" hmac_sha256 "}}`))
	if err != nil || string(normalized) != `{"key":"financial_entry","name":"Financial entry","description":"","fields":[],"lifecycle_policy":{"mode":"append_only"},"ledger_policy":{"integrity":"sha256_chain","signature":"hmac_sha256"}}` {
		t.Fatalf("normalized=%s err=%v", normalized, err)
	}
	for _, payload := range []string{
		`{"key":"financial_entry","name":"Financial entry","ledger_policy":{"integrity":"sha256_chain"}}`,
		`{"key":"financial_entry","name":"Financial entry","lifecycle_policy":{"mode":"mutable"},"ledger_policy":{"integrity":"sha256_chain"}}`,
		`{"key":"financial_entry","name":"Financial entry","lifecycle_policy":{"mode":"append_only"},"ledger_policy":{"integrity":"md5"}}`,
	} {
		_, err := ApplicationSchemaValidateObjectDefinition("financial_entry", json.RawMessage(payload))
		if apperror.CodeOf(err) != "backend.metadata.object_ledger_policy_invalid" {
			t.Fatalf("payload=%s err=%v", payload, err)
		}
	}
}

func TestMetadataValidateObjectDefinitionNormalizesExportAssurancePolicy(t *testing.T) {
	normalized, err := ApplicationSchemaValidateObjectDefinition("customer", json.RawMessage(`{"key":"customer","name":"Customer","export_assurance_policy":{"required_methods":[" otp "," recent_reauth "],"recent_reauth_max_age_seconds":300}}`))
	if err != nil || string(normalized) != `{"key":"customer","name":"Customer","description":"","fields":[],"export_assurance_policy":{"required_methods":["otp","recent_reauth"],"recent_reauth_max_age_seconds":300}}` {
		t.Fatalf("normalized=%s err=%v", normalized, err)
	}
	for _, payload := range []string{
		`{"key":"customer","name":"Customer","export_assurance_policy":{"required_methods":[]}}`,
		`{"key":"customer","name":"Customer","export_assurance_policy":{"required_methods":["otp","otp"]}}`,
		`{"key":"customer","name":"Customer","export_assurance_policy":{"required_methods":["recent_reauth"],"recent_reauth_max_age_seconds":0}}`,
		`{"key":"customer","name":"Customer","export_assurance_policy":{"required_methods":["otp"],"maker_field":"created_by"}}`,
	} {
		if _, err := ApplicationSchemaValidateObjectDefinition("customer", json.RawMessage(payload)); apperror.CodeOf(err) != "backend.metadata.object_export_assurance_policy_invalid" {
			t.Fatalf("payload=%s err=%v", payload, err)
		}
	}
}

func TestMetadataValidateObjectDefinitionRemainingConditions(t *testing.T) {
	tests := []struct {
		name      string
		resource  string
		payload   string
		errorCode string
	}{
		{name: "empty key", resource: "", payload: `{"key":"","name":"Object"}`, errorCode: "backend.metadata.object_key_mismatch"},
		{name: "validations", resource: "object", payload: `{"key":"object","name":"Object","validations":[{"expression":"true"}]}`, errorCode: "backend.metadata.object_shell_only"},
		{name: "empty state", resource: "object", payload: `{"key":"object","name":"Object","lifecycle_policy":{"mode":"immutable_after_state","state_field":"status","immutable_states":[" "]}}`, errorCode: "backend.metadata.object_lifecycle_policy_invalid"},
		{name: "mutable states", resource: "object", payload: `{"key":"object","name":"Object","lifecycle_policy":{"mode":"mutable","immutable_states":["closed"]}}`, errorCode: "backend.metadata.object_lifecycle_policy_invalid"},
		{name: "immutable state field", resource: "object", payload: `{"key":"object","name":"Object","lifecycle_policy":{"mode":"immutable_after_state","immutable_states":["closed"]}}`, errorCode: "backend.metadata.object_lifecycle_policy_invalid"},
		{name: "ledger signature", resource: "object", payload: `{"key":"object","name":"Object","lifecycle_policy":{"mode":"append_only"},"ledger_policy":{"integrity":"sha256_chain","signature":"rsa"}}`, errorCode: "backend.metadata.object_ledger_policy_invalid"},
		{name: "unknown assurance", resource: "object", payload: `{"key":"object","name":"Object","export_assurance_policy":{"required_methods":["unknown"]}}`, errorCode: "backend.metadata.object_export_assurance_policy_invalid"},
		{name: "reauth max", resource: "object", payload: `{"key":"object","name":"Object","export_assurance_policy":{"required_methods":["recent_reauth"],"recent_reauth_max_age_seconds":86401}}`, errorCode: "backend.metadata.object_export_assurance_policy_invalid"},
		{name: "age without reauth", resource: "object", payload: `{"key":"object","name":"Object","export_assurance_policy":{"required_methods":["otp"],"recent_reauth_max_age_seconds":1}}`, errorCode: "backend.metadata.object_export_assurance_policy_invalid"},
		{name: "approval version", resource: "object", payload: `{"key":"object","name":"Object","export_assurance_policy":{"required_methods":["otp"],"approval_version_field":"version"}}`, errorCode: "backend.metadata.object_export_assurance_policy_invalid"},
		{name: "approval hash", resource: "object", payload: `{"key":"object","name":"Object","export_assurance_policy":{"required_methods":["otp"],"approval_hash_field":"hash"}}`, errorCode: "backend.metadata.object_export_assurance_policy_invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			normalized, err := ApplicationSchemaValidateObjectDefinition(test.resource, json.RawMessage(test.payload))
			if got := apperror.CodeOf(err); got != test.errorCode {
				t.Fatalf("code=%q want=%q normalized=%s err=%v", got, test.errorCode, normalized, err)
			}
		})
	}
}
