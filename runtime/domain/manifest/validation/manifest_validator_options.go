package validation

import (
	"encoding/json"
	"fmt"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func (state *validationState) validateDictionaries() {
	for index, dict := range state.manifest.Dictionaries {
		path := fmt.Sprintf("dictionaries[%d]", index)
		if strings.TrimSpace(dict.Key) == "" {
			state.add(path+".key", "is required")
		}
		itemKeys := map[string]bool{}
		for itemIndex, item := range dict.Items {
			itemPath := fmt.Sprintf("%s.items[%d]", path, itemIndex)
			key := strings.TrimSpace(item.Key)
			value := strings.TrimSpace(item.Value)
			if key == "" {
				state.add(itemPath+".key", "is required")
			} else if itemKeys[key] {
				state.add(itemPath+".key", "duplicate dictionary item key %q", key)
			}
			itemKeys[key] = true
			if value == "" {
				value = key
			}
			if !stableOptionValue(value) {
				state.add(itemPath+".value", "must be a stable English storage value, got %q", value)
			}
		}
	}
}

func (state *validationState) validateFieldOptions(path string, field definitionmodel.FieldSchema) {
	allowed := fieldAllowedValues(field)
	for _, value := range allowed {
		if !stableOptionValue(value) {
			state.add(path+".options", "option value must be stable English storage value, got %q", value)
		}
	}
	if field.Options != nil {
		state.validateStructuredOptions(path+".options", field.Options)
	}
	for _, defaultValue := range []any{field.Default, field.DefaultValue} {
		if defaultValue == nil {
			continue
		}
		if field.Type == "relation" {
			state.add(path+".default_value", "relation defaults are unsupported; supply record identity explicitly")
			continue
		}
		if len(allowed) > 0 && !containsString(allowed, strings.TrimSpace(fmt.Sprint(defaultValue))) {
			state.add(path+".default_value", "must match one of the stable option values")
		}
	}
	if dictionaryKey := mapString(field.Config, "dictionary_key"); dictionaryKey != "" {
		if state.dicts[dictionaryKey].Key == "" {
			state.add(path+".config.dictionary_key", "unknown dictionary %q", dictionaryKey)
		}
	}
}

func (state *validationState) validateStructuredOptions(path string, value any) {
	switch typed := value.(type) {
	case []any:
		for index, item := range typed {
			itemPath := fmt.Sprintf("%s[%d]", path, index)
			if _, ok := item.(string); ok {
				state.add(itemPath, "raw string option arrays are not allowed; use {value,label}")
				continue
			}
			optionMap, ok := item.(map[string]any)
			if !ok {
				optionMap = cloneJSONMap(item)
			}
			stored := mapString(optionMap, "value")
			if stored == "" {
				state.add(itemPath+".value", "is required")
			} else if !stableOptionValue(stored) {
				state.add(itemPath+".value", "must be a stable English storage value, got %q", stored)
			}
			if mapString(optionMap, "label") == "" {
				state.add(itemPath+".label", "is required")
			}
		}
	case []string:
		state.add(path, "raw string option arrays are not allowed; use {value,label}")
	}
}

func fieldAllowedValues(field definitionmodel.FieldSchema) []string {
	values := []string{}
	values = append(values, compactStrings(field.Validation.Options)...)
	switch typed := field.Options.(type) {
	case []any:
		for _, item := range typed {
			if optionMap, ok := item.(map[string]any); ok {
				values = append(values, mapString(optionMap, "value"))
			}
		}
	case []map[string]any:
		for _, item := range typed {
			values = append(values, mapString(item, "value"))
		}
	}
	return compactStrings(values)
}

func compactStrings(values []string) []string {
	out := []string{}
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func containsString(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func cloneJSONMap(value any) map[string]any {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil
	}
	return out
}
