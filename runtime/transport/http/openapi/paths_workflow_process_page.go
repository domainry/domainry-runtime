package openapi

func annotateWorkflowProcessPages(paths map[string]any) {
	for _, path := range []string{"/workflow/processes", "/workflow/recovery/processes"} {
		item, ok := paths[path].(map[string]any)
		if !ok {
			continue
		}
		operation, ok := item["get"].(map[string]any)
		if !ok {
			continue
		}
		operation["description"] = "Pass page_size (1..200) to receive items, has_more and next_cursor. Follow next_cursor with unchanged filters and caller authorization. Without page_size or cursor, the legacy array and limit behavior is retained. The cursor follows created_at DESC, id DESC; it is a position, not a snapshot of mutable workflow state."
		parameters, _ := operation["parameters"].([]map[string]any)
		for _, name := range []string{"resource_id", "workflow_key", "object_key", "record_id", "initiator_id", "approver_id", "updated_from", "updated_to", "cursor"} {
			parameters = append(parameters, map[string]any{"name": name, "in": "query", "required": false, "schema": map[string]any{"type": "string"}})
		}
		parameters = append(parameters, map[string]any{"name": "status", "in": "query", "required": false, "schema": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}, "style": "form", "explode": true})
		for _, name := range []string{"limit", "definition_version", "page_size"} {
			schema := map[string]any{"type": "integer"}
			if name == "page_size" {
				schema["minimum"], schema["maximum"] = 1, 200
			}
			parameters = append(parameters, map[string]any{"name": name, "in": "query", "required": false, "schema": schema})
		}
		operation["parameters"] = parameters
		process := openAPIObject(map[string]any{"id": map[string]any{"type": "string"}, "workflow_key": map[string]any{"type": "string"}, "object_key": map[string]any{"type": "string"}, "record_id": map[string]any{"type": "string"}, "status": map[string]any{"type": "string"}, "created_at": map[string]any{"type": "string"}, "updated_at": map[string]any{"type": "string"}})
		page := openAPIRequiredObject([]string{"items", "has_more"}, map[string]any{"items": openAPIArray(process), "has_more": map[string]any{"type": "boolean"}, "next_cursor": map[string]any{"type": "string"}})
		responses, _ := operation["responses"].(map[string]any)
		if responses == nil {
			responses = map[string]any{}
		}
		responses["200"] = map[string]any{"description": "Workflow process array or cursor page", "content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"oneOf": []any{openAPIArray(process), page}}}}}
		operation["responses"] = responses
	}
}
