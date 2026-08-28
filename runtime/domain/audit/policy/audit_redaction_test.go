package policy

import "testing"

func TestRedactSensitiveMapMasksNestedValuesWithoutMutatingInput(t *testing.T) {
	input := map[string]any{
		"safe":    "visible",
		"api_key": "secret",
		"nested":  map[string]any{"access-token": "secret", "value": "visible"},
		"items":   []any{map[string]any{"password": "secret"}},
	}

	redacted := RedactSensitiveMap(input)
	if redacted["api_key"] != "[REDACTED]" {
		t.Fatalf("api_key = %#v", redacted["api_key"])
	}
	nested := redacted["nested"].(map[string]any)
	if nested["access-token"] != "[REDACTED]" || nested["value"] != "visible" {
		t.Fatalf("nested = %#v", nested)
	}
	items := redacted["items"].([]any)
	if items[0].(map[string]any)["password"] != "[REDACTED]" {
		t.Fatalf("items = %#v", items)
	}
	if input["api_key"] != "secret" {
		t.Fatalf("input mutated: %#v", input)
	}
}

func TestIsSensitiveKeyMatrix(t *testing.T) {
	for _, key := range []string{"password", "api_key", "access-token", "clientSecret", "authorization"} {
		if !IsSensitiveKey(key) {
			t.Fatalf("%q should be sensitive", key)
		}
	}
	for _, key := range []string{"", "name", "status", "monkey", "item_count"} {
		if IsSensitiveKey(key) {
			t.Fatalf("%q should not be sensitive", key)
		}
	}
}
