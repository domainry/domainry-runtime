package contract

import capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"

func ApplicationSchemaObjectAuthoringCapability() capabilitycontract.CapabilityAuthoringDefinition {
	payload := metadataObjectPayloadSchema()
	return capabilitycontract.CapabilityAuthoringDefinition{
		Key: "schema.object", Status: "supported", Lifecycle: "versioned_metadata",
		SystemDraftResourceType: "object",
		Parameters: []capabilitycontract.CapabilityAuthoringParameter{
			{Key: "name", Type: "string", Required: true, MinLength: metadataIntPointer(1)},
			{Key: "write_policy", Type: "string", Enum: []string{"direct_crud", "action_only"}},
		},
		Permissions: []string{"runtime.appschema.validate_application_definition"}, AuditEvents: []string{"metadata_definition_upserted"},
		ValidationEndpoint: "POST /tenant-admin/metadata/definitions/object/{resourceKey}/validate", ConfigurationRoutes: metadataConfigurationRoutes("object"),
		ResourceOperations:       metadataResourceOperations("object"),
		ResourceKeyPathParameter: "resourceKey",
		InputSchema:              metadataAuthoringRequestSchema(payload, false), OutputSchema: metadataAuthoringOutputSchema(payload),
		OutputVariables: []capabilitycontract.CapabilityAuthoringOutput{
			{Name: "resource_key", JSONPointer: "/definition/resource_key", Type: "object_key", VisibleTo: "subsequent_capability_calls"},
			{Name: "schema_hash", JSONPointer: "/definition/schema_hash", Type: "schema_hash", VisibleTo: "subsequent_capability_calls"},
		},
		Execution: metadataAuthoringExecution("schema.object"),
		Errors: []capabilitycontract.CapabilityAuthoringError{
			{Code: "backend.metadata.object_definition_invalid", FieldPath: "payload", MessageKey: "backend.metadata.object_definition_invalid"},
			{Code: "backend.metadata.object_key_mismatch", FieldPath: "payload.key", ParameterKeys: []string{"object"}, MessageKey: "backend.metadata.object_key_mismatch"},
			{Code: "backend.metadata.object_name_required", FieldPath: "payload.name", ParameterKeys: []string{"object"}, MessageKey: "backend.metadata.object_name_required"},
			{Code: "backend.metadata.object_shell_only", FieldPath: "payload.fields", ParameterKeys: []string{"object"}, MessageKey: "backend.metadata.object_shell_only"},
			{Code: "backend.metadata.object_write_policy_invalid", FieldPath: "payload.config.write_policy", ParameterKeys: []string{"object"}, MessageKey: "backend.metadata.object_write_policy_invalid"},
			{Code: "backend.metadata.object_lifecycle_policy_invalid", FieldPath: "payload.lifecycle_policy", ParameterKeys: []string{"object"}, MessageKey: "backend.metadata.object_lifecycle_policy_invalid"},
			{Code: "backend.metadata.object_ledger_policy_invalid", FieldPath: "payload.ledger_policy", ParameterKeys: []string{"object"}, MessageKey: "backend.metadata.object_ledger_policy_invalid"},
			{Code: "backend.metadata.object_export_assurance_policy_invalid", FieldPath: "payload.export_assurance_policy", ParameterKeys: []string{"object"}, MessageKey: "backend.metadata.object_export_assurance_policy_invalid"},
		},
		Examples: []capabilitycontract.CapabilityAuthoringExample{
			{Name: "minimal_valid", Value: map[string]any{"expected_schema_hash": "$instance.schema_hash", "payload": map[string]any{"name": "Order", "fields": []any{}, "config": map[string]any{"write_policy": "direct_crud"}}}},
			{Name: "representative", Value: map[string]any{"expected_schema_hash": "$instance.schema_hash", "payload": map[string]any{"name": "Order", "description": "Customer order", "fields": []any{}, "config": map[string]any{"write_policy": "action_only"}, "lifecycle_policy": map[string]any{"mode": "mutable"}, "ux": map[string]any{"display": map[string]any{"title_field": "number"}}}}},
			{Name: "invalid_with_repair", Value: map[string]any{"expected_schema_hash": "$instance.schema_hash", "payload": map[string]any{"key": "invoice", "name": "Order"}}, ExpectedErrorCodes: []string{"backend.metadata.object_key_mismatch"}},
		},
		Sources: []capabilitycontract.CapabilityAuthoringSource{
			{Kind: "contract", Path: "runtime/domain/appschema/contract/appschema_object_authoring.go", Symbol: "ApplicationSchemaObjectAuthoringCapability"},
			{Kind: "validation", Path: "runtime/domain/appschema/validation/appschema_object_validation.go", Symbol: "ApplicationSchemaValidateObjectDefinition"},
			{Kind: "service", Path: "runtime/application/appschema/appschema_definition_validation_application_service.go", Symbol: "ApplicationSchemaApplicationService.ValidateApplicationDefinitionPayload"},
		},
	}
}
