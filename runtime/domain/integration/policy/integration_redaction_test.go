package policy

import "testing"

func TestRedactSensitiveMapMasksIntegrationCredentials(t *testing.T) {
	redacted := RedactSensitiveMap(map[string]any{
		"api_key": "secret",
		"nested":  map[string]any{"access_token": "secret", "status": "ok"},
	})
	if redacted["api_key"] != "[REDACTED]" {
		t.Fatalf("api_key = %#v", redacted["api_key"])
	}
	nested := redacted["nested"].(map[string]any)
	if nested["access_token"] != "[REDACTED]" || nested["status"] != "ok" {
		t.Fatalf("nested = %#v", nested)
	}
}
