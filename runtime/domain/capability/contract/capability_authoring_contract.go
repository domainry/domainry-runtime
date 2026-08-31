package contract

import (
	connectormodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
)

const (
	RuntimeAuthoringContractVersion = "runtime-authoring-v1"
	// RuntimeAuthoringContractHash identifies the published, canonical authoring catalog.
	// The Application catalog test fails whenever catalog content changes without updating it.
	RuntimeAuthoringContractHash = "aeecaa7a0b08cdb709074668b0eada4fc924d12b13c5f14fabe41e4bb2c85c1e"
)

type CapabilityRuntimeAuthoringContract struct {
	ContractVersion         string                      `json:"contract_version"`
	EndpointContractVersion string                      `json:"endpoint_contract_version"`
	RuntimeVersion          string                      `json:"runtime_version"`
	ContractHash            string                      `json:"contract_hash"`
	InstanceHash            string                      `json:"instance_hash,omitempty"`
	Domains                 []CapabilityAuthoringDomain `json:"domains"`
	Instance                CapabilityAuthoringInstance `json:"instance"`
}

type CapabilityAuthoringDomain struct {
	Key          string                          `json:"key"`
	Capabilities []CapabilityAuthoringDefinition `json:"capabilities"`
}

type CapabilityAuthoringDefinition struct {
	Key                                     string                                 `json:"key"`
	Status                                  string                                 `json:"status"`
	Lifecycle                               string                                 `json:"lifecycle"`
	AllowedContexts                         []string                               `json:"allowed_contexts,omitempty"`
	Parameters                              []CapabilityAuthoringParameter         `json:"parameters,omitempty"`
	Requires                                []string                               `json:"requires,omitempty"`
	Conflicts                               []string                               `json:"conflicts,omitempty"`
	Permissions                             []string                               `json:"permissions,omitempty"`
	AuditEvents                             []string                               `json:"audit_events,omitempty"`
	ValidationEndpoint                      string                                 `json:"validation_endpoint,omitempty"`
	PreviewEndpoint                         string                                 `json:"preview_endpoint,omitempty"`
	SimulationEndpoint                      string                                 `json:"simulation_endpoint,omitempty"`
	ConfigurationRoutes                     []string                               `json:"configuration_routes,omitempty"`
	ResourceOperations                      *CapabilityAuthoringResourceOperations `json:"resource_operations,omitempty"`
	ResourceKeyPathParameter                string                                 `json:"resource_key_path_parameter,omitempty"`
	SystemDraftResourceType                 string                                 `json:"system_draft_resource_type,omitempty"`
	SystemDraftResourceTypeInputJSONPointer string                                 `json:"system_draft_resource_type_input_json_pointer,omitempty"`
	Errors                                  []CapabilityAuthoringError             `json:"errors,omitempty"`
	Examples                                []CapabilityAuthoringExample           `json:"examples,omitempty"`
	InputSchema                             *CapabilityAuthoringSchema             `json:"input_schema,omitempty"`
	OutputSchema                            *CapabilityAuthoringSchema             `json:"output_schema,omitempty"`
	OutputVariables                         []CapabilityAuthoringOutput            `json:"output_variables,omitempty"`
	ReferenceContracts                      []CapabilityAuthoringReference         `json:"reference_contracts,omitempty"`
	Execution                               *CapabilityAuthoringExecution          `json:"execution,omitempty"`
	Sources                                 []CapabilityAuthoringSource            `json:"sources"`
}

// CapabilityAuthoringResourceOperations gives direct-authoring clients an
// explicit operation map. ConfigurationRoutes remains the compatibility list;
// clients must not infer lifecycle semantics from its ordering or HTTP verbs.
type CapabilityAuthoringResourceOperations struct {
	PersistenceMode string                             `json:"persistence_mode"`
	Validate        string                             `json:"validate"`
	Upsert          string                             `json:"upsert"`
	UpsertHeaders   []CapabilityAuthoringRequestHeader `json:"upsert_headers"`
	SuccessSchema   *CapabilityAuthoringSchema         `json:"success_schema"`
	Get             string                             `json:"get"`
	Versions        string                             `json:"versions"`
	Simulate        string                             `json:"simulate,omitempty"`
	Rollback        string                             `json:"rollback,omitempty"`
	Delete          string                             `json:"delete,omitempty"`
}

