package projection

import (
	"sort"
	"strconv"

	appschemacontract "github.com/domainry/domainry-runtime/runtime/domain/appschema/contract"
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

// ActionDefinitionAuthoringCapability publishes Action metadata only. Business
// behavior is implemented by generated, source-owned handlers and is never
// described as Runtime JSON steps.
func ActionDefinitionAuthoringCapability() capabilitycontract.CapabilityAuthoringDefinition {
	return capabilitycontract.CapabilityAuthoringDefinition{
		Key: "action.definition", Status: "supported", Lifecycle: "versioned_metadata",
		Parameters: []capabilitycontract.CapabilityAuthoringParameter{
			{Key: "key", Type: "string", Required: true},
			{Key: "object_key", Type: "object_key", Required: true},
			{Key: "kind", Type: "string", Required: true, Enum: actionAuthoringKinds()},
			{Key: "risk_level", Type: "string", Enum: []string{"low", "medium", "high", "critical"}},
			{Key: "assurance_policy", Type: "object"},
			{Key: "target_organization", Type: "object"},
			{Key: "store_organization_mutation", Type: "object"},
			{Key: "expected_schema_hash", Type: "string", Required: true},
		},
		Permissions:        []string{"runtime.appschema.validate_application_definition"},
		ValidationEndpoint: "POST /application-schema/definitions/action/{resourceKey}/validate",
		ConfigurationRoutes: []string{
			"GET /metadata/definitions/action/{resourceKey}",
			"POST /application-schema/definitions/action/{resourceKey}/validate",
			"GET /authoring/snapshot",
			"GET /references",
		},
		ResourceKeyPathParameter: "resourceKey", SystemDraftResourceType: "action",
		InputSchema: actionAuthoringRequestSchema(), OutputSchema: actionAuthoringOutputSchema(),
		OutputVariables: []capabilitycontract.CapabilityAuthoringOutput{
			{Name: "action_key", JSONPointer: "/definition/resource_key", Type: "action_key", VisibleTo: "subsequent_capability_calls"},
			{Name: "schema_hash", JSONPointer: "/snapshot_hash", Type: "schema_hash", VisibleTo: "subsequent_capability_calls"},
		},
		ReferenceContracts: []capabilitycontract.CapabilityAuthoringReference{
			{Kind: "object_key", InputJSONPointer: "/payload/object_key", ResolverEndpoint: "GET /discovery/references/object_key"},
			{Kind: "field_key", InputJSONPointer: "/payload/assurance_policy/approval_version_field", ScopeFrom: "/payload/object_key", ResolverEndpoint: "GET /discovery/references/field_key"},
			{Kind: "field_key", InputJSONPointer: "/payload/assurance_policy/approval_hash_field", ScopeFrom: "/payload/object_key", ResolverEndpoint: "GET /discovery/references/field_key"},
			{Kind: "field_key", InputJSONPointer: "/payload/assurance_policy/maker_field", ScopeFrom: "/payload/object_key", ResolverEndpoint: "GET /discovery/references/field_key"},
		},
		Execution: &capabilitycontract.CapabilityAuthoringExecution{ReadSet: []string{"schema.object", "identity.permission_definition"}, Transaction: "read_only_candidate_validation", Idempotency: "naturally_idempotent_at_candidate_hash", SideEffectLevel: "none", PermissionModel: "runtime.appschema.validate_application_definition", ChangeControl: "source_controlled_json"},
		Errors: []capabilitycontract.CapabilityAuthoringError{
			{Code: "backend.action.definition_invalid", FieldPath: "payload", MessageKey: "backend.action.definition_invalid"},
			{Code: "backend.action.kind_invalid", FieldPath: "payload.kind", ParameterKeys: []string{"allowed", "actual"}, MessageKey: "backend.action.kind_invalid"},
		},
		Examples: actionAuthoringExamples(),
		Sources: []capabilitycontract.CapabilityAuthoringSource{
			{Kind: "projection", Path: "runtime/domain/action/projection/action_authoring_projection.go", Symbol: "ActionDefinitionAuthoringCapability"},
			{Kind: "validation", Path: "runtime/domain/action/validation/action_definition_validation.go", Symbol: "ActionValidateDefinitionIssues"},
		},
	}
}

func actionAuthoringRequestSchema() *capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	assurancePolicy := capabilitycontract.CapabilityAuthoringSchema{
		Type: "object", AdditionalProperties: &closed, Required: []string{"required_methods"},
		Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
			"required_methods":              {Type: "array", Items: &capabilitycontract.CapabilityAuthoringSchema{Type: "string", Enum: actionStringEnums([]string{"normal_login", "recent_reauth", "otp", "maker_checker", "workflow_approval"})}},
			"recent_reauth_max_age_seconds": {Type: "integer"}, "approval_version_field": {Type: "string"},
			"approval_hash_field": {Type: "string"}, "maker_field": {Type: "string"},
		},
		If: &capabilitycontract.CapabilityAuthoringSchema{
			Required: []string{"required_methods"},
			Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
				"required_methods": {Contains: &capabilitycontract.CapabilityAuthoringSchema{Const: definitionmodel.ActionAssuranceWorkflowApproval}},
			},
		},
		Then: &capabilitycontract.CapabilityAuthoringSchema{
			Required: []string{"approval_version_field", "approval_hash_field"},
			Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
				"approval_version_field": {Type: "string", Pattern: `\S`},
				"approval_hash_field":    {Type: "string", Pattern: `\S`},
			},
		},
		Else: &capabilitycontract.CapabilityAuthoringSchema{
			Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
				"approval_version_field": {Type: "string", Pattern: `^\s*$`},
				"approval_hash_field":    {Type: "string", Pattern: `^\s*$`},
			},
		},
	}
	// Payload fields are a recursive tree: scalar leaves, type "object" with
	// nested fields, and repeated variants of either. The tree is published as
	// one local definition so nested levels share the identical contract.
	payloadFieldTypes := append(appschemacontract.ApplicationSchemaAuthoringFieldTypes(), definitionmodel.ActionPayloadTypeObject)
	sort.Strings(payloadFieldTypes)
	payloadFieldItems := definitionmodel.ActionPayloadMaxItems
	payloadFieldFloor := 0
	payloadField := capabilitycontract.CapabilityAuthoringSchema{
		Type: "object", AdditionalProperties: &closed, Required: []string{"key", "type"},
		Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
			"key": {Type: "string"}, "name": {Type: "string"}, "description": {Type: "string"},
			"type":     {Type: "string", Enum: actionStringEnums(payloadFieldTypes)},
			"required": {Type: "boolean"}, "repeated": {Type: "boolean", Default: false},
			"options":           {Type: "array", Items: &capabilitycontract.CapabilityAuthoringSchema{Type: "string"}},
			"fields":            {Type: "array", Items: &capabilitycontract.CapabilityAuthoringSchema{Ref: "#/$defs/" + actionPayloadFieldDefinitionKey}, Description: "Nested fields; required and only valid when type is object. Nesting depth is limited to " + strconv.Itoa(definitionmodel.ActionPayloadMaxDepth) + " levels."},
			"min_items":         {Type: "integer", Minimum: capabilityAuthoringFloat(payloadFieldFloor), Maximum: capabilityAuthoringFloat(payloadFieldItems), Description: "Only valid with repeated."},
			"max_items":         {Type: "integer", Minimum: capabilityAuthoringFloat(payloadFieldFloor), Maximum: capabilityAuthoringFloat(payloadFieldItems), Description: "Only valid with repeated; defaults to " + strconv.Itoa(definitionmodel.ActionPayloadMaxItems) + "."},
			"target_object_key": {Type: "string", Description: "Required for relation fields."},
			"default":           {}, "default_value": {},
		},
	}
	payload := capabilitycontract.CapabilityAuthoringSchema{
		Type: "object", AdditionalProperties: &closed,
		Required: []string{"key", "kind", "label", "object_key"},
		Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
			"key": {Type: "string"}, "object_key": {Type: "string"}, "label": {Type: "string"},
			"kind": {Type: "string", Enum: actionStringEnums(actionAuthoringKinds())}, "risk_level": {Type: "string", Enum: actionStringEnums([]string{"low", "medium", "high", "critical"})},
			"preconditions":  {Type: "array", Items: &capabilitycontract.CapabilityAuthoringSchema{Type: "string"}},
			"payload_fields": {Type: "array", Items: &capabilitycontract.CapabilityAuthoringSchema{Ref: "#/$defs/" + actionPayloadFieldDefinitionKey}}, "defaults": {Type: "object", Description: "Top-level payload keys only; nested defaults use default_value on the nested field."},
			"optimistic_concurrency": {Type: "boolean", Default: false}, "concurrency_field": {Type: "string"}, "assurance_policy": assurancePolicy,
			"target_organization": {
				Type: "object", AdditionalProperties: &closed, Required: []string{"source"},
				Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
					"source": {Type: "string", Enum: actionStringEnums([]string{
						definitionmodel.ActionTargetOrganizationSourceExplicit,
						definitionmodel.ActionTargetOrganizationSourceExplicitOrSoleAuthorizedStore,
						definitionmodel.ActionTargetOrganizationSourceRecordOwner,
						definitionmodel.ActionTargetOrganizationSourceProvisionedStore,
					})},
					"input": {Type: "string"},
				},
			},
			"store_organization_mutation": {
				Type: "object", AdditionalProperties: &closed, Required: []string{"operations"},
				Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
					"operations": {Type: "array", Items: &capabilitycontract.CapabilityAuthoringSchema{Type: "string", Enum: actionStringEnums([]string{"rename", "disable"})}},
				},
			},
		},
	}
	return &capabilitycontract.CapabilityAuthoringSchema{
		Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed,
		Required: []string{"expected_schema_hash", "payload"},
		Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
			"expected_schema_hash": {Type: "string"}, "payload": payload,
		},
		Definitions: map[string]capabilitycontract.CapabilityAuthoringSchema{actionPayloadFieldDefinitionKey: payloadField},
	}
}

