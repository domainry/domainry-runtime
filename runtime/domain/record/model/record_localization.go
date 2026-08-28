package recordmodel

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

type RecordTranslations map[string]map[string]string

var recordLocalePattern = regexp.MustCompile(`^[A-Za-z]{2,3}(?:-[A-Za-z0-9]{2,8})*$`)

func RecordFieldIsLocalized(field definitionmodel.FieldSchema) bool {
	if field.Config == nil {
		return false
	}
	switch value := field.Config["localized"].(type) {
	case bool:
		return value
	case string:
		return strings.EqualFold(strings.TrimSpace(value), "true")
	default:
		return false
	}
}

func RecordLocalizedFieldKeys(object definitionmodel.ObjectSchema) []string {
	fields := []string{}
	for _, field := range object.Fields {
		if RecordFieldIsLocalized(field) {
			fields = append(fields, strings.TrimSpace(field.Key))
		}
	}
	sort.Strings(fields)
	return fields
}

func RecordValidateLocalizedFieldContract(object definitionmodel.ObjectSchema) error {
	for _, field := range object.Fields {
		if !RecordFieldIsLocalized(field) {
			continue
		}
		switch strings.TrimSpace(field.Type) {
		case "text", "long_text":
		default:
			return fmt.Errorf("localized field %q must use text or long_text, got %q", field.Key, field.Type)
		}
		if field.Unique {
			return fmt.Errorf("localized field %q cannot be unique because uniqueness belongs to stable business values", field.Key)
		}
	}
	return nil
}

func RecordNormalizeTranslations(object definitionmodel.ObjectSchema, input RecordTranslations) ([]RecordLocalizedValueMutation, error) {
	if err := RecordValidateLocalizedFieldContract(object); err != nil {
		return nil, err
	}
	allowed := map[string]bool{}
	for _, field := range RecordLocalizedFieldKeys(object) {
		allowed[field] = true
	}
	mutations := []RecordLocalizedValueMutation{}
	for rawLocale, values := range input {
		locale := strings.ReplaceAll(strings.TrimSpace(rawLocale), "_", "-")
		if !recordLocalePattern.MatchString(locale) {
			return nil, fmt.Errorf("locale %q is not a BCP 47 language tag", rawLocale)
		}
		for rawField, text := range values {
			field := strings.TrimSpace(rawField)
			if !allowed[field] {
				return nil, fmt.Errorf("field %q is not declared localized on object %q", field, object.Key)
			}
			mutations = append(mutations, RecordLocalizedValueMutation{FieldKey: field, Locale: locale, TextValue: strings.TrimSpace(text)})
		}
	}
	sort.Slice(mutations, func(i, j int) bool {
		if mutations[i].Locale != mutations[j].Locale {
			return mutations[i].Locale < mutations[j].Locale
		}
		return mutations[i].FieldKey < mutations[j].FieldKey
	})
	return mutations, nil
}

func RecordLocalizationLocales(requested, fallback string) []string {
	locales := []string{}
	seen := map[string]bool{}
	for _, locale := range []string{requested, fallback} {
		locale = strings.ReplaceAll(strings.TrimSpace(locale), "_", "-")
		if locale != "" && !seen[locale] {
			seen[locale] = true
			locales = append(locales, locale)
		}
	}
	return locales
}
