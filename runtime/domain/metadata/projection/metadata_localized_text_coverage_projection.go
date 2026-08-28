package projection

import (
	"fmt"
	"sort"
	"strings"

	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
)

type metadataLocalizedTextExpectedItem struct {
	entityType  string
	entityKey   string
	property    string
	defaultText string
}

// MetadataLocalizedTextCoverage projects persisted localized texts and a schema
// snapshot into the owner read model. It performs no authorization or I/O.
func MetadataLocalizedTextCoverage(
	workspaceID string,
	locale string,
	fallbackLocale string,
	snapshot metadatamodel.MetadataSchemaSnapshot,
	values []metadatamodel.LocalizedText,
) metadatamodel.LocalizedTextCoverageResult {
	byLocale := make(map[string]metadatamodel.LocalizedText, len(values))
	for _, value := range values {
		byLocale[metadataLocalizedTextCoverageLookupKey(value.Locale, value.EntityType, value.EntityKey, value.Property)] = value
	}

	expected := metadataLocalizedTextExpectedItems(snapshot)
	items := make([]metadatamodel.LocalizedTextCoverageItem, 0, len(expected))
	missing := 0
	for _, item := range expected {
		current := byLocale[metadataLocalizedTextCoverageLookupKey(locale, item.entityType, item.entityKey, item.property)]
		fallback := metadatamodel.LocalizedText{}
		if fallbackLocale != "" {
			fallback = byLocale[metadataLocalizedTextCoverageLookupKey(fallbackLocale, item.entityType, item.entityKey, item.property)]
		}
		coverage := metadatamodel.LocalizedTextCoverageItem{
			WorkspaceID:    workspaceID,
			EntityType:     item.entityType,
			EntityKey:      item.entityKey,
			Property:       item.property,
			Locale:         locale,
			RequestedText:  strings.TrimSpace(current.Text),
			FallbackLocale: fallbackLocale,
			FallbackText:   strings.TrimSpace(fallback.Text),
			DefaultText:    item.defaultText,
			SourceKind:     current.SourceKind,
			SourceID:       current.SourceID,
		}
		switch {
		case coverage.RequestedText != "":
			coverage.ResolvedText = coverage.RequestedText
			coverage.ResolvedSource = "requested_locale"
		case coverage.FallbackText != "":
			coverage.ResolvedText = coverage.FallbackText
			coverage.ResolvedSource = "fallback_locale"
			coverage.Missing = true
		case item.defaultText != "":
			coverage.ResolvedText = item.defaultText
			coverage.ResolvedSource = "default_field"
			coverage.Missing = true
		default:
			coverage.ResolvedText = metadataHumanizeKey(item.entityKey)
			coverage.ResolvedSource = "humanized_key"
			coverage.Missing = true
		}
		if coverage.Missing {
			missing++
		}
		items = append(items, coverage)
	}

	sort.Slice(items, func(i, j int) bool {
		return metadataLocalizedTextCoverageSortKey(items[i]) < metadataLocalizedTextCoverageSortKey(items[j])
	})
	return metadatamodel.LocalizedTextCoverageResult{
		WorkspaceID:    workspaceID,
		Locale:         locale,
		FallbackLocale: fallbackLocale,
		Items:          items,
		MissingCount:   missing,
		TotalCount:     len(items),
	}
}

