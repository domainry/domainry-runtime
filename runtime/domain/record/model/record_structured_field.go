package recordmodel

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

const (
	RecordMultiSelectFieldType = "multi_select"
	RecordJSONFieldType        = "json"

	RecordMultiSelectDefaultMaxItems = 100
	RecordMultiSelectMaximumItems    = 1000
	RecordJSONDefaultMaxBytes        = 64 << 10
	RecordJSONMaximumBytes           = 1 << 20
	RecordJSONMaximumDepth           = 16
	RecordJSONMaximumNodes           = 10000
)

type RecordStructuredFieldPolicy struct {
	MaxItems     int
	JSONShape    string
	MaxJSONBytes int
}

type RecordStructuredFieldError struct {
	Code   string
	Detail string
}

func (e *RecordStructuredFieldError) Error() string {
	if strings.TrimSpace(e.Detail) == "" {
		return e.Code
	}
	return e.Code + ": " + e.Detail
}

func RecordIsStructuredFieldType(value string) bool {
	switch strings.TrimSpace(value) {
	case RecordMultiSelectFieldType, RecordJSONFieldType:
		return true
	default:
		return false
	}
}

// RecordStructuredFieldPolicyFor validates definition-level constraints shared
// by project-model bootstrap and the record value pipeline. Structured
// values deliberately have no portable scalar index or uniqueness semantics.
func RecordStructuredFieldPolicyFor(field definitionmodel.FieldSchema) (RecordStructuredFieldPolicy, error) {
	kind := strings.TrimSpace(field.Type)
	policy := RecordStructuredFieldPolicy{
		MaxItems:     RecordMultiSelectDefaultMaxItems,
		JSONShape:    "object",
		MaxJSONBytes: RecordJSONDefaultMaxBytes,
	}
	if !RecordIsStructuredFieldType(kind) {
		return RecordStructuredFieldPolicy{}, structuredFieldError("backend.structured.field_type_invalid", kind)
	}
	if field.Unique {
		return RecordStructuredFieldPolicy{}, structuredFieldError("backend.structured.unique_unsupported", field.Key)
	}
	if raw, found := field.Config["indexed"]; found {
		indexed, ok := raw.(bool)
		if !ok {
			return RecordStructuredFieldPolicy{}, structuredFieldError("backend.structured.indexed_invalid", field.Key)
		}
		if indexed {
			return RecordStructuredFieldPolicy{}, structuredFieldError("backend.structured.index_unsupported", field.Key)
		}
	}
	if kind == RecordMultiSelectFieldType {
		if raw, found := field.Config["max_items"]; found {
			value, ok := recordStructuredInteger(raw)
			if !ok || value < 1 || value > RecordMultiSelectMaximumItems {
				return RecordStructuredFieldPolicy{}, structuredFieldError("backend.multi_select.max_items_invalid", fmt.Sprint(raw))
			}
			policy.MaxItems = value
		}
		for _, key := range []string{"json_shape", "max_json_bytes"} {
			if _, found := field.Config[key]; found {
				return RecordStructuredFieldPolicy{}, structuredFieldError("backend.structured.config_on_wrong_type", key)
			}
		}
		return policy, nil
	}
	if len(field.Validation.Options) > 0 || field.Options != nil {
		return RecordStructuredFieldPolicy{}, structuredFieldError("backend.json.options_unsupported", field.Key)
	}
	if raw, found := field.Config["options"]; found && raw != nil {
		return RecordStructuredFieldPolicy{}, structuredFieldError("backend.json.options_unsupported", field.Key)
	}
	if _, found := field.Config["max_items"]; found {
		return RecordStructuredFieldPolicy{}, structuredFieldError("backend.structured.config_on_wrong_type", "max_items")
	}
	if raw, found := field.Config["json_shape"]; found {
		value, ok := raw.(string)
		if !ok {
			return RecordStructuredFieldPolicy{}, structuredFieldError("backend.json.shape_invalid", fmt.Sprint(raw))
		}
		policy.JSONShape = strings.ToLower(strings.TrimSpace(value))
	}
	if policy.JSONShape != "object" && policy.JSONShape != "array" {
		return RecordStructuredFieldPolicy{}, structuredFieldError("backend.json.shape_invalid", policy.JSONShape)
	}
	if raw, found := field.Config["max_json_bytes"]; found {
		value, ok := recordStructuredInteger(raw)
		if !ok || value < 1 || value > RecordJSONMaximumBytes {
			return RecordStructuredFieldPolicy{}, structuredFieldError("backend.json.max_size_invalid", fmt.Sprint(raw))
		}
		policy.MaxJSONBytes = value
	}
	return policy, nil
}

func RecordValidateStructuredFieldDefinition(field definitionmodel.FieldSchema) error {
	policy, err := RecordStructuredFieldPolicyFor(field)
	if err != nil {
		return err
	}
	for _, value := range []any{field.Default, field.DefaultValue} {
		if value != nil {
			if _, err := recordNormalizeStructuredFieldValue(field, value, policy); err != nil {
				return err
			}
		}
	}
	if field.Upgrade != nil && strings.TrimSpace(field.Upgrade.ExistingRows) == definitionmodel.FieldUpgradeBackfill && field.Upgrade.BackfillValue != nil {
		if _, err := recordNormalizeStructuredFieldValue(field, field.Upgrade.BackfillValue, policy); err != nil {
			return err
		}
	}
	return nil
}

