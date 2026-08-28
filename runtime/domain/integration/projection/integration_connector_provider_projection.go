package projection

import (
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	localizationmodel "github.com/domainry/domainry-runtime/runtime/domain/localization/model"
)

func IntegrationReplaceConnectorProvider(providers []integrationmodel.ConnectorProviderSchema, replacement integrationmodel.ConnectorProviderSchema) []integrationmodel.ConnectorProviderSchema {
	for index := range providers {
		if providers[index].Key == replacement.Key {
			providers[index] = IntegrationMergeConnectorProviderSchema(providers[index], replacement)
			return providers
		}
	}
	return providers
}

func IntegrationMergeConnectorProviderSchema(base, overlay integrationmodel.ConnectorProviderSchema) integrationmodel.ConnectorProviderSchema {
	if strings.TrimSpace(overlay.ProviderRevision) == "" {
		overlay.ProviderRevision = base.ProviderRevision
	}
	if strings.TrimSpace(overlay.Name) == "" {
		overlay.Name = base.Name
	}
	if strings.TrimSpace(overlay.Description) == "" {
		overlay.Description = base.Description
	}
	mergedI18n := integrationCloneLocalizedTextMap(base.I18n)
	for locale, values := range overlay.I18n {
		if mergedI18n[locale] == nil {
			mergedI18n[locale] = map[string]string{}
		}
		for property, value := range values {
			mergedI18n[locale][property] = value
		}
	}
	overlay.I18n = mergedI18n
	if len(overlay.ConfigFields) > 0 {
		overlay.ConfigFields = integrationMergeConnectorFieldSchemas(base.ConfigFields, overlay.ConfigFields, false)
	}
	if len(overlay.SecretFields) > 0 {
		overlay.SecretFields = integrationMergeConnectorFieldSchemas(base.SecretFields, overlay.SecretFields, true)
	}
	if len(overlay.OperationKeys) == 0 {
		overlay.OperationKeys = append([]string(nil), base.OperationKeys...)
	}
	return overlay
}

func integrationMergeConnectorFieldSchemas(base, overlays []definitionmodel.FieldSchema, secret bool) []definitionmodel.FieldSchema {
	byKey := map[string]definitionmodel.FieldSchema{}
	for _, field := range base {
		byKey[field.Key] = field
	}
	out := append([]definitionmodel.FieldSchema(nil), overlays...)
	for index := range out {
		fallback, ok := byKey[out[index].Key]
		if !ok {
			fallback = integrationConnectorFieldDisplayFallback(out[index], secret)
		}
		if strings.TrimSpace(out[index].Name) == "" {
			out[index].Name = fallback.Name
		}
		if strings.TrimSpace(out[index].Description) == "" {
			out[index].Description = fallback.Description
		}
		if strings.TrimSpace(out[index].Type) == "" {
			out[index].Type = fallback.Type
		}
		if out[index].Default == nil {
			out[index].Default = fallback.Default
		}
		if out[index].DefaultValue == nil {
			out[index].DefaultValue = fallback.DefaultValue
		}
		if out[index].Options == nil {
			out[index].Options = fallback.Options
		}
		out[index].Validation = integrationMergeConnectorFieldValidation(fallback.Validation, out[index].Validation)
		mergedConfig := integrationCloneMap(fallback.Config)
		if mergedConfig == nil {
			mergedConfig = map[string]any{}
		}
		for key, value := range out[index].Config {
			mergedConfig[key] = value
		}
		out[index].Config = mergedConfig
		merged := integrationCloneLocalizedTextMap(fallback.I18n)
		for locale, values := range out[index].I18n {
			if merged[locale] == nil {
				merged[locale] = map[string]string{}
			}
			for property, value := range values {
				merged[locale][property] = value
			}
		}
		out[index].I18n = merged
	}
	return out
}

func integrationMergeConnectorFieldValidation(base, overlay definitionmodel.FieldValidation) definitionmodel.FieldValidation {
	if overlay.MinLength == 0 {
		overlay.MinLength = base.MinLength
	}
	if overlay.MaxLength == 0 {
		overlay.MaxLength = base.MaxLength
	}
	if overlay.Min == nil {
		overlay.Min = base.Min
	}
	if overlay.Max == nil {
		overlay.Max = base.Max
	}
	if strings.TrimSpace(overlay.Pattern) == "" {
		overlay.Pattern = base.Pattern
	}
	if len(overlay.Options) == 0 {
		overlay.Options = append([]string(nil), base.Options...)
	}
	if strings.TrimSpace(overlay.Target) == "" {
		overlay.Target = base.Target
	}
	return overlay
}

func integrationConnectorFieldDisplayFallback(field definitionmodel.FieldSchema, secret bool) definitionmodel.FieldSchema {
	name := strings.TrimSpace(field.Name)
	if name == "" {
		name = strings.ReplaceAll(field.Key, "_", " ")
	}
	description := "Configuration value for " + name + "."
	zhDescription := name + " 配置项。"
	if secret {
		description = "Sensitive credential for " + name + "."
		zhDescription = name + " 敏感凭据。"
	}
	return definitionmodel.FieldSchema{Key: field.Key, Name: name, Description: description, I18n: localizationmodel.LocalizedTextMap{
		"en-US": {"name": name, "description": description},
		"zh-CN": {"name": name, "description": zhDescription},
	}}
}

func integrationCloneLocalizedTextMap(values localizationmodel.LocalizedTextMap) localizationmodel.LocalizedTextMap {
	out := localizationmodel.LocalizedTextMap{}
	for locale, properties := range values {
		out[locale] = map[string]string{}
		for property, value := range properties {
			out[locale][property] = value
		}
	}
	return out
}
