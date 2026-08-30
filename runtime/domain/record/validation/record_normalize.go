package validation

import definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
import recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
)

func RecordNormalizeData(object definitionmodel.ObjectSchema, data map[string]any, partial bool) (map[string]any, error) {
	if data == nil {
		data = map[string]any{}
	}
	fields := make(map[string]definitionmodel.FieldSchema, len(object.Fields))
	for _, field := range object.Fields {
		fields[field.Key] = field
	}
	out := map[string]any{}
	for key, value := range data {
		field, ok := fields[key]
		if !ok {
			return nil, validationError("backend.validation.unknown_field", "field", key, "object", object.Key)
		}
		// PATCH semantics distinguish an omitted key from an explicit null. The
		// latter clears an optional field and is required by relation set_null.
		if partial && value == nil {
			out[key] = nil
			continue
		}
		normalized, err := RecordNormalizeFieldValue(field, value)
		if err != nil {
			return nil, err
		}
		if !RecordIsEmptyValue(normalized) || !partial {
			out[key] = normalized
		}
	}
	return out, nil
}

func RecordNormalizeFieldValue(field definitionmodel.FieldSchema, value any) (any, error) {
	if value == nil {
		return nil, nil
	}
	switch field.Type {
	case "integer":
		number, ok := integerValue(value)
		if !ok {
			return nil, validationError("backend.validation.integer", "field", field.Key)
		}
		return number, nil
	case "currency", "percent":
		config, err := recordmodel.RecordNormalizeDecimalConfig(field.Config)
		if err != nil {
			return nil, recordDecimalValidationError(err, field.Key)
		}
		number, err := recordmodel.RecordNormalizeDecimal(value, config)
		if err != nil {
			return nil, recordDecimalValidationError(err, field.Key)
		}
		return number, nil
	case "number":
		number, ok := numericValue(value)
		if !ok {
			return nil, validationError("backend.validation.number", "field", field.Key)
		}
		if math.IsNaN(number) || math.IsInf(number, 0) {
			return nil, validationError("backend.validation.finite_number", "field", field.Key)
		}
		return number, nil
	case "boolean":
		boolean, ok := boolValue(value)
		if !ok {
			return nil, validationError("backend.validation.boolean", "field", field.Key)
		}
		return boolean, nil
	case "date":
		text, ok := stringValue(value)
		if !ok {
			return nil, validationError("backend.validation.date_string", "field", field.Key)
		}
		if strings.TrimSpace(text) == "" {
			return "", nil
		}
		if _, err := time.Parse("2006-01-02", text); err != nil {
			return nil, validationError("backend.validation.date_format", "field", field.Key)
		}
		return text, nil
	case "datetime":
		text, ok := stringValue(value)
		if !ok {
			return nil, validationError("backend.validation.datetime_string", "field", field.Key)
		}
		if strings.TrimSpace(text) == "" {
			return "", nil
		}
		if _, err := time.Parse(time.RFC3339, text); err != nil {
			return nil, validationError("backend.validation.datetime_format", "field", field.Key)
		}
		return text, nil
	default:
		text, ok := stringValue(value)
		if !ok {
			return nil, validationError("backend.validation.string", "field", field.Key)
		}
		return text, nil
	}
}

func validateFieldType(field definitionmodel.FieldSchema, value any) error {
	if value == nil {
		return nil
	}
	switch field.Type {
	case "text", "long_text", "email", "phone", "url", "select", "user", "relation", "date", "datetime":
		if _, ok := value.(string); !ok {
			return validationError("backend.validation.string", "field", field.Key)
		}
	case "currency", "percent":
		config, err := recordmodel.RecordNormalizeDecimalConfig(field.Config)
		if err != nil {
			return recordDecimalValidationError(err, field.Key)
		}
		if _, err := recordmodel.RecordNormalizeDecimal(value, config); err != nil {
			return recordDecimalValidationError(err, field.Key)
		}
	case "integer":
		switch typed := value.(type) {
		case int, int64:
			return nil
		case float64:
			if math.IsNaN(typed) || math.IsInf(typed, 0) || math.Trunc(typed) != typed || typed < math.MinInt64 || typed > math.MaxInt64 {
				return validationError("backend.validation.integer", "field", field.Key)
			}
		default:
			return validationError("backend.validation.integer", "field", field.Key)
		}
	case "number":
		switch typed := value.(type) {
		case float64:
			if math.IsNaN(typed) || math.IsInf(typed, 0) {
				return validationError("backend.validation.finite_number", "field", field.Key)
			}
		case int, int64, float32:
			return nil
		default:
			return validationError("backend.validation.number", "field", field.Key)
		}
	case "boolean":
		if _, ok := value.(bool); !ok {
			return validationError("backend.validation.boolean", "field", field.Key)
		}
	}
	return nil
}

func recordDecimalValidationError(err error, fieldKey string) error {
	if decimalError, ok := err.(*recordmodel.RecordDecimalError); ok {
		return validationError(decimalError.Code, "field", fieldKey)
	}
	return validationError("backend.decimal.value_invalid", "field", fieldKey)
}

