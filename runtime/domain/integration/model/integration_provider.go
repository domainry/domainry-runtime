package integrationmodel

import definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

import localizationmodel "github.com/domainry/domainry-runtime/runtime/domain/localization/model"

// ConnectorProviderSchema identifies a concrete provider within a Connector
// family. Provider-specific typed configuration replaces the legacy practice
// of treating provider="multi" as an executable adapter.
type ConnectorProviderSchema struct {
	Key               string                             `json:"key"`
	ProviderRevision  string                             `json:"provider_revision,omitempty"`
	Name              string                             `json:"name,omitempty"`
	Description       string                             `json:"description,omitempty"`
	I18n              localizationmodel.LocalizedTextMap `json:"i18n,omitempty"`
	ConfigFields      []definitionmodel.FieldSchema      `json:"config_fields,omitempty"`
	SecretFields      []definitionmodel.FieldSchema      `json:"secret_fields,omitempty"`
	OperationKeys     []string                           `json:"operation_keys,omitempty"`
	Readiness         string                             `json:"readiness,omitempty"`
	StartupActivation string                             `json:"startup_activation,omitempty"`
}

type ConnectionSchema struct {
	Key          string                             `json:"key"`
	ConnectorKey string                             `json:"connector_key"`
	ProviderKey  string                             `json:"provider_key,omitempty"`
	Name         string                             `json:"name,omitempty"`
	I18n         localizationmodel.LocalizedTextMap `json:"i18n,omitempty"`
	Status       string                             `json:"status,omitempty"`
	Config       map[string]any                     `json:"config,omitempty"`
}
