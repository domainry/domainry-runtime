package validation

import (
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"fmt"
	"strings"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"

	localization "github.com/domainry/domainry-runtime/runtime/platform/localization"
)

var importValueDomainLookup = localization.Lookup

func coerceImportValue(objectKey string, field definitionmodel.FieldSchema, value string) (any, string) {
	if mapped, ok := importValueDomainValue(objectKey, field, value); ok {
		return mapped, ""
	}
	if importFieldRequiresValueDomain(field) {
		return nil, importValueDomainIssue(field, value)
	}
	if field.Type == "currency" || field.Type == "percent" {
		config, err := recordmodel.RecordNormalizeDecimalConfig(field.Config)
		if err != nil {
			return value, ""
		}
		if normalized, err := recordmodel.RecordNormalizeDecimal(value, config); err == nil {
			return normalized, ""
		}
		return value, ""
	}
	if field.Type == "number" {
		var number float64
		if _, err := fmt.Sscan(value, &number); err == nil {
			return number, ""
		}
	}
	if field.Type == "boolean" {
		return strings.EqualFold(value, "true") || value == "1" || strings.EqualFold(value, "yes"), ""
	}
	return value, ""
}

func RecordCoerceImportValue(objectKey string, field definitionmodel.FieldSchema, value string) (any, string) {
	return coerceImportValue(objectKey, field, value)
}

func importValueDomainValue(objectKey string, field definitionmodel.FieldSchema, raw string) (string, bool) {
	needle := normalizeImportValueDomainText(raw)
	if needle == "" {
		return "", false
	}
	for _, item := range importValueDomainItems(field) {
		value := strings.TrimSpace(item.Value)
		if value == "" {
			value = strings.TrimSpace(item.Key)
		}
		for _, candidate := range []string{item.Key, item.Value, item.Label, item.Description} {
			if normalizeImportValueDomainText(candidate) == needle {
				return value, true
			}
		}
		for _, candidate := range importValueDomainAliases(item) {
			if normalizeImportValueDomainText(candidate) == needle {
				return value, true
			}
		}
		for _, candidate := range importValueDomainI18nAliases(objectKey, field.Key, value) {
			if normalizeImportValueDomainText(candidate) == needle {
				return value, true
			}
		}
	}
	return "", false
}

func importFieldRequiresValueDomain(field definitionmodel.FieldSchema) bool {
	return len(importValueDomainItems(field)) > 0 && (field.Type == "select" || field.Type == "status" || strings.Contains(strings.ToLower(field.Key), "status") || strings.Contains(strings.ToLower(field.Key), "stage"))
}

func importValueDomainIssue(field definitionmodel.FieldSchema, value string) string {
	if len(importValueDomainCandidates(field)) == 0 {
		return "backend.import.invalid_value_domain"
	}
	return "backend.import.invalid_value_domain_option"
}

func importValueDomainCandidates(field definitionmodel.FieldSchema) []string {
	items := importValueDomainItems(field)
	out := make([]string, 0, len(items))
	seen := map[string]bool{}
	for _, item := range items {
		label := strings.TrimSpace(item.Label)
		value := strings.TrimSpace(item.Value)
		if value == "" {
			value = strings.TrimSpace(item.Key)
		}
		display := strings.TrimSpace(label)
		if display == "" {
			display = value
		}
		if value != "" && display != value {
			display += " (" + value + ")"
		}
		if display != "" && !seen[display] {
			seen[display] = true
			out = append(out, display)
		}
	}
	return out
}

func RecordImportValueDomainCandidates(field definitionmodel.FieldSchema) []string {
	return importValueDomainCandidates(field)
}

