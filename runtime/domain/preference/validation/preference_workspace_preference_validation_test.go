package validation

import (
	"encoding/json"
	"testing"

	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestDecodeWorkspacePreferenceDefinitionAcceptsTypedEffectiveValues(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		valueType string
		value     string
	}{
		{name: "boolean", valueType: "boolean", value: `true`},
		{name: "date", valueType: "date", value: `"2026-07-21"`},
		{name: "datetime", valueType: "datetime", value: `"2026-07-21T09:30:00+08:00"`},
		{name: "decimal", valueType: "decimal", value: `"19.9500"`},
		{name: "integer", valueType: "integer", value: `42`},
		{name: "json", valueType: "json", value: `{"mode":"strict","limits":[1,2]}`},
		{name: "number", valueType: "number", value: `1.25e2`},
		{name: "text", valueType: "text", value: `"enabled"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			payload := json.RawMessage(`{"key":"policy.default","name":"Default policy","value_type":"` + test.valueType + `","value":` + test.value + `,"effective_from":"2026-07-01","effective_to":"2026-08-01"}`)
			definition, err := DecodeWorkspacePreferenceDefinition("policy.default", payload)
			if err != nil {
				t.Fatalf("decode typed preference: %v", err)
			}
			if definition.Key != "policy.default" || definition.ValueType != test.valueType {
				t.Fatalf("unexpected definition: %+v", definition)
			}
		})
	}
}

func TestDecodeWorkspacePreferenceDefinitionRejectsOpenLegacyAndMismatchedContracts(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		key     string
		payload string
		code    string
	}{
		{name: "unknown field", key: "limit", payload: `{"key":"limit","name":"Limit","value_type":"integer","value":1,"effective_from":"2026-07-01","legacy":true}`, code: "backend.preference.definition_invalid"},
		{name: "legacy aliases", key: "theme", payload: `{"preference_key":"theme","label":"Theme","value":"dark","effective_from":"2026-07-01"}`, code: "backend.preference.definition_invalid"},
		{name: "key mismatch", key: "limit", payload: `{"key":"other","name":"Limit","value_type":"integer","value":1,"effective_from":"2026-07-01"}`, code: "backend.preference.definition_invalid"},
		{name: "invalid start", key: "limit", payload: `{"key":"limit","name":"Limit","value_type":"integer","value":1,"effective_from":"tomorrow"}`, code: "backend.preference.effective_from_invalid"},
		{name: "empty interval", key: "limit", payload: `{"key":"limit","name":"Limit","value_type":"integer","value":1,"effective_from":"2026-07-01","effective_to":"2026-07-01"}`, code: "backend.preference.effective_to_invalid"},
		{name: "integer fraction", key: "limit", payload: `{"key":"limit","name":"Limit","value_type":"integer","value":1.5,"effective_from":"2026-07-01"}`, code: "backend.preference.value_type_mismatch"},
		{name: "decimal number loses precision contract", key: "fee", payload: `{"key":"fee","name":"Fee","value_type":"decimal","value":19.95,"effective_from":"2026-07-01"}`, code: "backend.preference.value_type_mismatch"},
		{name: "decimal fraction syntax", key: "fee", payload: `{"key":"fee","name":"Fee","value_type":"decimal","value":"1/3","effective_from":"2026-07-01"}`, code: "backend.preference.value_type_mismatch"},
		{name: "unknown value type", key: "limit", payload: `{"key":"limit","name":"Limit","value_type":"money","value":"1.00","effective_from":"2026-07-01"}`, code: "backend.preference.value_type_invalid"},
		{name: "null value", key: "limit", payload: `{"key":"limit","name":"Limit","value_type":"json","value":null,"effective_from":"2026-07-01"}`, code: "backend.preference.value_invalid"},
		{name: "trailing document", key: "limit", payload: `{"key":"limit","name":"Limit","value_type":"integer","value":1,"effective_from":"2026-07-01"} {}`, code: "backend.preference.definition_invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := DecodeWorkspacePreferenceDefinition(test.key, json.RawMessage(test.payload))
			wrapped := apperror.FromError(apperror.KindBadRequest, err)
			if err == nil || apperror.CodeOf(wrapped) != test.code {
				t.Fatalf("expected %s, got %v", test.code, err)
			}
			if got := apperror.ParamsOf(wrapped)["preference_key"]; got != test.key {
				t.Fatalf("preference_key=%q want=%q", got, test.key)
			}
		})
	}
}

func TestWorkspacePreferenceValidationCoversGenericScalarEdges(t *testing.T) {
	base := `{"key":"policy.default","name":"Default policy","value_type":"text","value":"enabled","effective_from":"2026-07-01"}`
	envelopes := []string{
		`{`,
		base + ` !`,
		replacePreferenceOnce(base, `"key":"policy.default"`, `"key":""`),
		replacePreferenceOnce(base, `"name":"Default policy"`, `"name":""`),
		replacePreferenceOnce(base, `"value_type":"text"`, `"value_type":""`),
		replacePreferenceOnce(base, `"value":"enabled",`, ``),
		replacePreferenceOnce(base, `"value":"enabled"`, `"value":null`),
		replacePreferenceOnce(base, `"effective_from":"2026-07-01"`, `"effective_from":""`),
		replacePreferenceOnce(base, `"effective_from"`, `"effective_to":"invalid","effective_from"`),
	}
	for _, payload := range envelopes {
		if _, err := DecodeWorkspacePreferenceDefinition("", json.RawMessage(payload)); err == nil {
			t.Fatalf("accepted invalid envelope %s", payload)
		}
	}
	if _, err := DecodeWorkspacePreferenceDefinition(" ", json.RawMessage(base)); err != nil {
		t.Fatalf("blank resource key should defer to document key: %v", err)
	}

	invalidValues := []struct {
		valueType string
		raw       string
	}{
		{valueType: "boolean", raw: `"true"`},
		{valueType: "integer", raw: `"1"`},
		{valueType: "integer", raw: `1e2`},
		{valueType: "integer", raw: `999999999999999999999999999999`},
		{valueType: "decimal", raw: `1.2`},
		{valueType: "number", raw: `"1.2"`},
		{valueType: "number", raw: `1e9999`},
		{valueType: "text", raw: `1`},
		{valueType: "date", raw: `1`},
		{valueType: "date", raw: `"2026-7-1"`},
		{valueType: "date", raw: `"2026-99-99"`},
		{valueType: "datetime", raw: `1`},
		{valueType: "datetime", raw: `"2026-07-01"`},
		{valueType: "datetime", raw: `"not-a-datetime"`},
		{valueType: "json", raw: `{`},
		{valueType: "json", raw: `{} {}`},
		{valueType: "json", raw: `{} !`},
	}
	for _, test := range invalidValues {
		if err := validateWorkspacePreferenceValue("policy.default", test.valueType, json.RawMessage(test.raw)); err == nil {
			t.Fatalf("accepted type=%s raw=%s", test.valueType, test.raw)
		}
	}
}

func replacePreferenceOnce(value, old, replacement string) string {
	for index := 0; index+len(old) <= len(value); index++ {
		if value[index:index+len(old)] == old {
			return value[:index] + replacement + value[index+len(old):]
		}
	}
	return value
}
