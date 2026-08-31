package projection

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func enrichObjectsWithFieldValueDomains(objects []definitionmodel.ObjectSchema, dictionaries []appschemamodel.DictionarySchema) []definitionmodel.ObjectSchema {
	type generatedValueDomain struct {
		Items  []appschemamodel.DictionaryItemSchema
		Source string
	}
	domains := map[string]generatedValueDomain{}
	for _, dictionary := range dictionaries {
		key := strings.TrimSpace(dictionary.Key)
		if key == "" {
			continue
		}
		defaultItems := make([]appschemamodel.DictionaryItemSchema, 0, len(dictionary.Items))
		localeItems := make([]appschemamodel.DictionaryItemSchema, 0)
		for _, item := range dictionary.Items {
			if strings.TrimSpace(item.Locale) == "" {
				defaultItems = append(defaultItems, item)
				continue
			}
			localeItems = append(localeItems, item)
		}
		if len(defaultItems) == 0 {
			defaultItems = append(defaultItems, localeItems...)
			localeItems = nil
		}
		if items := mergeGeneratedDictionaryItems(defaultItems, localeItems); len(items) > 0 {
			source := strings.TrimSpace(dictionary.Source)
			if source == "" {
				source = "field_metadata"
			}
			domains[key] = generatedValueDomain{Items: items, Source: source}
		}
	}
	out := append([]definitionmodel.ObjectSchema(nil), objects...)
	for objectIndex := range out {
		fields := append([]definitionmodel.FieldSchema(nil), out[objectIndex].Fields...)
		for fieldIndex := range fields {
			field := fields[fieldIndex]
			dictionaryKey := generatedFieldDictionaryKey(field)
			if dictionaryKey != "" && !generatedFieldHasInlineValueDomain(field) {
				valueDomain := domains[dictionaryKey]
				if len(valueDomain.Items) > 0 {
					items := valueDomain.Items
					config := metadataCloneMap(field.Config)
					config["dictionary_key"] = dictionaryKey
					config["value_domain"] = map[string]any{
						"key":    dictionaryKey,
						"items":  append([]appschemamodel.DictionaryItemSchema(nil), items...),
						"source": valueDomain.Source,
					}
					config["value_domain_items"] = append([]appschemamodel.DictionaryItemSchema(nil), items...)
					config["options"] = append([]appschemamodel.DictionaryItemSchema(nil), items...)
					field.Config = config
					if len(field.Validation.Options) == 0 {
						field.Validation.Options = generatedDictionaryOptionKeys(items)
					}
					fields[fieldIndex] = field
					continue
				}
			}
			items := generatedInlineValueDomainItems(field)
			if len(items) > 0 && len(field.Validation.Options) == 0 {
				field.Validation.Options = generatedDictionaryOptionKeys(items)
			}
			fields[fieldIndex] = field
		}
		out[objectIndex].Fields = fields
	}
	return out
}

func ApplicationSchemaEnrichObjectsWithFieldValueDomains(objects []definitionmodel.ObjectSchema, dictionaries []appschemamodel.DictionarySchema) []definitionmodel.ObjectSchema {
	return enrichObjectsWithFieldValueDomains(objects, dictionaries)
}

func generatedFieldDictionaryKey(field definitionmodel.FieldSchema) string {
	for _, key := range []string{"dictionary_key", "dictionaryKey", "dictionary"} {
		if value, ok := field.Config[key]; ok {
			text := strings.TrimSpace(fmt.Sprint(value))
			if text != "" && text != "<nil>" {
				return text
			}
		}
	}
	return ""
}

func generatedFieldHasInlineValueDomain(field definitionmodel.FieldSchema) bool {
	if field.Options != nil {
		return true
	}
	for _, key := range []string{"value_domain", "valueDomain", "value_domain_items", "valueDomainItems"} {
		if value, ok := field.Config[key]; ok && value != nil {
			return true
		}
	}
	return false
}

func generatedInlineValueDomainItems(field definitionmodel.FieldSchema) []appschemamodel.DictionaryItemSchema {
	if items := generatedDictionaryItemsFromConfig(field.Options); len(items) > 0 {
		return items
	}
	for _, key := range []string{"value_domain", "valueDomain", "value_domain_items", "valueDomainItems"} {
		if value, ok := field.Config[key]; ok {
			if items := generatedDictionaryItemsFromConfig(value); len(items) > 0 {
				return items
			}
		}
	}
	if items := generatedDictionaryItemsFromConfig(field.Config["options"]); len(items) > 0 {
		return items
	}
	return nil
}

func generatedDictionaryItemsFromConfig(value any) []appschemamodel.DictionaryItemSchema {
	if items := metadataDictionaryItemsFromAny(metadataMapFromAny(value)["items"]); len(items) > 0 {
		return items
	}
	if items := metadataDictionaryItemsFromAny(metadataMapFromAny(value)["options"]); len(items) > 0 {
		return items
	}
	return metadataDictionaryItemsFromAny(value)
}

