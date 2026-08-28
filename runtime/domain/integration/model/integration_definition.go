package integrationmodel

import definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

import localizationmodel "github.com/domainry/domainry-runtime/runtime/domain/localization/model"

type ConnectorSchema struct {
	Key                   string                             `json:"key"`
	Type                  string                             `json:"type"`
	Provider              string                             `json:"provider"`
	Version               string                             `json:"version,omitempty"`
	MinimumRuntimeVersion string                             `json:"minimum_runtime_version,omitempty"`
	FeatureFlags          []string                           `json:"feature_flags,omitempty"`
	Source                string                             `json:"source,omitempty"`
	Classification        string                             `json:"classification,omitempty"`
	LifecycleStatus       string                             `json:"lifecycle_status,omitempty"`
	ReplacementCapability string                             `json:"replacement_capability,omitempty"`
	Name                  string                             `json:"name,omitempty"`
	Description           string                             `json:"description,omitempty"`
	I18n                  localizationmodel.LocalizedTextMap `json:"i18n,omitempty"`
	Capabilities          []string                           `json:"capabilities,omitempty"`
	Providers             []ConnectorProviderSchema          `json:"providers,omitempty"`
	Operations            []ConnectorOperationSchema         `json:"operations,omitempty"`
	Required              bool                               `json:"required,omitempty"`
	ConfigFields          []string                           `json:"config_fields,omitempty"`
	SecretRefs            []string                           `json:"secret_refs,omitempty"`
	Readiness             string                             `json:"readiness,omitempty"`
	DefinitionReady       bool                               `json:"definition_ready"`
	AdapterReady          bool                               `json:"adapter_ready"`
	ConnectionReady       bool                               `json:"connection_ready"`
	Config                map[string]any                     `json:"config,omitempty"`
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

type IntegrationExternalIdentityMappingSchema struct {
	Provider    string `json:"provider,omitempty"`
	SubjectPath string `json:"subject_path,omitempty"`
	SubjectType string `json:"subject_type,omitempty"`
	NamePath    string `json:"name_path,omitempty"`
	OnUnmapped  string `json:"on_unmapped,omitempty"`
}

// IntegrationEventFieldSchema is the closed inbound payload contract for one
// event mapping. Path uses the same dotted object traversal as PayloadPathValue;
// Runtime validates the actual event before projecting values into an Action or
// Workflow invocation.
type IntegrationEventFieldSchema struct {
	Path     string   `json:"path"`
	Type     string   `json:"type"`
	Options  []string `json:"options,omitempty"`
	Required bool     `json:"required,omitempty"`
}

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