func metadataLocalizedTextExpectedItems(snapshot metadatamodel.MetadataSchemaSnapshot) []metadataLocalizedTextExpectedItem {
	out := []metadataLocalizedTextExpectedItem{}
	add := func(entityType string, entityKey string, property string, defaultText string) {
		entityKey = strings.TrimSpace(entityKey)
		property = strings.TrimSpace(property)
		if entityKey == "" {
			return
		}
		out = append(out, metadataLocalizedTextExpectedItem{
			entityType: entityType, entityKey: entityKey, property: property, defaultText: strings.TrimSpace(defaultText),
		})
	}
	add("app", "app", "name", snapshot.Name)
	for _, object := range snapshot.Objects {
		add("object", object.Key, "name", object.Name)
		add("object", object.Key, "description", object.Description)
		for _, field := range object.Fields {
			fieldKey := strings.Trim(object.Key+"."+field.Key, ".")
			add("field", fieldKey, "name", field.Name)
			metadataAddValueOptionExpectedItems(&out, "field_option", fieldKey, field.Options)
		}
		for _, validation := range object.Validations {
			key := strings.TrimSpace(validation.Key)
			if key == "" {
				key = strings.Trim(strings.Join([]string{object.Key, validation.Type, validation.FieldKey, strings.Join(validation.Fields, "_")}, "."), ".")
			}
			add("validation", key, "message", validation.Message)
		}
	}
	for _, view := range snapshot.Views {
		add("view", view.Key, "name", view.Name)
	}
	for _, action := range snapshot.Actions {
		add("action", action.Key, "label", action.Label)
		for _, field := range action.PayloadFields {
			add("action_payload_field", strings.Trim(action.Key+"."+field.Key, "."), "name", field.Name)
		}
	}
	for _, workflow := range snapshot.Workflows {
		add("workflow", workflow.Key, "name", workflow.Name)
	}
	for _, dictionary := range snapshot.Dictionaries {
		add("dictionary", dictionary.Key, "name", dictionary.Name)
		add("dictionary", dictionary.Key, "description", dictionary.Description)
		for _, item := range dictionary.Items {
			itemKey := strings.Trim(dictionary.Key+"."+metadataFirstNonEmpty(item.Key, item.Value), ".")
			add("dictionary_item", itemKey, "label", item.Label)
			add("dictionary_item", itemKey, "description", item.Description)
		}
	}
	for _, report := range snapshot.Reports {
		add("report", report.Key, "name", report.Name)
	}
	for _, entrypoint := range snapshot.EntryPoints {
		add("entrypoint", entrypoint.Key, "name", entrypoint.Name)
		add("entrypoint", entrypoint.Key, "description", entrypoint.Description)
	}
	for _, skill := range snapshot.Skills {
		add("skill", skill.Key, "name", skill.Name)
		add("skill", skill.Key, "description", skill.Description)
	}
	for _, agent := range snapshot.Agents {
		add("agent", agent.Key, "name", agent.Name)
		add("agent", agent.Key, "description", agent.Description)
	}
	return out
}

func metadataAddValueOptionExpectedItems(out *[]metadataLocalizedTextExpectedItem, entityType string, parentKey string, value any) {
	switch typed := value.(type) {
	case []map[string]any:
		for _, option := range typed {
			metadataAddValueOptionExpectedItem(out, entityType, parentKey, option)
		}
	case []any:
		for _, item := range typed {
			if option, ok := item.(map[string]any); ok {
				metadataAddValueOptionExpectedItem(out, entityType, parentKey, option)
			}
		}
	}
}

func metadataAddValueOptionExpectedItem(out *[]metadataLocalizedTextExpectedItem, entityType string, parentKey string, option map[string]any) {
	optionKey := strings.TrimSpace(fmt.Sprint(option["value"]))
	if optionKey == "" || optionKey == "<nil>" {
		optionKey = strings.TrimSpace(fmt.Sprint(option["key"]))
	}
	if optionKey == "" || optionKey == "<nil>" {
		return
	}
	entityKey := strings.Trim(parentKey+"."+optionKey, ".")
	label := strings.TrimSpace(fmt.Sprint(option["label"]))
	if label != "" && label != "<nil>" {
		*out = append(*out, metadataLocalizedTextExpectedItem{entityType: entityType, entityKey: entityKey, property: "label", defaultText: label})
	}
	description := strings.TrimSpace(fmt.Sprint(option["description"]))
	if description != "" && description != "<nil>" {
		*out = append(*out, metadataLocalizedTextExpectedItem{entityType: entityType, entityKey: entityKey, property: "description", defaultText: description})
	}
}

func metadataLocalizedTextCoverageLookupKey(locale string, entityType string, entityKey string, property string) string {
	return strings.TrimSpace(locale) + "\x00" + strings.TrimSpace(entityType) + "\x00" + strings.TrimSpace(entityKey) + "\x00" + strings.TrimSpace(property)
}

func metadataLocalizedTextCoverageSortKey(item metadatamodel.LocalizedTextCoverageItem) string {
	missingPrefix := "1"
	if item.Missing {
		missingPrefix = "0"
	}
	return missingPrefix + "\x00" + item.EntityType + "\x00" + item.EntityKey + "\x00" + item.Property
}

func metadataHumanizeKey(key string) string {
	key = strings.NewReplacer("_", " ", ".", " ", "-", " ").Replace(strings.TrimSpace(key))
	return strings.Join(strings.Fields(key), " ")
}

func metadataFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if text := strings.TrimSpace(value); text != "" {
			return text
		}
	}
	return ""
}