func importValueDomainItems(field definitionmodel.FieldSchema) []appschemamodel.DictionaryItemSchema {
	config := field.Config
	if config == nil {
		return nil
	}
	for _, key := range []string{"value_domain", "valueDomain", "domain"} {
		if items := dictionaryItemsFromAny(importMapFromAny(config[key])["items"]); len(items) > 0 {
			return items
		}
		if items := dictionaryItemsFromAny(importMapFromAny(config[key])["options"]); len(items) > 0 {
			return items
		}
	}
	for _, key := range []string{"value_domain_items", "valueDomainItems", "options"} {
		if items := dictionaryItemsFromAny(config[key]); len(items) > 0 {
			return items
		}
	}
	if len(field.Validation.Options) > 0 {
		items := make([]appschemamodel.DictionaryItemSchema, 0, len(field.Validation.Options))
		for index, option := range field.Validation.Options {
			option = strings.TrimSpace(option)
			if option != "" {
				items = append(items, appschemamodel.DictionaryItemSchema{Key: option, Value: option, Label: option, SortOrder: index})
			}
		}
		return items
	}
	return nil
}

func dictionaryItemsFromAny(value any) []appschemamodel.DictionaryItemSchema {
	switch typed := value.(type) {
	case []appschemamodel.DictionaryItemSchema:
		return append([]appschemamodel.DictionaryItemSchema(nil), typed...)
	case []any:
		items := make([]appschemamodel.DictionaryItemSchema, 0, len(typed))
		for index, item := range typed {
			switch record := item.(type) {
			case appschemamodel.DictionaryItemSchema:
				items = append(items, record)
			case map[string]any:
				items = append(items, appschemamodel.DictionaryItemSchema{
					Key:         importValueDomainString(record["key"]),
					Value:       importValueDomainString(record["value"]),
					Label:       importValueDomainString(record["label"]),
					Description: importValueDomainString(record["description"]),
					SortOrder:   index,
					Config:      importMapFromAny(record["config"]),
					UI:          importMapFromAny(record["ui"]),
					Metadata:    importMapFromAny(record["metadata"]),
				})
			case string:
				text := strings.TrimSpace(record)
				if text != "" {
					items = append(items, appschemamodel.DictionaryItemSchema{Key: text, Value: text, Label: text, SortOrder: index})
				}
			}
		}
		return items
	case []string:
		items := make([]appschemamodel.DictionaryItemSchema, 0, len(typed))
		for index, item := range typed {
			item = strings.TrimSpace(item)
			if item != "" {
				items = append(items, appschemamodel.DictionaryItemSchema{Key: item, Value: item, Label: item, SortOrder: index})
			}
		}
		return items
	default:
		return nil
	}
}

func importValueDomainString(value any) string {
	if value == nil {
		return ""
	}
	text := strings.TrimSpace(fmt.Sprint(value))
	if text == "<nil>" {
		return ""
	}
	return text
}

func importValueDomainAliases(item appschemamodel.DictionaryItemSchema) []string {
	aliases := []string{}
	for _, record := range []map[string]any{item.Config, item.UI, item.Metadata} {
		for _, key := range []string{"alias", "aliases", "name", "label", "zh", "zh-CN", "en", "en-US"} {
			aliases = append(aliases, importStringListFromAny(record[key])...)
			if text := strings.TrimSpace(fmt.Sprint(record[key])); text != "" && text != "<nil>" {
				aliases = append(aliases, text)
			}
		}
	}
	return aliases
}

func importValueDomainI18nAliases(objectKey string, fieldKey string, value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	keys := []string{
		"option." + strings.TrimSpace(objectKey) + "." + strings.TrimSpace(fieldKey) + "." + value,
		"option." + strings.TrimSpace(fieldKey) + "." + value,
		"option." + value,
	}
	aliases := []string{}
	seen := map[string]bool{}
	for _, locale := range localization.SupportedLocales() {
		for _, key := range keys {
			label, ok := importValueDomainLookup(locale, key)
			if !ok {
				continue
			}
			label = strings.TrimSpace(label)
			if label == "" || seen[label] {
				continue
			}
			seen[label] = true
			aliases = append(aliases, label)
		}
	}
	return aliases
}

func normalizeImportValueDomainText(value string) string {
	value = strings.TrimSpace(strings.ToLower(value))
	value = strings.ReplaceAll(value, " ", "")
	value = strings.ReplaceAll(value, "_", "")
	value = strings.ReplaceAll(value, "-", "")
	return value
}
