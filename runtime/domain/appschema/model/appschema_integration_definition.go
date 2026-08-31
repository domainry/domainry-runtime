package appschemamodel

import (
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	localizationmodel "github.com/domainry/domainry-runtime/runtime/domain/localization/model"
)

// ConnectorSchema is Runtime's narrow, transient anti-corruption projection
// for validating application references. The source definition, lifecycle,
// localization, Provider release metadata, and implementations remain owned by
// domainry-connectors and are not persisted by Runtime.
type ConnectorSchema struct {
	Key        string                     `json:"key"`
	Name       string                     `json:"name,omitempty"`
	Source     string                     `json:"source,omitempty"`
	Providers  []ConnectorProviderSchema  `json:"providers,omitempty"`
	Operations []ConnectorOperationSchema `json:"operations,omitempty"`
}

type ConnectorOperationSchema struct {
	Key                   string                             `json:"key"`
	Name                  string                             `json:"name,omitempty"`
	Description           string                             `json:"description,omitempty"`
	I18n                  localizationmodel.LocalizedTextMap `json:"i18n,omitempty"`
	Method                string                             `json:"method,omitempty"`
	ExecutionMode         string                             `json:"execution_mode,omitempty"`
	SideEffect            string                             `json:"side_effect,omitempty"`
	Input                 []definitionmodel.FieldSchema      `json:"input,omitempty"`
	Output                []definitionmodel.FieldSchema      `json:"output,omitempty"`
	TimeoutDefaultSeconds int                                `json:"timeout_default_seconds,omitempty"`
	TimeoutMaxSeconds     int                                `json:"timeout_max_seconds,omitempty"`
	IdempotencySupported  bool                               `json:"idempotency_supported,omitempty"`
	CompensationOperation string                             `json:"compensation_operation,omitempty"`
	TestSupported         bool                               `json:"test_supported,omitempty"`
	DryRunSupported       bool                               `json:"dry_run_supported,omitempty"`
}

// These aliases preserve Runtime's application-schema compatibility while the
// actual event payload and external-identity contracts remain Integration SDK
// owned. Runtime adds only Action/Workflow reference validation around them.
type IntegrationExternalIdentityMappingSchema = integrationsdk.ExternalIdentityMappingRequirement
type IntegrationEventFieldSchema = integrationsdk.EventFieldRequirement

type IntegrationEventMappingSchema struct {
	Key              string                                   `json:"key"`
	Provider         string                                   `json:"provider"`
	EventType        string                                   `json:"event_type,omitempty"`
	CommandPrefix    string                                   `json:"command_prefix,omitempty"`
	TargetType       string                                   `json:"target_type"`
	WorkflowKey      string                                   `json:"workflow_key,omitempty"`
	ObjectKey        string                                   `json:"object_key,omitempty"`
	ObjectKeyPath    string                                   `json:"object_key_path,omitempty"`
	RecordID         string                                   `json:"record_id,omitempty"`
	RecordIDPath     string                                   `json:"record_id_path,omitempty"`
	ActionKey        string                                   `json:"action_key,omitempty"`
	ActionKeyPath    string                                   `json:"action_key_path,omitempty"`
	ActionInput      map[string]string                        `json:"action_input,omitempty"`
	WorkflowInput    map[string]string                        `json:"workflow_input,omitempty"`
	EventFields      []IntegrationEventFieldSchema            `json:"event_fields,omitempty"`
	ExternalIdentity IntegrationExternalIdentityMappingSchema `json:"external_identity,omitempty"`
	Payload          map[string]any                           `json:"payload,omitempty"`
	Enabled          bool                                     `json:"enabled,omitempty"`
}

type IntegrationSchema struct {
	Connectors    []ConnectorSchema               `json:"connectors,omitempty"`
	Connections   []ConnectionSchema              `json:"connections,omitempty"`
	EventMappings []IntegrationEventMappingSchema `json:"event_mappings,omitempty"`
}