type CapabilityAuthoringRequestHeader struct {
	Name        string `json:"name"`
	Required    bool   `json:"required"`
	ValueSource string `json:"value_source"`
	Description string `json:"description"`
}

// DirectAuthoringUpsertHeaders is transport protocol, not owner business
// policy. Owners publish this shared contract on every persistent upsert so a
// client never has to infer header names or value sources from Skill text.
func DirectAuthoringUpsertHeaders() []CapabilityAuthoringRequestHeader {
	return []CapabilityAuthoringRequestHeader{
		{Name: "Builder-Task-ID", Required: true, ValueSource: "builder_task_id", Description: "Stable identity of the project-owned builder task."},
		{Name: "Idempotency-Key", Required: true, ValueSource: "request_fingerprint", Description: "Stable key for this capability, resource, and canonical payload."},
		{Name: "Expected-Schema-Hash", Required: true, ValueSource: "expected_resource_hash", Description: "Last observed resource hash, or empty when the resource does not exist."},
	}
}

// DirectAuthoringSuccessSchema publishes the owner-neutral success envelope.
// Resource remains owner-shaped; the hashes and successor summaries are
// stable protocol fields consumed by direct-authoring clients.
func DirectAuthoringSuccessSchema() *CapabilityAuthoringSchema {
	open := true
	closed := false
	return &CapabilityAuthoringSchema{
		Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &open,
		Required: []string{"resource", "resource_hash", "snapshot_hash", "available_successors"},
		Properties: map[string]CapabilityAuthoringSchema{
			"resource": {OneOf: []CapabilityAuthoringSchema{
				{Type: "object", AdditionalProperties: &open},
				{Type: "array"},
			}},
			"resource_hash": {Type: "string", MinLength: capabilityAuthoringIntPointer(1)},
			"snapshot_hash": {Type: "string", MinLength: capabilityAuthoringIntPointer(1)},
			"available_successors": {
				Type: "array", Items: &CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed,
					Required: []string{"key", "domain", "status", "detail_endpoint"},
					Properties: map[string]CapabilityAuthoringSchema{
						"key": {Type: "string"}, "domain": {Type: "string"}, "status": {Type: "string"},
						"detail_endpoint": {Type: "string"}, "validation_endpoint": {Type: "string"},
					},
				},
			},
		},
	}
}

func capabilityAuthoringIntPointer(value int) *int { return &value }

// CapabilityAuthoringSchema is the closed, JSON-Schema-shaped leaf contract
// consumed by direct-authoring clients. Parameters are the compact discovery
// index; InputSchema is the authoritative payload contract when present.
type CapabilityAuthoringSchema struct {
	Schema               string                               `json:"$schema,omitempty"`
	Ref                  string                               `json:"$ref,omitempty"`
	Type                 string                               `json:"type,omitempty"`
	Properties           map[string]CapabilityAuthoringSchema `json:"properties,omitempty"`
	Definitions          map[string]CapabilityAuthoringSchema `json:"$defs,omitempty"`
	Required             []string                             `json:"required,omitempty"`
	Items                *CapabilityAuthoringSchema           `json:"items,omitempty"`
	OneOf                []CapabilityAuthoringSchema          `json:"oneOf,omitempty"`
	Enum                 []any                                `json:"enum,omitempty"`
	Const                any                                  `json:"const,omitempty"`
	Default              any                                  `json:"default,omitempty"`
	Format               string                               `json:"format,omitempty"`
	Minimum              *float64                             `json:"minimum,omitempty"`
	Maximum              *float64                             `json:"maximum,omitempty"`
	MinLength            *int                                 `json:"minLength,omitempty"`
	MaxLength            *int                                 `json:"maxLength,omitempty"`
	MinItems             *int                                 `json:"minItems,omitempty"`
	MaxItems             *int                                 `json:"maxItems,omitempty"`
	AdditionalProperties *bool                                `json:"additionalProperties,omitempty"`
	Description          string                               `json:"description,omitempty"`
}

