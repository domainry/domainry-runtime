package contract

import capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"

func ApplicationSchemaDictionaryAuthoringCapabilities() []capabilitycontract.CapabilityAuthoringDefinition {
	payload := metadataDictionaryPayloadSchema()
	return []capabilitycontract.CapabilityAuthoringDefinition{{
		Key: "schema.dictionary", Status: "supported", Lifecycle: "versioned_metadata",
		SystemDraftResourceType: "dictionary",
		Parameters: []capabilitycontract.CapabilityAuthoringParameter{
			{Key: "name", Type: "string"}, {Key: "description", Type: "string"},
			{Key: "items", Type: "array", ItemSchema: "dictionary_item"}, {Key: "config", Type: "object"},
		},
		Permissions: []string{"runtime.appschema.validate_application_definition"}, AuditEvents: []string{"metadata_definition_upserted", "metadata_definition_deleted"},
		ValidationEndpoint: "POST /metadata/definitions/dictionary/{resourceKey}/validate", ConfigurationRoutes: metadataConfigurationRoutes("dictionary"), ResourceOperations: metadataResourceOperations("dictionary"),
		ResourceKeyPathParameter: "resourceKey",
		InputSchema:              metadataAuthoringRequestSchema(payload, false), OutputSchema: metadataAuthoringOutputSchema(payload),
		OutputVariables: []capabilitycontract.CapabilityAuthoringOutput{{Name: "dictionary_key", JSONPointer: "/definition/resource_key", Type: "dictionary_key", VisibleTo: "subsequent_capability_calls"}, {Name: "schema_hash", JSONPointer: "/definition/schema_hash", Type: "schema_hash", VisibleTo: "subsequent_capability_calls"}},
		Execution:       metadataAuthoringExecution("schema.dictionary"),
		Errors: []capabilitycontract.CapabilityAuthoringError{
			{Code: "backend.dictionary.definition_invalid", FieldPath: "dictionary", MessageKey: "backend.dictionary.definition_invalid"},
			{Code: "backend.dictionary.key_mismatch", FieldPath: "key", ParameterKeys: []string{"dictionary"}, MessageKey: "backend.dictionary.key_mismatch"},
			{Code: "backend.dictionary.item_key_value_required", FieldPath: "items[]", ParameterKeys: []string{"dictionary"}, MessageKey: "backend.dictionary.item_key_value_required"},
			{Code: "backend.dictionary.item_key_exists", FieldPath: "items[].key", ParameterKeys: []string{"item"}, MessageKey: "backend.dictionary.item_key_exists"},
			{Code: "backend.dictionary.item_value_exists", FieldPath: "items[].value", ParameterKeys: []string{"value"}, MessageKey: "backend.dictionary.item_value_exists"},
			{Code: "backend.dictionary.item_parent_self", FieldPath: "items[].parent_key", ParameterKeys: []string{"item"}, MessageKey: "backend.dictionary.item_parent_self"},
			{Code: "backend.dictionary.item_parent_not_found", FieldPath: "items[].parent_key", ParameterKeys: []string{"parent_key"}, MessageKey: "backend.dictionary.item_parent_not_found"},
			{Code: "backend.dictionary.item_parent_cycle", FieldPath: "items[].parent_key", ParameterKeys: []string{"item"}, MessageKey: "backend.dictionary.item_parent_cycle"},
		},
		Examples: []capabilitycontract.CapabilityAuthoringExample{
			{Name: "minimal_valid", Value: map[string]any{"expected_schema_hash": "$instance.schema_hash", "payload": map[string]any{"items": []any{map[string]any{"key": "draft", "value": "draft"}}}}},
			{Name: "representative", Value: map[string]any{"expected_schema_hash": "$instance.schema_hash", "payload": map[string]any{"name": "Order Status", "items": []any{map[string]any{"key": "draft", "label": "Draft", "value": "draft", "sort_order": 10, "status": "active"}, map[string]any{"key": "confirmed", "label": "Confirmed", "value": "confirmed", "sort_order": 20, "status": "active"}}}}},
			{Name: "invalid_with_repair", Value: map[string]any{"expected_schema_hash": "$instance.schema_hash", "payload": map[string]any{"key": "order_status", "items": []any{map[string]any{"key": "draft"}}}}, ExpectedErrorCodes: []string{"backend.dictionary.item_key_value_required"}},
		},
		Sources: []capabilitycontract.CapabilityAuthoringSource{
			{Kind: "contract", Path: "runtime/domain/appschema/contract/appschema_dictionary_authoring.go", Symbol: "ApplicationSchemaDictionaryAuthoringCapabilities"},
			{Kind: "validation", Path: "runtime/domain/appschema/validation/appschema_dictionary_validation.go", Symbol: "ApplicationSchemaValidateDictionaryDefinition"},
			{Kind: "service", Path: "runtime/application/appschema/appschema_definition_validation_application_service.go", Symbol: "ApplicationSchemaApplicationService.ValidateApplicationDefinitionPayload"},
		},
	}}
}
