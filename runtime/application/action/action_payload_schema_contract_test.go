package action

import (
	"reflect"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	invocationcontract "github.com/domainry/domainry-runtime/runtime/domain/invocation/contract"
)

func TestPublishedPayloadSchemaMatchesCanonicalRuntimeInputsAndDefaults(t *testing.T) {
	two := 2
	action := definitionmodel.ActionSchema{Key: "request.submit", Defaults: map[string]any{"mode": "final"}, PayloadFields: []definitionmodel.ActionPayloadField{
		{Key: "due", Type: "date", Required: true}, {Key: "mode", Type: "select", Required: true, Options: []string{"draft", "final"}, DefaultValue: "draft"},
		{Key: "rows", Type: "object", Repeated: true, Required: true, MaxItems: &two, Fields: []definitionmodel.ActionPayloadField{{Key: "amount", Type: "currency", Required: true}, {Key: "count", Type: "integer", Required: true, DefaultValue: 1}}},
	}}
	schema := invocationcontract.PayloadJSONSchema(action.PayloadFields, action.Defaults)
	props := schema["properties"].(map[string]any)
	if !reflect.DeepEqual(schema["required"], []string{"due", "rows"}) || props["mode"].(map[string]any)["default"] != "final" || props["due"].(map[string]any)["format"] != "date" {
		t.Fatal(schema)
	}
	rowSchema := props["rows"].(map[string]any)["items"].(map[string]any)
	if !reflect.DeepEqual(rowSchema["required"], []string{"amount"}) || rowSchema["properties"].(map[string]any)["amount"].(map[string]any)["type"] != "string" {
		t.Fatal(rowSchema)
	}
	normalized, err := ActionNormalizePayload(action, map[string]any{"due": "2026-09-10", "rows": []any{map[string]any{"amount": "12.34"}}})
	if err != nil || normalized["mode"] != "final" {
		t.Fatal(normalized, err)
	}
	if normalized["rows"].([]any)[0].(map[string]any)["count"] != int64(1) {
		t.Fatal("declared nested default did not match execution", normalized)
	}
	for _, input := range []map[string]any{
		{"due": "2026-09-10T00:00:00Z", "rows": []any{map[string]any{"amount": "12.34"}}},
		{"due": "2026-09-10", "rows": []any{}},
		{"due": "2026-09-10", "rows": []any{map[string]any{"amount": "12.34", "secret": "injected"}}},
		{"due": "2026-09-10", "rows": []any{map[string]any{"amount": "1"}, map[string]any{"amount": "2"}, map[string]any{"amount": "3"}}},
	} {
		if _, err := ActionNormalizePayload(action, input); err == nil {
			t.Fatal("input forbidden by advertised schema accepted", input)
		}
	}
}
