package validation

import (
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"encoding/json"
	"fmt"
	"sort"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func RecordNormalizeListQuery(object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery, principal principalmodel.Principal) recordmodel.RecordListQuery {
	if query.Page <= 0 {
		query.Page = 1
	}
	if query.PageSize <= 0 {
		query.PageSize = 25
	}
	if query.PageSize <= 0 {
		query.PageSize = 25
	}
	if query.PageSize > 200 {
		query.PageSize = 200
	}
	query.SearchFields = allowedFieldKeys(object, query.SearchFields)
	if strings.TrimSpace(query.Search) != "" && len(query.SearchFields) == 0 {
		query.SearchFields = defaultSearchFields(object)
	}
	query.Filters = normalizeListFilters(object, query.Filters)
	query.Sort = allowedSortRules(object, query.Sort)
	if len(query.Sort) == 0 {
		query.Sort = []recordmodel.RecordSortRule{{Field: "id", Direction: "asc"}}
	} else if !recordSortContainsField(query.Sort, "id") {
		query.Sort = append(query.Sort, recordmodel.RecordSortRule{Field: "id", Direction: "asc"})
	}
	query.Scope = "none"
	if principal.AccessBundle != nil {
		query.Scope = "identity_policy"
	} else if principal.SystemScope.Valid() && principal.Allows(object.Key, "read") {
		query.Scope = "all_records"
	}
	query.RootObjectKey = object.Key
	return query
}

func recordSortContainsField(rules []recordmodel.RecordSortRule, field string) bool {
	for _, rule := range rules {
		if strings.TrimSpace(rule.Field) == field {
			return true
		}
	}
	return false
}

func normalizeListFilters(object definitionmodel.ObjectSchema, raw map[string]any) map[string]any {
	out, fields := map[string]any{}, map[string]definitionmodel.FieldSchema{}
	for _, field := range object.Fields {
		fields[field.Key] = field
	}
	keys := make([]string, 0, len(raw))
	for key := range raw {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, rawKey := range keys {
		key, value := strings.TrimSpace(rawKey), raw[rawKey]
		if key == "" || RecordIsEmptyValue(value) {
			continue
		}
		if baseKey, operator, ok := splitFilterOperator(key); ok {
			if operator == "in" && (baseKey == "id" || fields[baseKey].Key != "") {
				if normalized := normalizedListFilterValues(fields[baseKey], value, baseKey == "id"); len(normalized) > 0 {
					out[baseKey+"__in"] = normalized
				}
				continue
			}
			if field, exists := fields[baseKey]; exists {
				switch operator {
				case "gte", "lte":
					if normalized, err := RecordNormalizeFieldValue(field, value); err == nil {
						out[baseKey+"__"+operator] = normalized
					}
				}
			}
			continue
		}
		if field, ok := fields[key]; ok {
			if normalized, err := RecordNormalizeFieldValue(field, value); err == nil {
				out[key] = normalized
			}
			continue
		}
	}
	return out
}

func splitFilterOperator(key string) (string, string, bool) {
	for _, operator := range []string{"gte", "lte", "in"} {
		suffix := "__" + operator
		if strings.HasSuffix(key, suffix) {
			return strings.TrimSuffix(key, suffix), operator, true
		}
	}
	return key, "", false
}

func normalizedListFilterValues(field definitionmodel.FieldSchema, raw any, identity bool) []any {
	values := []any{}
	switch typed := raw.(type) {
	case []any:
		values = typed
	case []string:
		for _, value := range typed {
			values = append(values, value)
		}
	default:
		return nil
	}
	if len(values) > 200 {
		values = values[:200]
	}
	result, seen := make([]any, 0, len(values)), map[string]bool{}
	for _, value := range values {
		var normalized any
		var err error
		if identity {
			normalized = strings.TrimSpace(fmt.Sprint(value))
			if normalized == "" || normalized == "<nil>" {
				continue
			}
		} else {
			normalized, err = RecordNormalizeFieldValue(field, value)
			if err != nil || RecordIsEmptyValue(normalized) {
				continue
			}
		}
		fingerprint := fmt.Sprintf("%T:%v", normalized, normalized)
		if seen[fingerprint] {
			continue
		}
		seen[fingerprint] = true
		result = append(result, normalized)
	}
	return result
}

func allowedFieldKeys(object definitionmodel.ObjectSchema, values []string) []string {
	out := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if RecordFieldExists(object, value) {
			out = append(out, value)
		}
	}
	return out
}

func defaultSearchFields(object definitionmodel.ObjectSchema) []string {
	out := []string{}
	for _, field := range object.Fields {
		switch field.Type {
		case "text", "email", "phone", "url", "select", "user":
			out = append(out, field.Key)
		}
	}
	return out
}

func allowedSortRules(object definitionmodel.ObjectSchema, values []recordmodel.RecordSortRule) []recordmodel.RecordSortRule {
	out := []recordmodel.RecordSortRule{}
	for _, value := range values {
		field := strings.TrimSpace(value.Field)
		if !RecordFieldExists(object, field) && !metaFieldExists(field) {
			continue
		}
		direction := strings.ToLower(strings.TrimSpace(value.Direction))
		if direction != "desc" {
			direction = "asc"
		}
		out = append(out, recordmodel.RecordSortRule{Field: field, Direction: direction})
	}
	return out
}

func RecordFieldExists(object definitionmodel.ObjectSchema, fieldKey string) bool {
	for _, field := range object.Fields {
		if field.Key == fieldKey {
			return true
		}
	}
	return false
}
func metaFieldExists(fieldKey string) bool {
	return fieldKey == "id" || fieldKey == "created_at" || fieldKey == "updated_at"
}

func RecordIntFromAny(value any, fallback int) int {
	switch typed := value.(type) {
	case int:
		return typed
	case int64:
		return int(typed)
	case float64:
		return int(typed)
	case json.Number:
		if parsed, err := typed.Int64(); err == nil {
			return int(parsed)
		}
	}
	return fallback
}

func sortRulesFromAny(value any) []recordmodel.RecordSortRule {
	out := []recordmodel.RecordSortRule{}
	items, ok := value.([]any)
	if !ok {
		return out
	}
	for _, item := range items {
		switch typed := item.(type) {
		case map[string]any:
			out = append(out, recordmodel.RecordSortRule{Field: strings.TrimSpace(fmt.Sprint(typed["field"])), Direction: strings.TrimSpace(fmt.Sprint(typed["direction"]))})
		case string:
			field, direction := splitSortRule(typed)
			out = append(out, recordmodel.RecordSortRule{Field: field, Direction: direction})
		}
	}
	return out
}

func splitSortRule(value string) (string, string) {
	parts := strings.Fields(strings.TrimSpace(value))
	if len(parts) == 0 {
		return "", "asc"
	}
	field, direction := strings.TrimPrefix(parts[0], "-"), "asc"
	if strings.HasPrefix(parts[0], "-") || (len(parts) > 1 && strings.EqualFold(parts[1], "desc")) {
		direction = "desc"
	}
	return field, direction
}
