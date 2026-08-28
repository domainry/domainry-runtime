package integrationtest

func actionQueryReference(source, valueType string, path ...string) map[string]any {
	return map[string]any{"kind": "reference", "value_type": valueType, "reference": map[string]any{"source": source, "path": path}}
}

func actionQueryLiteral(valueType string, value any) map[string]any {
	return map[string]any{"kind": "literal", "value_type": valueType, "value": value}
}

func actionQueryEquals(field string, expression map[string]any) map[string]any {
	return map[string]any{"operator": "eq", "field": field, "value": expression}
}

func actionQueryExactlyOneStep(key, objectKey string, filters ...map[string]any) map[string]any {
	var filter any
	if len(filters) == 1 {
		filter = filters[0]
	} else if len(filters) > 1 {
		children := make([]any, 0, len(filters))
		for _, candidate := range filters {
			children = append(children, candidate)
		}
		filter = map[string]any{"operator": "and", "children": children}
	}
	query := map[string]any{"sort": []any{}, "limit": 1, "expect": "exactly_one", "select_fields": []any{}, "lock_intent": "none"}
	if filter != nil {
		query["filter"] = filter
	}
	return map[string]any{"key": key, "type": "query_records", "object_key": objectKey, "query": query}
}
