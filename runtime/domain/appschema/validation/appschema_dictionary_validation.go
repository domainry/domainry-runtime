package validation

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
)

func ApplicationSchemaValidateDictionaryDefinition(resourceKey string, payload json.RawMessage) error {
	_, err := ApplicationSchemaNormalizeDictionaryDefinition(resourceKey, payload)
	return err
}

func ApplicationSchemaNormalizeDictionaryDefinition(resourceKey string, payload json.RawMessage) (json.RawMessage, error) {
	var declared map[string]json.RawMessage
	if err := json.Unmarshal(payload, &declared); err != nil {
		return nil, dictionaryValidationError("backend.dictionary.definition_invalid", "field_path", "dictionary")
	}
	var dictionary appschemamodel.DictionarySchema
	if err := decodeClosedAuthoringJSON(payload, &dictionary); err != nil {
		return nil, dictionaryValidationError("backend.dictionary.definition_invalid", "field_path", "dictionary")
	}
	resourceKey = strings.TrimSpace(resourceKey)
	dictionary.Key = strings.TrimSpace(dictionary.Key)
	if dictionary.Key == "" && declared["key"] == nil {
		dictionary.Key = resourceKey
	}
	if dictionary.Key == "" || dictionary.Key != resourceKey {
		return nil, dictionaryValidationError("backend.dictionary.key_mismatch", "field_path", "key", "dictionary", resourceKey)
	}
	keys, values := map[string]struct{}{}, map[string]struct{}{}
	itemsByKey := map[string]appschemamodel.DictionaryItemSchema{}
	itemIndexes := map[string]int{}
	for index, item := range dictionary.Items {
		locale := strings.ToLower(strings.TrimSpace(item.Locale))
		key := strings.ToLower(strings.TrimSpace(item.Key))
		value := strings.ToLower(strings.TrimSpace(item.Value))
		if key == "" || value == "" {
			field := "key"
			if key != "" {
				field = "value"
			}
			return nil, dictionaryValidationError("backend.dictionary.item_key_value_required", "field_path", fmt.Sprintf("items[%d].%s", index, field), "dictionary", resourceKey)
		}
		localizedKey, localizedValue := locale+"\x00"+key, locale+"\x00"+value
		if _, exists := keys[localizedKey]; exists {
			return nil, dictionaryValidationError("backend.dictionary.item_key_exists", "field_path", fmt.Sprintf("items[%d].key", index), "item", item.Key)
		}
		if _, exists := values[localizedValue]; exists {
			return nil, dictionaryValidationError("backend.dictionary.item_value_exists", "field_path", fmt.Sprintf("items[%d].value", index), "value", item.Value)
		}
		keys[localizedKey], values[localizedValue] = struct{}{}, struct{}{}
		if locale == "" {
			itemsByKey[key], itemIndexes[key] = item, index
		}
	}
	for key, item := range itemsByKey {
		parentKey := strings.ToLower(strings.TrimSpace(item.ParentKey))
		if parentKey == "" {
			continue
		}
		if parentKey == key {
			return nil, dictionaryValidationError("backend.dictionary.item_parent_self", "field_path", fmt.Sprintf("items[%d].parent_key", itemIndexes[key]), "item", item.Key)
		}
		if _, exists := itemsByKey[parentKey]; !exists {
			return nil, dictionaryValidationError("backend.dictionary.item_parent_not_found", "field_path", fmt.Sprintf("items[%d].parent_key", itemIndexes[key]), "parent_key", item.ParentKey)
		}
	}
	for key, item := range itemsByKey {
		parentKey := strings.ToLower(strings.TrimSpace(item.ParentKey))
		if parentKey == "" {
			continue
		}
		seen, cursor := map[string]bool{key: true}, parentKey
		for cursor != "" {
			if seen[cursor] {
				return nil, dictionaryValidationError("backend.dictionary.item_parent_cycle", "field_path", fmt.Sprintf("items[%d].parent_key", itemIndexes[key]), "item", item.Key)
			}
			seen[cursor] = true
			parent := itemsByKey[cursor]
			cursor = strings.ToLower(strings.TrimSpace(parent.ParentKey))
		}
	}
	normalized, _ := json.Marshal(dictionary)
	return normalized, nil
}

func dictionaryValidationError(code string, params ...string) error {
	values := map[string]string{}
	for index := 0; index+1 < len(params); index += 2 {
		values[params[index]] = params[index+1]
	}
	return &apperror.AppError{Kind: apperror.KindBadRequest, Code: code, Params: values}
}