type CapabilityAuthoringOutput struct {
	Name        string `json:"name"`
	JSONPointer string `json:"json_pointer"`
	Type        string `json:"type"`
	VisibleTo   string `json:"visible_to"`
}

type CapabilityAuthoringReference struct {
	Kind             string `json:"kind"`
	InputJSONPointer string `json:"input_json_pointer"`
	ScopeFrom        string `json:"scope_from,omitempty"`
	ResolverEndpoint string `json:"resolver_endpoint"`
}

type CapabilityAuthoringExecution struct {
	ReadSet         []string `json:"read_set,omitempty"`
	WriteSet        []string `json:"write_set,omitempty"`
	BoundaryClass   string   `json:"boundary_class"`
	Transaction     string   `json:"transaction"`
	Idempotency     string   `json:"idempotency"`
	SideEffects     []string `json:"side_effects,omitempty"`
	SideEffectLevel string   `json:"side_effect_level"`
	Compensation    string   `json:"compensation,omitempty"`
	PermissionModel string   `json:"permission_model"`
	ChangeControl   string   `json:"change_control,omitempty"`
}

type CapabilityAuthoringExample struct {
	Name               string         `json:"name"`
	Value              map[string]any `json:"value"`
	ExpectedErrorCodes []string       `json:"expected_error_codes,omitempty"`
}

type CapabilityAuthoringParameter struct {
	Key           string         `json:"key"`
	Type          string         `json:"type"`
	Required      bool           `json:"required,omitempty"`
	Default       any            `json:"default,omitempty"`
	Enum          []string       `json:"enum,omitempty"`
	Minimum       *float64       `json:"minimum,omitempty"`
	Maximum       *float64       `json:"maximum,omitempty"`
	MinLength     *int           `json:"min_length,omitempty"`
	MaxLength     *int           `json:"max_length,omitempty"`
	RequiredWhen  map[string]any `json:"required_when,omitempty"`
	ConflictsWith []string       `json:"conflicts_with,omitempty"`
	ItemSchema    string         `json:"item_schema,omitempty"`
	Format        string         `json:"format,omitempty"`
	ReadOnly      bool           `json:"read_only,omitempty"`
}

type CapabilityAuthoringError struct {
	Code          string   `json:"code"`
	FieldPath     string   `json:"field_path,omitempty"`
	ParameterKeys []string `json:"parameter_keys,omitempty"`
	MessageKey    string   `json:"message_key"`
}

type CapabilityAuthoringSource struct {
	Kind   string `json:"kind"`
	Path   string `json:"path"`
	Symbol string `json:"symbol,omitempty"`
}

type CapabilityAuthoringInstance struct {
	ObjectKeys          []string                              `json:"object_keys"`
	FieldKeys           []CapabilityAuthoringScopedValues     `json:"field_keys"`
	ActionKeys          []string                              `json:"action_keys"`
	WorkflowKeys        []string                              `json:"workflow_keys"`
	ReportKeys          []string                              `json:"report_keys"`
	RoleKeys            []string                              `json:"role_keys"`
	PermissionKeys      []string                              `json:"permission_keys"`
	UserIDs             []string                              `json:"user_ids"`
	WorkforceProfileIDs []string                              `json:"workforce_profile_ids"`
	DepartmentIDs       []string                              `json:"department_ids"`
	RoleIDs             []string                              `json:"role_ids"`
	MenuIDs             []string                              `json:"menu_ids"`
	ConnectorKeys       []string                              `json:"connector_keys"`
	ConnectionKeys      []string                              `json:"connection_keys"`
	ConnectorOperations []CapabilityAuthoringConnectorBinding `json:"connector_operations"`
}

type CapabilityAuthoringScopedValues struct {
	Scope  string   `json:"scope"`
	Values []string `json:"values"`
}

type CapabilityAuthoringConnectorBinding struct {
	ConnectorKey string   `json:"connector_key"`
	ProviderKeys []string `json:"provider_keys"`
	Operations   []string `json:"operations"`
	Ready        bool     `json:"ready"`
}