func RecordNormalizeStructuredFieldValue(field definitionmodel.FieldSchema, value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	policy, err := RecordStructuredFieldPolicyFor(field)
	if err != nil {
		return nil, err
	}
	return recordNormalizeStructuredFieldValue(field, value, policy)
}

func recordNormalizeStructuredFieldValue(field definitionmodel.FieldSchema, value any, policy RecordStructuredFieldPolicy) (any, error) {
	if strings.TrimSpace(field.Type) == RecordMultiSelectFieldType {
		values, ok := recordStructuredStringSlice(value)
		if !ok {
			return nil, structuredFieldError("backend.validation.multi_select", field.Key)
		}
		seen := make(map[string]bool, len(values))
		result := make([]string, 0, len(values))
		for _, raw := range values {
			item := strings.TrimSpace(raw)
			if item == "" {
				return nil, structuredFieldError("backend.validation.multi_select_item", field.Key)
			}
			if !seen[item] {
				seen[item] = true
				result = append(result, item)
			}
		}
		if len(result) > policy.MaxItems {
			return nil, structuredFieldError("backend.validation.multi_select_max_items", field.Key)
		}
		sort.Strings(result)
		return result, nil
	}
	normalized, encoded, err := normalizeJSONDocument(value)
	if err != nil {
		return nil, structuredFieldError("backend.validation.json", field.Key)
	}
	if len(encoded) > policy.MaxJSONBytes {
		return nil, structuredFieldError("backend.validation.json_too_large", field.Key)
	}
	switch policy.JSONShape {
	case "object":
		if _, ok := normalized.(map[string]any); !ok {
			return nil, structuredFieldError("backend.validation.json_shape", field.Key)
		}
	case "array":
		if _, ok := normalized.([]any); !ok {
			return nil, structuredFieldError("backend.validation.json_shape", field.Key)
		}
	}
	nodes := 0
	if !recordJSONWithinLimits(normalized, 1, &nodes) {
		return nil, structuredFieldError("backend.validation.json_complexity", field.Key)
	}
	return normalized, nil
}

func RecordEncodeStructuredFieldValue(field definitionmodel.FieldSchema, value any) (string, error) {
	normalized, err := RecordNormalizeStructuredFieldValue(field, value)
	if err != nil {
		return "", err
	}
	encoded, err := json.Marshal(normalized)
	if err != nil {
		return "", structuredFieldError("backend.validation.json", field.Key)
	}
	return string(encoded), nil
}

func RecordDecodeStructuredFieldValue(field definitionmodel.FieldSchema, value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	var raw []byte
	switch typed := value.(type) {
	case []byte:
		raw = append([]byte(nil), typed...)
	case string:
		raw = []byte(typed)
	default:
		return RecordNormalizeStructuredFieldValue(field, value)
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	decoded, err := decodeJSONDocument(raw)
	if err != nil {
		return nil, structuredFieldError("backend.validation.json", field.Key)
	}
	return RecordNormalizeStructuredFieldValue(field, decoded)
}

func normalizeJSONDocument(value any) (any, []byte, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, nil, err
	}
	decoded, err := decodeJSONDocument(encoded)
	if err != nil {
		return nil, nil, err
	}
	canonical, err := json.Marshal(decoded)
	if err != nil {
		return nil, nil, err
	}
	return decoded, canonical, nil
}

func decodeJSONDocument(raw []byte) (any, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return nil, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return nil, fmt.Errorf("multiple JSON values")
		}
		return nil, err
	}
	return decoded, nil
}

func recordJSONWithinLimits(value any, depth int, nodes *int) bool {
	*nodes++
	if depth > RecordJSONMaximumDepth || *nodes > RecordJSONMaximumNodes {
		return false
	}
	switch typed := value.(type) {
	case map[string]any:
		for _, child := range typed {
			if !recordJSONWithinLimits(child, depth+1, nodes) {
				return false
			}
		}
	case []any:
		for _, child := range typed {
			if !recordJSONWithinLimits(child, depth+1, nodes) {
				return false
			}
		}
	}
	return true
}

func recordStructuredStringSlice(value any) ([]string, bool) {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...), true
	case []any:
		result := make([]string, 0, len(typed))
		for _, item := range typed {
			text, ok := item.(string)
			if !ok {
				return nil, false
			}
			result = append(result, text)
		}
		return result, true
	default:
		return nil, false
	}
}

func recordStructuredInteger(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), int64(int(typed)) == typed
	case float64:
		return int(typed), typed == float64(int(typed))
	case json.Number:
		parsed, err := typed.Int64()
		return int(parsed), err == nil && int64(int(parsed)) == parsed
	default:
		return 0, false
	}
}

func structuredFieldError(code, detail string) error {
	return &RecordStructuredFieldError{Code: code, Detail: strings.TrimSpace(detail)}
}
