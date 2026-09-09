package validation

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	apperror "github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
)

// Structured payload validation codes. Leaf errors keep the Record validation
// codes (backend.validation.string, integer, required, ...).
const (
	ActionPayloadObjectExpectedCode = "backend.validation.object_expected"
	ActionPayloadArrayExpectedCode  = "backend.validation.array_expected"
	ActionPayloadMinItemsCode       = "backend.validation.min_items"
	ActionPayloadMaxItemsCode       = "backend.validation.max_items"
	ActionPayloadRequiredCode       = "backend.validation.required"
	ActionPayloadUnknownFieldCode   = "backend.validation.unknown_field"
)

// ActionNormalizeStructuredPayload walks the declared payload field tree and
// returns the normalized declared payload. Every error is an
// *apperror.CodedError whose params carry field (full path such as
// a.b[2].c), field_key (leaf key) and object (Action key). Caller-owned extra
// keys must be split off before calling; any key not declared at its level is
// rejected with backend.validation.unknown_field.
//
// Semantics: required object missing/null and required array missing/null/empty
// fail with backend.validation.required; optional containers that are missing
// or null are omitted from the result; an optional empty array is kept.
func ActionNormalizeStructuredPayload(action definitionmodel.ActionSchema, data map[string]any) (map[string]any, error) {
	walker := actionPayloadStructureNormalizer{actionKey: strings.TrimSpace(action.Key)}
	if data == nil {
		data = map[string]any{}
	}
	return walker.normalizeObject(action.PayloadFields, data, "")
}

type actionPayloadStructureNormalizer struct {
	actionKey string
}

func (n actionPayloadStructureNormalizer) normalizeObject(fields []definitionmodel.ActionPayloadField, data map[string]any, prefix string) (map[string]any, error) {
	declared := make(map[string]definitionmodel.ActionPayloadField, len(fields))
	for _, field := range fields {
		if key := strings.TrimSpace(field.Key); key != "" {
			declared[key] = field
		}
	}
	for key := range data {
		if _, ok := declared[key]; !ok {
			return nil, n.failure(ActionPayloadUnknownFieldCode, actionPayloadJoinPath(prefix, key), key, nil)
		}
	}
	out := make(map[string]any, len(data))
	for _, field := range fields {
		key := strings.TrimSpace(field.Key)
		if key == "" {
			continue
		}
		path := actionPayloadJoinPath(prefix, key)
		value, present := data[key]
		switch {
		case field.Repeated:
			normalized, keep, err := n.normalizeArray(field, value, present, path)
			if err != nil {
				return nil, err
			}
			if keep {
				out[key] = normalized
			}
		case field.IsObject():
			if !present || value == nil {
				if field.Required {
					return nil, n.failure(ActionPayloadRequiredCode, path, key, nil)
				}
				continue
			}
			nested, ok := value.(map[string]any)
			if !ok {
				return nil, n.failure(ActionPayloadObjectExpectedCode, path, key, nil)
			}
			normalized, err := n.normalizeObject(field.Fields, nested, path)
			if err != nil {
				return nil, err
			}
			out[key] = normalized
		default:
			if !present && field.DefaultValue != nil {
				value, present = field.DefaultValue, true
			}
			normalized, err := n.normalizeLeaf(field, value, path)
			if err != nil {
				return nil, err
			}
			if present {
				out[key] = normalized
			}
		}
	}
	return out, nil
}

