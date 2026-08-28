package openapi

func addBusinessAuditOpenAPIPath(paths map[string]any) {
	args := []any{openAPIAdminSecurity()}
	for _, parameter := range []struct {
		name        string
		description string
		schema      map[string]any
	}{
		{name: "object_key", description: "Optional business object key", schema: map[string]any{"type": "string"}},
		{name: "record_id", description: "Optional business record identity; complete object and record targets are data-scope checked", schema: map[string]any{"type": "string"}},
		{name: "event", description: "Exact audit event key", schema: map[string]any{"type": "string"}},
		{name: "actor_id", description: "Exact actor identity; untargeted business queries remain current-actor scoped", schema: map[string]any{"type": "string"}},
		{name: "role_key", description: "Exact event-time role key", schema: map[string]any{"type": "string"}},
		{name: "request_id", description: "Exact request identity stored in audit metadata", schema: map[string]any{"type": "string"}},
		{name: "created_from", description: "Inclusive RFC3339 lower time bound subject to Runtime retention", schema: map[string]any{"type": "string"}},
		{name: "created_to", description: "Inclusive RFC3339 upper time bound", schema: map[string]any{"type": "string"}},
		{name: "page_size", description: "Requested server page size; maximum 200", schema: map[string]any{"type": "integer", "minimum": 1, "maximum": 200}},
		{name: "cursor", description: "Opaque keyset cursor ordered by created_at descending then id descending", schema: map[string]any{"type": "string", "maxLength": 2048}},
		{name: "limit", description: "Deprecated compatibility alias for page_size", schema: map[string]any{"type": "integer", "minimum": 1, "maximum": 200, "deprecated": true}},
	} {
		args = append(args, openAPIQueryParameter(parameter.name, parameter.description, parameter.schema))
	}
	event := openAPIRequiredObject([]string{"id", "event", "actor_id", "summary", "created_at"}, map[string]any{
		"id": map[string]any{"type": "string"}, "event": map[string]any{"type": "string"}, "object_key": map[string]any{"type": "string"},
		"record_id": map[string]any{"type": "string"}, "actor_id": map[string]any{"type": "string"}, "summary": map[string]any{"type": "string"},
		"before": openAPIObject(nil), "after": openAPIObject(nil), "created_at": map[string]any{"type": "string"},
	})
	page := openAPIRequiredObject([]string{"items", "count", "page_size", "truncated", "retention_class", "retention_days"}, map[string]any{
		"items": openAPIArray(event), "count": map[string]any{"type": "integer", "minimum": 0}, "page_size": map[string]any{"type": "integer", "minimum": 1, "maximum": 200},
		"truncated": map[string]any{"type": "boolean"}, "next_cursor": map[string]any{"type": "string"}, "retention_class": map[string]any{"type": "string"},
		"retention_days": map[string]any{"type": "integer", "minimum": 1},
	})
	args = append(args, openAPIJSONResponse("Bounded business audit event page", page))
	paths["/business/audit-events"] = map[string]any{"get": openAPIRuntimeClient(openAPIOperation(
		"listBusinessAuditEventPage", "Audit Business", "List one actor- or record-scoped business audit page with stable keyset continuation", args...,
	), "listBusinessAuditEventPage")}
}
