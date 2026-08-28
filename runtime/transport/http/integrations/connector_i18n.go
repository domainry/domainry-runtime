package integrations

import (
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"

	localizationmodel "github.com/domainry/domainry-runtime/runtime/domain/localization/model"

	"strings"

	localization "github.com/domainry/domainry-runtime/runtime/platform/localization"
)

func localizeConnectorCatalog(connectors []integrationmodel.ConnectorSchema, locale string) []integrationmodel.ConnectorSchema {
	locale = localization.NormalizeLocale(locale)
	out := append([]integrationmodel.ConnectorSchema(nil), connectors...)
	for connectorIndex := range out {
		connector := &out[connectorIndex]
		connector.Name = localizedConnectorProperty(connector.I18n, locale, "name", connector.Name)
		connector.Description = localizedConnectorProperty(connector.I18n, locale, "description", connector.Description)
		connector.Providers = append([]integrationmodel.ConnectorProviderSchema(nil), connector.Providers...)
		for providerIndex := range connector.Providers {
			provider := &connector.Providers[providerIndex]
			provider.Name = localizedConnectorProperty(provider.I18n, locale, "name", provider.Name)
			provider.Description = localizedConnectorProperty(provider.I18n, locale, "description", provider.Description)
		}
		connector.Operations = append([]integrationmodel.ConnectorOperationSchema(nil), connector.Operations...)
		for operationIndex := range connector.Operations {
			operation := &connector.Operations[operationIndex]
			operation.Name = localizedConnectorProperty(operation.I18n, locale, "name", operation.Name)
			operation.Description = localizedConnectorProperty(operation.I18n, locale, "description", operation.Description)
		}
	}
	return out
}

func localizedConnectorProperty(values localizationmodel.LocalizedTextMap, locale, property, fallback string) string {
	for _, candidate := range []string{locale, localization.DefaultLocale} {
		if value := strings.TrimSpace(values[candidate][property]); value != "" {
			return value
		}
	}
	return fallback
}