func generatedDictionaryOptionKeys(items []appschemamodel.DictionaryItemSchema) []string {
	out := make([]string, 0, len(items))
	for _, item := range items {
		value := strings.TrimSpace(item.Value)
		if value == "" {
			value = strings.TrimSpace(item.Key)
		}
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func mergeGeneratedDictionaryItems(defaultItems, localizedItems []appschemamodel.DictionaryItemSchema) []appschemamodel.DictionaryItemSchema {
	merged := map[string]appschemamodel.DictionaryItemSchema{}
	for _, item := range defaultItems {
		item = normalizeGeneratedDictionaryItem(item)
		if item.Key != "" {
			merged[item.Key] = item
		}
	}
	for _, item := range localizedItems {
		item.Key = strings.TrimSpace(item.Key)
		item.Tags = append([]string(nil), item.Tags...)
		item.Config = metadataCloneMap(item.Config)
		item.UI = metadataCloneMap(item.UI)
		item.Metadata = metadataCloneMap(item.Metadata)
		base, ok := merged[item.Key]
		if ok {
			if item.Description == "" {
				item.Description = base.Description
			}
			if item.Value == "" {
				item.Value = base.Value
			}
			if item.SortOrder == 0 {
				item.SortOrder = base.SortOrder
			}
			if item.Status == "" {
				item.Status = base.Status
			}
			if item.ParentKey == "" {
				item.ParentKey = base.ParentKey
			}
			if item.Color == "" {
				item.Color = base.Color
			}
			if item.Icon == "" {
				item.Icon = base.Icon
			}
			if len(item.Tags) == 0 {
				item.Tags = append([]string(nil), base.Tags...)
			}
			if len(item.UI) == 0 {
				item.UI = metadataCloneMap(base.UI)
			}
			if len(item.Metadata) == 0 {
				item.Metadata = metadataCloneMap(base.Metadata)
			}
		}
		item = normalizeGeneratedDictionaryItem(item)
		if item.Key != "" {
			merged[item.Key] = item
		}
	}
	items := make([]appschemamodel.DictionaryItemSchema, 0, len(merged))
	for _, item := range merged {
		if item.Status == "active" {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].SortOrder == items[j].SortOrder {
			return items[i].Key < items[j].Key
		}
		return items[i].SortOrder < items[j].SortOrder
	})
	return items
}

func ApplicationSchemaNormalizeGeneratedDictionaryItem(item appschemamodel.DictionaryItemSchema) appschemamodel.DictionaryItemSchema {
	return normalizeGeneratedDictionaryItem(item)
}

func normalizeGeneratedDictionaryItem(item appschemamodel.DictionaryItemSchema) appschemamodel.DictionaryItemSchema {
	item.Key = strings.TrimSpace(item.Key)
	if strings.TrimSpace(item.Value) == "" {
		item.Value = item.Key
	}
	if strings.TrimSpace(item.Label) == "" {
		item.Label = item.Value
	}
	if strings.TrimSpace(item.Status) == "" {
		item.Status = "active"
	}
	if len(item.UI) == 0 {
		item.UI = metadataCloneMap(item.Config)
	}
	if item.Color == "" {
		item.Color = dictionaryConfigString(item.UI, "color")
	}
	if item.Color == "" {
		item.Color = dictionaryConfigString(item.Config, "color")
	}
	if item.Icon == "" {
		item.Icon = dictionaryConfigString(item.UI, "icon")
	}
	if item.Icon == "" {
		item.Icon = dictionaryConfigString(item.Config, "icon")
	}
	item.Tags = append([]string(nil), item.Tags...)
	item.Config = metadataCloneMap(item.Config)
	item.UI = metadataCloneMap(item.UI)
	item.Metadata = metadataCloneMap(item.Metadata)
	return item
}

func dictionaryConfigString(config map[string]any, key string) string {
	value, ok := config[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func metadataCloneMap(value map[string]any) map[string]any {
	if value == nil {
		return nil
	}
	clone := make(map[string]any, len(value))
	for key, item := range value {
		clone[key] = item
	}
	return clone
}

func metadataMapFromAny(value any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	if typed, ok := value.(map[string]any); ok {
		return metadataCloneMap(typed)
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return map[string]any{}
	}
	result := map[string]any{}
	if json.Unmarshal(payload, &result) != nil {
		return map[string]any{}
	}
	return result
}

func metadataDictionaryItemsFromAny(value any) []appschemamodel.DictionaryItemSchema {
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
					Key: strings.TrimSpace(fmt.Sprint(record["key"])), Value: strings.TrimSpace(fmt.Sprint(record["value"])),
					Label: strings.TrimSpace(fmt.Sprint(record["label"])), Description: strings.TrimSpace(fmt.Sprint(record["description"])),
					SortOrder: index, Config: metadataMapFromAny(record["config"]), UI: metadataMapFromAny(record["ui"]), Metadata: metadataMapFromAny(record["metadata"]),
				})
			case string:
				if text := strings.TrimSpace(record); text != "" {
					items = append(items, appschemamodel.DictionaryItemSchema{Key: text, Value: text, Label: text, SortOrder: index})
				}
			}
		}
		return items
	case []string:
		items := make([]appschemamodel.DictionaryItemSchema, 0, len(typed))
		for index, item := range typed {
			if text := strings.TrimSpace(item); text != "" {
				items = append(items, appschemamodel.DictionaryItemSchema{Key: text, Value: text, Label: text, SortOrder: index})
			}
		}
		return items
	default:
		return nil
	}
}
