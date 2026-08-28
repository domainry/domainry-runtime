package integration

import (
	"encoding/json"
	"testing"
)

func TestIntegrationCatalogJSONNormalizationRemainingEdges(t *testing.T) {
	for name, decode := range map[string]func(json.RawMessage) (map[string]any, error){
		"public":  decodePublicObject,
		"webhook": decodePublicWebhookObject,
	} {
		t.Run(name+" null with whitespace", func(t *testing.T) {
			value, err := decode(json.RawMessage(" null "))
			if err != nil || len(value) != 0 {
				t.Fatalf("value=%v err=%v", value, err)
			}
		})
	}
	for _, decode := range []func(json.RawMessage) (map[string]any, error){decodePublicObject, decodePublicWebhookObject} {
		value, err := decode(nil)
		if err != nil || len(value) != 0 {
			t.Fatalf("empty value=%v err=%v", value, err)
		}
	}
	if value := normalizePublicJSONNumbers(json.Number("1.5")); value != float64(1.5) {
		t.Fatalf("decimal=%#v", value)
	}
	invalid := json.Number("invalid")
	if value := normalizePublicJSONNumbers(invalid); value != invalid {
		t.Fatalf("invalid=%#v", value)
	}
	if value := normalizePublicJSONNumbers(map[string]any{}).(map[string]any); len(value) != 0 {
		t.Fatalf("empty map=%#v", value)
	}
	value := normalizePublicJSONNumbers(map[string]any{"number": json.Number("2")}).(map[string]any)
	if value["number"] != 2 {
		t.Fatalf("map=%#v", value)
	}
	if value := normalizePublicJSONNumbers([]any{}).([]any); len(value) != 0 {
		t.Fatalf("empty slice=%#v", value)
	}
	values := normalizePublicJSONNumbers([]any{json.Number("3")}).([]any)
	if values[0] != 3 {
		t.Fatalf("slice=%#v", values)
	}
}