func validateFieldRules(field definitionmodel.FieldSchema, value any) error {
	if RecordIsEmptyValue(value) {
		return nil
	}
	if text, ok := value.(string); ok {
		if minLength, ok := intConfig(field.Config, "min_length"); ok && len(text) < minLength {
			return validationError("backend.validation.min_length", "field", field.Key, "min", strconv.Itoa(minLength))
		}
		if maxLength, ok := intConfig(field.Config, "max_length"); ok && len(text) > maxLength {
			return validationError("backend.validation.max_length", "field", field.Key, "max", strconv.Itoa(maxLength))
		}
		if pattern, ok := stringConfig(field.Config, "pattern"); ok {
			matched, err := regexp.MatchString(pattern, text)
			if err != nil {
				return validationError("backend.validation.invalid_pattern", "field", field.Key)
			}
			if !matched {
				return validationError("backend.validation.invalid_format", "field", field.Key)
			}
		}
		if field.Type == "select" {
			options := selectFieldOptions(field)
			if len(options) > 0 && !containsOption(options, text) {
				return validationError("backend.validation.invalid_option", "field", field.Key, "options", strings.Join(options, ", "))
			}
		}
	}
	if number, ok := numericValue(value); ok {
		if min, ok := floatConfig(field.Config, "min"); ok && number < min {
			return validationError("backend.validation.numeric_min", "field", field.Key, "min", fmt.Sprint(min))
		}
		if max, ok := floatConfig(field.Config, "max"); ok && number > max {
			return validationError("backend.validation.numeric_max", "field", field.Key, "max", fmt.Sprint(max))
		}
	}
	return nil
}

func selectFieldOptions(field definitionmodel.FieldSchema) []string {
	options := compactStrings(field.Validation.Options)
	if len(options) > 0 {
		return options
	}
	options = dictionaryItemOptionKeys(field.Options)
	if len(options) > 0 {
		return options
	}
	for _, key := range []string{"options", "value_domain_items", "valueDomainItems"} {
		options = dictionaryItemOptionKeys(field.Config[key])
		if len(options) > 0 {
			return options
		}
	}
	if valueDomain, ok := field.Config["value_domain"].(map[string]any); ok {
		options = dictionaryItemOptionKeys(valueDomain["items"])
		if len(options) > 0 {
			return options
		}
	}
	if valueDomain, ok := field.Config["valueDomain"].(map[string]any); ok {
		options = dictionaryItemOptionKeys(valueDomain["items"])
		if len(options) > 0 {
			return options
		}
	}
	return stringListConfig(field.Config, "options")
}

func dictionaryItemOptionKeys(value any) []string {
	switch typed := value.(type) {
	case []appschemamodel.DictionaryItemSchema:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if option := dictionaryItemOptionKey(item.Key, item.Value); option != "" {
				out = append(out, option)
			}
		}
		return out
	case []map[string]any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if option := dictionaryMapItemOptionKey(item); option != "" {
				out = append(out, option)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			switch next := item.(type) {
			case appschemamodel.DictionaryItemSchema:
				if option := dictionaryItemOptionKey(next.Key, next.Value); option != "" {
					out = append(out, option)
				}
			case map[string]any:
				if option := dictionaryMapItemOptionKey(next); option != "" {
					out = append(out, option)
				}
			case string:
				if option := strings.TrimSpace(next); option != "" {
					out = append(out, option)
				}
			}
		}
		return out
	default:
		return nil
	}
}

func dictionaryMapItemOptionKey(item map[string]any) string {
	key := strings.TrimSpace(fmt.Sprint(item["key"]))
	value := strings.TrimSpace(fmt.Sprint(item["value"]))
	return dictionaryItemOptionKey(key, value)
}

func dictionaryItemOptionKey(key string, value string) string {
	if strings.TrimSpace(value) != "" && strings.TrimSpace(value) != "<nil>" {
		return strings.TrimSpace(value)
	}
	if strings.TrimSpace(key) != "" && strings.TrimSpace(key) != "<nil>" {
		return strings.TrimSpace(key)
	}
	return ""
}

func compactStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if text := strings.TrimSpace(value); text != "" {
			out = append(out, text)
		}
	}
	return out
}

func stringConfig(config map[string]any, key string) (string, bool) {
	value, ok := config[key].(string)
	return value, ok && strings.TrimSpace(value) != ""
}

func intConfig(config map[string]any, key string) (int, bool) {
	number, ok := floatConfig(config, key)
	if !ok {
		return 0, false
	}
	return int(number), true
}

func floatConfig(config map[string]any, key string) (float64, bool) {
	switch value := config[key].(type) {
	case float64:
		return value, true
	case int:
		return float64(value), true
	case int64:
		return float64(value), true
	default:
		return 0, false
	}
}

func stringListConfig(config map[string]any, key string) []string {
	raw, ok := config[key].([]any)
	if !ok {
		if typed, ok := config[key].([]string); ok {
			return typed
		}
		return nil
	}
	values := make([]string, 0, len(raw))
	for _, value := range raw {
		values = append(values, fmt.Sprint(value))
	}
	return values
}
