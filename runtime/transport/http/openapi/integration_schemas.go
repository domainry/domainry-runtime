package openapi

func openAPIIntegrationConnectionSchema(request bool) map[string]any {
	properties := map[string]any{
		"key":          map[string]any{"type": "string"},
		"workspace_id": map[string]any{"type": "string"},
		"connector_key": map[string]any{
			"type": "string",
		},
		"provider_key": map[string]any{
			"type":        "string",
			"description": "Concrete provider selected from the Runtime connector catalog.",
		},
		"name":        map[string]any{"type": "string"},
		"status":      map[string]any{"type": "string"},
		"config":      openAPIObject(nil),
		"secret_refs": openAPIObject(map[string]any{"type": "string"}),
	}
	required := []string{"connector_key", "provider_key"}
	if !request {
		required = append(required, "key", "status")
		properties["created_by"] = map[string]any{"type": "string"}
		properties["created_at"] = map[string]any{"type": "string", "format": "date-time"}
		properties["updated_at"] = map[string]any{"type": "string", "format": "date-time"}
	}
	return map[string]any{"type": "object", "required": required, "properties": properties}
}
