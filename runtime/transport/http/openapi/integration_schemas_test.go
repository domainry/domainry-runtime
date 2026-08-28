package openapi

import "testing"

func TestIntegrationConnectionOpenAPIRequiresConcreteProvider(t *testing.T) {
	schema := openAPIIntegrationConnectionSchema(true)
	required, ok := schema["required"].([]string)
	if !ok {
		t.Fatalf("integration connection required fields=%#v", schema["required"])
	}
	seen := map[string]bool{}
	for _, field := range required {
		seen[field] = true
	}
	if !seen["connector_key"] || !seen["provider_key"] {
		t.Fatalf("integration connection schema must require connector_key and provider_key: %#v", required)
	}
}