func (n actionPayloadStructureNormalizer) normalizeArray(field definitionmodel.ActionPayloadField, value any, present bool, path string) ([]any, bool, error) {
	key := strings.TrimSpace(field.Key)
	if !present || value == nil {
		if field.Required {
			return nil, false, n.failure(ActionPayloadRequiredCode, path, key, nil)
		}
		return nil, false, nil
	}
	items, ok := actionPayloadArrayValue(value)
	if !ok {
		return nil, false, n.failure(ActionPayloadArrayExpectedCode, path, key, nil)
	}
	minItems, maxItems := 0, definitionmodel.ActionPayloadMaxItems
	if field.MinItems != nil {
		minItems = *field.MinItems
	}
	if field.MaxItems != nil && *field.MaxItems < maxItems {
		maxItems = *field.MaxItems
	}
	if field.Required && minItems < 1 {
		minItems = 1
	}
	if len(items) == 0 && field.Required {
		return nil, false, n.failure(ActionPayloadRequiredCode, path, key, nil)
	}
	if len(items) < minItems {
		return nil, false, n.failure(ActionPayloadMinItemsCode, path, key, map[string]string{"limit": strconv.Itoa(minItems), "actual": strconv.Itoa(len(items))})
	}
	if len(items) > maxItems {
		return nil, false, n.failure(ActionPayloadMaxItemsCode, path, key, map[string]string{"limit": strconv.Itoa(maxItems), "actual": strconv.Itoa(len(items))})
	}
	out := make([]any, 0, len(items))
	for index, item := range items {
		itemPath := fmt.Sprintf("%s[%d]", path, index)
		if field.IsObject() {
			nested, ok := item.(map[string]any)
			if !ok {
				return nil, false, n.failure(ActionPayloadObjectExpectedCode, itemPath, key, nil)
			}
			normalized, err := n.normalizeObject(field.Fields, nested, itemPath)
			if err != nil {
				return nil, false, err
			}
			out = append(out, normalized)
			continue
		}
		if item == nil {
			return nil, false, n.failure(ActionPayloadRequiredCode, itemPath, key, nil)
		}
		normalized, err := n.normalizeLeaf(field, item, itemPath)
		if err != nil {
			return nil, false, err
		}
		if recordvalidation.RecordIsEmptyValue(normalized) {
			return nil, false, n.failure(ActionPayloadRequiredCode, itemPath, key, nil)
		}
		out = append(out, normalized)
	}
	return out, true, nil
}

// normalizeLeaf applies the exact scalar pipeline used by the flat payload
// path: normalize, type check, rule check, then the required check.
func (n actionPayloadStructureNormalizer) normalizeLeaf(field definitionmodel.ActionPayloadField, value any, path string) (any, error) {
	leaf := field
	leaf.Repeated, leaf.Fields, leaf.MinItems, leaf.MaxItems = false, nil, nil, nil
	schema := ActionTypedPayloadField(leaf)
	normalized, err := recordvalidation.RecordNormalizeFieldValue(schema, value)
	if err != nil {
		return nil, n.relocate(err, path, schema.Key)
	}
	if err := recordvalidation.RecordValidateFieldType(schema, normalized); err != nil {
		return nil, n.relocate(err, path, schema.Key)
	}
	if err := recordvalidation.RecordValidateFieldRules(schema, normalized); err != nil {
		return nil, n.relocate(err, path, schema.Key)
	}
	if schema.Required && recordvalidation.RecordIsEmptyValue(normalized) {
		return nil, n.failure(ActionPayloadRequiredCode, path, schema.Key, nil)
	}
	return normalized, nil
}

func (n actionPayloadStructureNormalizer) relocate(err error, path, key string) error {
	var coded *apperror.CodedError
	if !errors.As(err, &coded) {
		return err
	}
	params := map[string]string{}
	for name, value := range coded.Params {
		params[name] = value
	}
	return n.failure(coded.Code, path, key, params)
}

func (n actionPayloadStructureNormalizer) failure(code, path, key string, params map[string]string) error {
	if params == nil {
		params = map[string]string{}
	}
	params["field"] = path
	params["field_key"] = key
	params["object"] = n.actionKey
	return &apperror.CodedError{Code: code, Params: params}
}

func actionPayloadJoinPath(prefix, key string) string {
	if prefix == "" {
		return key
	}
	return prefix + "." + key
}

func actionPayloadArrayValue(value any) ([]any, bool) {
	switch typed := value.(type) {
	case []any:
		return typed, true
	case []map[string]any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, item)
		}
		return out, true
	case []string:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, item)
		}
		return out, true
	default:
		return nil, false
	}
}