type CapabilityInstanceSchema struct {
	Objects      []definitionmodel.ObjectSchema
	Actions      []definitionmodel.ActionSchema
	Workflows    []definitionmodel.WorkflowSchema
	Reports      []reportmodel.ReportSchema
	Integrations connectormodel.IntegrationSchema
}

func authoringContractHash(contract CapabilityRuntimeAuthoringContract) string {
	contract.ContractHash = ""
	contract.InstanceHash = ""
	contract.Instance = CapabilityAuthoringInstance{}
	payload, _ := json.Marshal(contract)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func ContractHash(contract CapabilityRuntimeAuthoringContract) string {
	return authoringContractHash(contract)
}

func CapabilityAuthoringInstanceHash(instance CapabilityAuthoringInstance) string {
	return authoringInstanceHash(instance)
}

func authoringInstanceHash(instance CapabilityAuthoringInstance) string {
	payload, _ := json.Marshal(instance)
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func sortAuthoringContract(contract *CapabilityRuntimeAuthoringContract) {
	sort.Slice(contract.Domains, func(i, j int) bool { return contract.Domains[i].Key < contract.Domains[j].Key })
	for index := range contract.Domains {
		domainContract := &contract.Domains[index]
		sort.Slice(domainContract.Capabilities, func(i, j int) bool { return domainContract.Capabilities[i].Key < domainContract.Capabilities[j].Key })
		for capabilityIndex := range domainContract.Capabilities {
			capability := &domainContract.Capabilities[capabilityIndex]
			sort.Slice(capability.Parameters, func(i, j int) bool { return capability.Parameters[i].Key < capability.Parameters[j].Key })
			for parameterIndex := range capability.Parameters {
				sort.Strings(capability.Parameters[parameterIndex].Enum)
				sort.Strings(capability.Parameters[parameterIndex].ConflictsWith)
			}
			sort.Slice(capability.OutputVariables, func(i, j int) bool { return capability.OutputVariables[i].Name < capability.OutputVariables[j].Name })
			sort.Slice(capability.ReferenceContracts, func(i, j int) bool {
				if capability.ReferenceContracts[i].Kind == capability.ReferenceContracts[j].Kind {
					return capability.ReferenceContracts[i].InputJSONPointer < capability.ReferenceContracts[j].InputJSONPointer
				}
				return capability.ReferenceContracts[i].Kind < capability.ReferenceContracts[j].Kind
			})
			if capability.InputSchema != nil {
				sortAuthoringSchema(capability.InputSchema)
			}
			if capability.OutputSchema != nil {
				sortAuthoringSchema(capability.OutputSchema)
			}
			if capability.Execution != nil {
				sort.Strings(capability.Execution.ReadSet)
				sort.Strings(capability.Execution.WriteSet)
				sort.Strings(capability.Execution.SideEffects)
			}
			sort.Strings(capability.AllowedContexts)
			sort.Strings(capability.Requires)
			sort.Strings(capability.Conflicts)
			sort.Strings(capability.Permissions)
			sort.Strings(capability.AuditEvents)
			sort.Strings(capability.ConfigurationRoutes)
			sort.Slice(capability.Errors, func(i, j int) bool { return capability.Errors[i].Code < capability.Errors[j].Code })
			sort.Slice(capability.Examples, func(i, j int) bool { return capability.Examples[i].Name < capability.Examples[j].Name })
		}
	}
}

func sortAuthoringSchema(schema *CapabilityAuthoringSchema) {
	sort.Strings(schema.Required)
	if schema.Items != nil {
		sortAuthoringSchema(schema.Items)
	}
	for key, property := range schema.Properties {
		sortAuthoringSchema(&property)
		schema.Properties[key] = property
	}
	for key, definition := range schema.Definitions {
		sortAuthoringSchema(&definition)
		schema.Definitions[key] = definition
	}
	for index := range schema.OneOf {
		sortAuthoringSchema(&schema.OneOf[index])
	}
}

func SortAuthoringContract(contract *CapabilityRuntimeAuthoringContract) {
	sortAuthoringContract(contract)
}

func floatPointer(value float64) *float64 { return &value }
func intPointer(value int) *int           { return &value }
