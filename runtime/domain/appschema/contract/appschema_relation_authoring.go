package contract

import capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"

func ApplicationSchemaRelationAuthoringCapability() capabilitycontract.CapabilityAuthoringDefinition {
	payload := metadataRelationPayloadSchema()
	return capabilitycontract.CapabilityAuthoringDefinition{
		Key: "schema.relation", Status: "supported", Lifecycle: "versioned_metadata", Requires: []string{"schema.field"},
		SystemDraftResourceType: "field",
		Parameters: []capabilitycontract.CapabilityAuthoringParameter{
			{Key: "target", Type: "object_key", Required: true}, {Key: "cardinality", Type: "string", Default: "many_to_one", Enum: []string{"many_to_one", "one_to_one"}},
			{Key: "on_delete", Type: "string", Default: "restrict", Enum: []string{"cascade", "restrict", "set_null"}},
			{Key: "inverse_name", Type: "string"}, {Key: "indexed", Type: "boolean", Default: true},
		},
		Permissions: []string{"metadata.read", "metadata.write"}, ValidationEndpoint: "POST /metadata/definitions/field/{resourceKey}/validate",
		ConfigurationRoutes: metadataConfigurationRoutes("field"), ResourceOperations: metadataResourceOperations("field"),
		ResourceKeyPathParameter: "resourceKey",
		InputSchema:              metadataAuthoringRequestSchema(payload, true), OutputSchema: metadataAuthoringOutputSchema(payload),
		OutputVariables: []capabilitycontract.CapabilityAuthoringOutput{
			{Name: "field_key", JSONPointer: "/definition/payload/key", Type: "field_key", VisibleTo: "subsequent_capability_calls"},
			{Name: "schema_hash", JSONPointer: "/definition/schema_hash", Type: "schema_hash", VisibleTo: "subsequent_capability_calls"},
		},
		ReferenceContracts: []capabilitycontract.CapabilityAuthoringReference{metadataObjectReference("/object_key"), metadataRelationTargetReference("/payload/config/target")},
		Execution:          metadataAuthoringExecution("schema.relation"),
		Errors: []capabilitycontract.CapabilityAuthoringError{
			{Code: "backend.metadata.relation_target_required", FieldPath: "validation.target", ParameterKeys: []string{"object", "field"}, MessageKey: "backend.metadata.relation_target_required"},
			{Code: "backend.metadata.relation_cardinality_invalid", FieldPath: "config.cardinality", ParameterKeys: []string{"cardinality"}, MessageKey: "backend.metadata.relation_cardinality_invalid"},
			{Code: "backend.metadata.relation_on_delete_invalid", FieldPath: "config.on_delete", ParameterKeys: []string{"on_delete"}, MessageKey: "backend.metadata.relation_on_delete_invalid"},
		},
		Examples: []capabilitycontract.CapabilityAuthoringExample{
			{Name: "minimal_valid", Value: map[string]any{"object_key": "order", "expected_schema_hash": "$instance.schema_hash", "payload": map[string]any{"key": "customer_id", "name": "Customer", "type": "relation", "config": map[string]any{"target": "customer"}}}},
			{Name: "representative", Value: map[string]any{"object_key": "order", "expected_schema_hash": "$instance.schema_hash", "payload": map[string]any{"key": "customer_id", "name": "Customer", "type": "relation", "required": true, "config": map[string]any{"target": "customer", "cardinality": "many_to_one", "on_delete": "restrict", "inverse_name": "orders", "indexed": true}}}},
			{Name: "invalid_with_repair", Value: map[string]any{"object_key": "order", "expected_schema_hash": "$instance.schema_hash", "payload": map[string]any{"key": "customer_id", "name": "Customer", "type": "relation", "config": map[string]any{}}}, ExpectedErrorCodes: []string{"backend.metadata.relation_target_required"}},
		},
		Sources: []capabilitycontract.CapabilityAuthoringSource{
			{Kind: "contract", Path: "runtime/domain/appschema/contract/appschema_relation_authoring.go", Symbol: "ApplicationSchemaRelationAuthoringCapability"},
			{Kind: "validation", Path: "runtime/domain/appschema/validation/appschema_field_mutation_validation.go", Symbol: "ApplicationSchemaNormalizeFieldMutation"},
			{Kind: "service", Path: "runtime/application/appschema/appschema_definition_validation_application_service.go", Symbol: "ApplicationSchemaApplicationService.ValidateApplicationDefinitionPayload"},
		},
	}
}
