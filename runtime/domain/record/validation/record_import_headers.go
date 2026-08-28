package validation

import (
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"fmt"
	"strings"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	localization "github.com/domainry/domainry-runtime/runtime/platform/localization"
)

func RecordImportFieldHeaderAliases(object definitionmodel.ObjectSchema) map[string]definitionmodel.FieldSchema {
	fieldByHeader := map[string]definitionmodel.FieldSchema{}
	for _, field := range object.Fields {
		for _, alias := range importFieldHeaderAliasValues(object.Key, field) {
			if normalized := RecordNormalizeImportHeaderAlias(alias); normalized != "" {
				fieldByHeader[normalized] = field
			}
		}
		if strings.TrimSpace(field.Key) != "" {
			fieldByHeader[strings.TrimSpace(field.Key)] = field
		}
	}
	return fieldByHeader
}

func importFieldHeaderAliasValues(objectKey string, field definitionmodel.FieldSchema) []string {
	aliases := []string{field.Key, field.Name}
	for _, key := range []string{"field." + strings.TrimSpace(objectKey) + "." + strings.TrimSpace(field.Key), "field." + strings.TrimSpace(field.Key)} {
		for _, locale := range localization.SupportedLocales() {
			if label, ok := localization.Lookup(locale, key); ok {
				aliases = append(aliases, label)
			}
		}
	}
	for _, record := range []map[string]any{field.Config, importMapValue(field.Config["ui"])} {
		for _, key := range []string{"label", "display_label", "name", "alias", "aliases"} {
			aliases = append(aliases, importStringListFromAny(record[key])...)
			if text := strings.TrimSpace(fmt.Sprint(record[key])); text != "" && text != "<nil>" {
				aliases = append(aliases, text)
			}
		}
	}
	return aliases
}

func RecordNormalizeImportHeaderAlias(value string) string {
	return strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(strings.TrimSpace(value), " ", ""), "_", ""))
}

func RecordImportErrorSummary(row recordmodel.RecordImportPreviewRow) string {
	if len(row.Issues) == 0 {
		if row.Duplicate {
			return "backend.import.duplicate_row"
		}
		return ""
	}
	parts := make([]string, 0, len(row.Issues))
	for _, issue := range row.Issues {
		code := strings.TrimSpace(issue.Code)
		if code == "" {
			code = strings.TrimSpace(issue.Message)
		}
		if issue.Field != "" {
			code = issue.Field + ":" + code
		}
		if code != "" {
			parts = append(parts, code)
		}
	}
	return strings.Join(parts, "; ")
}

func importMapValue(value any) map[string]any {
	record, _ := value.(map[string]any)
	return record
}
