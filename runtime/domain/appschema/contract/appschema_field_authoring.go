package contract

import capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"

func ApplicationSchemaFieldAuthoringCapability() capabilitycontract.CapabilityAuthoringDefinition {
	payload := metadataFieldPayloadSchema(ApplicationSchemaAuthoringFieldTypes())
	return capabilitycontract.CapabilityAuthoringDefinition{
		Key: "schema.field", Status: "supported", Lifecycle: "versioned_metadata",
		SystemDraftResourceType: "field",
		Parameters: []capabilitycontract.CapabilityAuthoringParameter{
			{Key: "key", Type: "string", Required: true, MinLength: metadataIntPointer(1)},
			{Key: "name", Type: "string", Required: true, MinLength: metadataIntPointer(1)},
			{Key: "type", Type: "string", Required: true, Enum: ApplicationSchemaAuthoringFieldTypes()},
			{Key: "required", Type: "boolean", Default: false}, {Key: "unique", Type: "boolean", Default: false},
			{Key: "default_value", Type: "any"}, {Key: "min_length", Type: "integer", Minimum: metadataFloatPointer(0)},
			{Key: "max_length", Type: "integer", Minimum: metadataFloatPointer(0)}, {Key: "minimum", Type: "number"}, {Key: "maximum", Type: "number"},
			{Key: "precision", Type: "integer", Minimum: metadataFloatPointer(1), Maximum: metadataFloatPointer(38), RequiredWhen: map[string]any{"type": "currency"}, Default: 19},
			{Key: "scale", Type: "integer", Minimum: metadataFloatPointer(0), Maximum: metadataFloatPointer(38), RequiredWhen: map[string]any{"type": "currency"}, Default: 2},
			{Key: "rounding_mode", Type: "string", Enum: []string{"ceiling", "down", "floor", "half_even", "half_up", "up"}, RequiredWhen: map[string]any{"type": "currency"}, Default: "half_even"},
			{Key: "currency_code", Type: "currency_code", Format: "iso-4217", RequiredWhen: map[string]any{"type": "currency"}, Default: "XXX"},
		},
		Permissions: []string{"metadata.read", "metadata.write"}, AuditEvents: []string{"metadata_definition_upserted"},
		ValidationEndpoint: "POST /metadata/definitions/field/{resourceKey}/validate", ConfigurationRoutes: metadataConfigurationRoutes("field"),
		ResourceOperations:       metadataResourceOperations("field"),
		ResourceKeyPathParameter: "resourceKey",
		InputSchema:              metadataAuthoringRequestSchema(payload, true), OutputSchema: metadataAuthoringOutputSchema(payload),
		OutputVariables: []capabilitycontract.CapabilityAuthoringOutput{
			{Name: "field_key", JSONPointer: "/definition/payload/key", Type: "field_key", VisibleTo: "subsequent_capability_calls"},
			{Name: "schema_hash", JSONPointer: "/definition/schema_hash", Type: "schema_hash", VisibleTo: "subsequent_capability_calls"},
		},
		ReferenceContracts: []capabilitycontract.CapabilityAuthoringReference{metadataObjectReference("/object_key")},
		Execution:          metadataAuthoringExecution("schema.field"),
		Errors: []capabilitycontract.CapabilityAuthoringError{
			{Code: "backend.metadata.field_definition_invalid", FieldPath: "field", MessageKey: "backend.metadata.field_definition_invalid"},
			{Code: "backend.validation.required", FieldPath: "field", ParameterKeys: []string{"field"}, MessageKey: "backend.validation.required"},
			{Code: "backend.decimal.precision_invalid", FieldPath: "config.precision", MessageKey: "backend.decimal.precision_invalid"},
			{Code: "backend.decimal.scale_invalid", FieldPath: "config.scale", MessageKey: "backend.decimal.scale_invalid"},
			{Code: "backend.decimal.rounding_mode_invalid", FieldPath: "config.rounding_mode", MessageKey: "backend.decimal.rounding_mode_invalid"},
			{Code: "backend.decimal.currency_code_invalid", FieldPath: "config.currency_code", MessageKey: "backend.decimal.currency_code_invalid"},
		},
		Examples: []capabilitycontract.CapabilityAuthoringExample{
			{Name: "minimal_valid", Value: map[string]any{"object_key": "order", "expected_schema_hash": "$instance.schema_hash", "payload": map[string]any{"key": "status", "name": "Status", "type": "text", "required": false}}},
			{Name: "representative", Value: map[string]any{"object_key": "order", "expected_schema_hash": "$instance.schema_hash", "payload": map[string]any{"key": "amount", "name": "Amount", "type": "currency", "required": true, "config": map[string]any{"precision": 19, "scale": 2, "rounding_mode": "half_even", "currency_code": "CNY"}}}},
			{Name: "invalid_with_repair", Value: map[string]any{"object_key": "order", "expected_schema_hash": "$instance.schema_hash", "payload": map[string]any{"key": "amount", "name": "Amount", "type": "currency", "config": map[string]any{"precision": 2, "scale": 3}}}, ExpectedErrorCodes: []string{"backend.decimal.scale_invalid"}},
		},
		Sources: []capabilitycontract.CapabilityAuthoringSource{
			{Kind: "contract", Path: "runtime/domain/appschema/contract/appschema_field_authoring.go", Symbol: "ApplicationSchemaFieldAuthoringCapability"},
			{Kind: "validation", Path: "runtime/domain/appschema/validation/appschema_field_mutation_validation.go", Symbol: "ApplicationSchemaNormalizeFieldMutation"},
			{Kind: "service", Path: "runtime/application/appschema/appschema_definition_validation_application_service.go", Symbol: "ApplicationSchemaApplicationService.ValidateApplicationDefinitionPayload"},
		},
	}
}