const actionPayloadFieldDefinitionKey = "action_payload_field"

func capabilityAuthoringFloat(value int) *float64 {
	result := float64(value)
	return &result
}

func actionAuthoringOutputSchema() *capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	open := true
	return &capabilitycontract.CapabilityAuthoringSchema{
		Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed,
		Required: []string{"definition", "resource_hash", "schema", "snapshot_hash"},
		Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
			"definition": {Type: "object", AdditionalProperties: &open}, "schema": {Type: "object", AdditionalProperties: &open},
			"resource_hash": {Type: "string"}, "snapshot_hash": {Type: "string"},
		},
	}
}

func actionAuthoringExamples() []capabilitycontract.CapabilityAuthoringExample {
	base := map[string]any{"key": "order.complete", "object_key": "order", "label": "Complete order", "kind": "record_update"}
	representative := actionExampleCopy(base)
	representative["preconditions"] = []any{"status == paid"}
	representative["payload_fields"] = []any{map[string]any{"key": "reason", "type": "text", "required": true}}
	invalid := actionExampleCopy(base)
	invalid["kind"] = "script"
	return []capabilitycontract.CapabilityAuthoringExample{
		{Name: "minimal_valid", Value: map[string]any{"expected_schema_hash": "$instance.schema_hash", "payload": base}},
		{Name: "representative", Value: map[string]any{"expected_schema_hash": "$instance.schema_hash", "payload": representative}},
		{Name: "invalid_with_repair", Value: map[string]any{"expected_schema_hash": "$instance.schema_hash", "payload": invalid}, ExpectedErrorCodes: []string{"backend.action.kind_invalid"}},
	}
}

func actionExampleCopy(value map[string]any) map[string]any {
	result := make(map[string]any, len(value)+2)
	for key, item := range value {
		result[key] = item
	}
	return result
}

func actionAuthoringKinds() []string {
	return definitionmodel.ActionKindValues()
}

func actionStringEnums(values []string) []any {
	result := make([]any, len(values))
	for index, value := range values {
		result[index] = value
	}
	return result
}
