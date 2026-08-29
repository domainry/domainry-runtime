package integrationcontract

import (
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	metadatacontract "github.com/domainry/domainry-runtime/runtime/domain/metadata/contract"
)

func RuntimeConnectorTypes() []string   { return integrationmodel.RuntimeConnectorTypes() }
func RuntimeConnectorMethods() []string { return integrationmodel.RuntimeConnectorMethods() }
func RuntimeConnectorExecutionModes() []string {
	return integrationmodel.RuntimeConnectorExecutionModes()
}
func RuntimeConnectorSideEffects() []string { return integrationmodel.RuntimeConnectorSideEffects() }
func RuntimeConnectorProtocolFieldTypes() []string {
	return integrationmodel.RuntimeConnectorProtocolFieldTypes()
}

func IntegrationConnectorDefinitionAuthoringCapability() capabilitycontract.CapabilityAuthoringDefinition {
	payload := integrationConnectorSchema()
	return capabilitycontract.CapabilityAuthoringDefinition{
		Key: "integration.connector_definition", Status: "supported", Lifecycle: "versioned_metadata",
		SystemDraftResourceType: "connector",
		Parameters: []capabilitycontract.CapabilityAuthoringParameter{
			{Key: "key", Type: "connector_key", Required: true}, {Key: "type", Type: "string", Required: true, Enum: RuntimeConnectorTypes()}, {Key: "provider", Type: "string", Required: true},
			{Key: "providers", Type: "array", ItemSchema: "connector_provider"}, {Key: "operations", Type: "array", Required: true, ItemSchema: "connector_operation"},
			{Key: "required", Type: "boolean"}, {Key: "config_fields", Type: "array", ItemSchema: "field_key"}, {Key: "secret_refs", Type: "array", ItemSchema: "secret_ref_name"}, {Key: "expected_schema_hash", Type: "schema_hash", Required: true},
		},
		Permissions: []string{"workspace.admin"}, AuditEvents: []string{"metadata_definition_upserted"}, ValidationEndpoint: "POST /metadata/definitions/connector/{resourceKey}/validate",
		ConfigurationRoutes: integrationConnectorConfigurationRoutes(), ResourceKeyPathParameter: "resourceKey", FrontendSupportKey: "integration.connector-definition.v1",
		ResourceOperations: integrationConnectorResourceOperations(),
		InputSchema:        integrationMetadataRequestSchema(payload), OutputSchema: integrationMetadataOutputSchema(payload),
		OutputVariables: []capabilitycontract.CapabilityAuthoringOutput{{Name: "connector_key", JSONPointer: "/definition/resource_key", Type: "connector_key", VisibleTo: "subsequent_capability_calls"}, {Name: "schema_hash", JSONPointer: "/definition/schema_hash", Type: "schema_hash", VisibleTo: "subsequent_capability_calls"}},
		Execution:       metadatacontract.VersionedMetadataDefinitionExecution("integration.connector_definition"),
		Errors:          integrationConnectorAuthoringErrors(), Examples: integrationConnectorDefinitionExamples(),
		Sources: []capabilitycontract.CapabilityAuthoringSource{{Kind: "model", Path: "runtime/domain/integration/model/integration_definition.go", Symbol: "ConnectorSchema"}, {Kind: "validation", Path: "runtime/domain/metadata/validation/metadata_connector_validation.go", Symbol: "MetadataValidateConnectorDefinitionIssues"}, {Kind: "service", Path: "runtime/application/metadata/metadata_definition_orchestration_application_service.go", Symbol: "ApplicationSchemaService.UpsertMetadataDefinition"}},
	}
}

func integrationConnectorResourceOperations() *capabilitycontract.CapabilityAuthoringResourceOperations {
	return nil
}

func IntegrationConnectorOperationAuthoringCapability() capabilitycontract.CapabilityAuthoringDefinition {
	parameters := []capabilitycontract.CapabilityAuthoringParameter{
		{Key: "key", Type: "operation_key", Required: true}, {Key: "method", Type: "string", Required: true, Enum: RuntimeConnectorMethods()}, {Key: "execution_mode", Type: "string", Required: true, Enum: RuntimeConnectorExecutionModes()},
		{Key: "side_effect", Type: "string", Required: true, Enum: RuntimeConnectorSideEffects()}, {Key: "protocol_field_type", Type: "string", Enum: RuntimeConnectorProtocolFieldTypes(), ReadOnly: true}, {Key: "input", Type: "array", ItemSchema: "protocol_field"}, {Key: "output", Type: "array", ItemSchema: "protocol_field"},
		{Key: "timeout_default_seconds", Type: "integer", Required: true, Default: 10, Minimum: integrationFloatPointer(1)}, {Key: "timeout_max_seconds", Type: "integer", Required: true, Default: 30, Minimum: integrationFloatPointer(1)},
		{Key: "idempotency_supported", Type: "boolean"}, {Key: "compensation_operation", Type: "operation_key"}, {Key: "test_supported", Type: "boolean"}, {Key: "dry_run_supported", Type: "boolean"},
	}
	return capabilitycontract.CapabilityAuthoringDefinition{
		Key: "integration.connector_operation", Status: "supported", Lifecycle: "definition_fragment", Requires: []string{"integration.connector_definition"}, Permissions: []string{"workspace.admin"},
		Parameters: parameters, ValidationEndpoint: "POST /metadata/definitions/connector/{resourceKey}/validate", FrontendSupportKey: "integration.connector-operation.v1",
		InputSchema: integrationConnectorOperationSchema(), OutputSchema: integrationValidationOutputSchema(),
		OutputVariables:    []capabilitycontract.CapabilityAuthoringOutput{{Name: "valid", JSONPointer: "/valid", Type: "boolean", VisibleTo: "subsequent_capability_calls"}},
		ReferenceContracts: []capabilitycontract.CapabilityAuthoringReference{{Kind: "operation_key", InputJSONPointer: "/compensation_operation", ScopeFrom: "/connector_key", ResolverEndpoint: "/tenant-admin/platform-capabilities/references/operation_key"}},
		Execution:          &capabilitycontract.CapabilityAuthoringExecution{ReadSet: []string{"integration.connector_definition"}, Transaction: "read_only_validation", Idempotency: "naturally_idempotent", SideEffectLevel: "none", PermissionModel: "workspace.admin"},
		Errors:             integrationConnectorAuthoringErrors(), Examples: integrationConnectorOperationExamples(),
		Sources: []capabilitycontract.CapabilityAuthoringSource{{Kind: "model", Path: "runtime/domain/integration/model/integration_definition.go", Symbol: "ConnectorOperationSchema"}, {Kind: "validation", Path: "runtime/domain/metadata/validation/metadata_connector_validation.go", Symbol: "MetadataValidateConnectorDefinitionIssues"}},
	}
}

func integrationConnectorSchema() capabilitycontract.CapabilityAuthoringSchema {
	closed, open := false, true
	operation := *integrationConnectorOperationSchema()
	field := integrationProtocolFieldSchema()
	provider := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"key"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"key": {Type: "string"}, "name": {Type: "string"}, "description": {Type: "string"}, "i18n": {Type: "object", AdditionalProperties: &open},
		"config_fields": {Type: "array", Items: &field}, "secret_fields": {Type: "array", Items: &field},
		"operation_keys": {Type: "array", Items: &capabilitycontract.CapabilityAuthoringSchema{Type: "string"}}, "readiness": {Type: "string"},
	}}
	return capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"key", "type", "provider", "operations"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"key": {Type: "string"}, "type": {Type: "string", Enum: integrationAnyEnums(RuntimeConnectorTypes())}, "provider": {Type: "string"}, "version": {Type: "string"}, "minimum_runtime_version": {Type: "string"},
		"feature_flags": integrationStringArraySchema(), "source": {Type: "string"}, "classification": {Type: "string"}, "lifecycle_status": {Type: "string"}, "replacement_capability": {Type: "string"}, "name": {Type: "string"}, "description": {Type: "string"}, "i18n": {Type: "object", AdditionalProperties: &open},
		"capabilities": integrationStringArraySchema(), "providers": {Type: "array", Items: &provider}, "operations": {Type: "array", MinItems: integrationIntPointer(1), Items: &operation},
		"required": {Type: "boolean"}, "config_fields": integrationStringArraySchema(), "secret_refs": integrationStringArraySchema(), "readiness": {Type: "string"}, "definition_ready": {Type: "boolean"}, "adapter_ready": {Type: "boolean"}, "connection_ready": {Type: "boolean"}, "config": {Type: "object", AdditionalProperties: &open},
	}}
}

func integrationConnectorOperationSchema() *capabilitycontract.CapabilityAuthoringSchema {
	closed, open := false, true
	field := integrationProtocolFieldSchema()
	return &capabilitycontract.CapabilityAuthoringSchema{Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed, Required: []string{"key", "method", "execution_mode", "side_effect", "timeout_default_seconds", "timeout_max_seconds"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"key": {Type: "string"}, "name": {Type: "string"}, "description": {Type: "string"}, "i18n": {Type: "object", AdditionalProperties: &open}, "method": {Type: "string", Enum: integrationAnyEnums(RuntimeConnectorMethods())},
		"execution_mode": {Type: "string", Enum: integrationAnyEnums(RuntimeConnectorExecutionModes())}, "side_effect": {Type: "string", Enum: integrationAnyEnums(RuntimeConnectorSideEffects())},
		"input": {Type: "array", Items: &field}, "output": {Type: "array", Items: &field}, "timeout_default_seconds": {Type: "integer", Minimum: integrationFloatPointer(1)}, "timeout_max_seconds": {Type: "integer", Minimum: integrationFloatPointer(1)},
		"idempotency_supported": {Type: "boolean"}, "compensation_operation": {Type: "string"}, "test_supported": {Type: "boolean"}, "dry_run_supported": {Type: "boolean"},
	}, Definitions: map[string]capabilitycontract.CapabilityAuthoringSchema{"protocol_field": field}}
}

func integrationProtocolFieldSchema() capabilitycontract.CapabilityAuthoringSchema {
	closed, open := false, true
	validation := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{"min_length": {Type: "integer"}, "max_length": {Type: "integer"}, "min": {Type: "number"}, "max": {Type: "number"}, "pattern": {Type: "string"}, "options": integrationStringArraySchema(), "target": {Type: "string"}}}
	return capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"key", "name", "type", "required"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"key": {Type: "string"}, "name": {Type: "string"}, "description": {Type: "string"}, "type": {Type: "string", Enum: integrationAnyEnums(RuntimeConnectorProtocolFieldTypes())}, "i18n": {Type: "object", AdditionalProperties: &open}, "config": {Type: "object", AdditionalProperties: &open}, "validation": validation,
		"options": {}, "required": {Type: "boolean"}, "unique": {Type: "boolean"}, "default": {}, "default_value": {}, "disabled_at": {Type: "string"},
	}}
}

func integrationMetadataRequestSchema(payload capabilitycontract.CapabilityAuthoringSchema) *capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	return &capabilitycontract.CapabilityAuthoringSchema{Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed, Required: []string{"expected_schema_hash", "payload"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{"object_key": {Type: "string"}, "name": {Type: "string"}, "source_kind": {Type: "string"}, "source_id": {Type: "string"}, "expected_schema_hash": {Type: "string"}, "payload": payload}}
}

func integrationMetadataOutputSchema(payload capabilitycontract.CapabilityAuthoringSchema) *capabilitycontract.CapabilityAuthoringSchema {
	closed, open := false, true
	definition := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"payload", "resource_key", "resource_type", "schema_hash"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{"resource_type": {Type: "string", Const: "connector"}, "resource_key": {Type: "string"}, "object_key": {Type: "string"}, "name": {Type: "string"}, "payload": payload, "schema_version": {Type: "string"}, "schema_hash": {Type: "string"}, "source_kind": {Type: "string"}, "source_id": {Type: "string"}, "disabled_at": {Type: "string"}, "created_at": {Type: "string"}, "updated_at": {Type: "string"}}}
	return &capabilitycontract.CapabilityAuthoringSchema{Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed, Required: []string{"definition", "resource_hash", "schema", "snapshot_hash"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{"definition": definition, "resource_hash": {Type: "string"}, "snapshot_hash": {Type: "string"}, "schema": {Type: "object", AdditionalProperties: &open}}}
}

func integrationValidationOutputSchema() *capabilitycontract.CapabilityAuthoringSchema {
	closed, open := false, true
	return &capabilitycontract.CapabilityAuthoringSchema{Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed, Required: []string{"valid"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{"valid": {Type: "boolean"}, "errors": {Type: "array", Items: &capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &open}}}}
}

func integrationConnectorAuthoringErrors() []capabilitycontract.CapabilityAuthoringError {
	codes := []string{"backend.integration.connector.type_invalid", "backend.integration.connector.identity_required", "backend.integration.connector.operation_required", "backend.integration.connector.required_secret_ref_unknown", "backend.integration.connector.legacy_provider_config_forbidden", "backend.integration.connector.operation_key_invalid", "backend.integration.connector.operation_execution_mode_invalid", "backend.integration.connector.operation_method_invalid", "backend.integration.connector.operation_side_effect_invalid", "backend.integration.connector.operation_timeout_invalid", "backend.integration.connector.protocol_field_key_invalid", "backend.integration.connector.protocol_field_type_invalid", "backend.integration.connector.compensation_operation_not_found", "backend.integration.connector.reserve_contract_incomplete"}
	result := make([]capabilitycontract.CapabilityAuthoringError, 0, len(codes))
	for _, code := range codes {
		path := "operations[]"
		switch code {
		case "backend.integration.connector.type_invalid":
			path = "type"
		case "backend.integration.connector.identity_required":
			path = "key"
		case "backend.integration.connector.operation_required":
			path = "operations"
		case "backend.integration.connector.required_secret_ref_unknown":
			path = "config.required_secret_refs[]"
		case "backend.integration.connector.legacy_provider_config_forbidden":
			path = "config"
		}
		result = append(result, capabilitycontract.CapabilityAuthoringError{Code: code, FieldPath: path, MessageKey: code})
	}
	return result
}

func integrationConnectorDefinitionExamples() []capabilitycontract.CapabilityAuthoringExample {
	minimalOperation := map[string]any{"key": "get_order", "method": "GET", "execution_mode": "sync", "side_effect": "read", "timeout_default_seconds": 10, "timeout_max_seconds": 30}
	return []capabilitycontract.CapabilityAuthoringExample{
		{Name: "minimal_valid", Value: map[string]any{"expected_schema_hash": "$instance.schema_hash", "payload": map[string]any{"key": "order_api", "type": "http", "provider": "default", "operations": []any{minimalOperation}}}},
		{Name: "representative", Value: map[string]any{"expected_schema_hash": "$instance.schema_hash", "source_kind": "builder_v4", "source_id": "$builder_task_id", "payload": map[string]any{"key": "inventory_api", "type": "http", "provider": "default", "name": "Inventory API", "operations": []any{map[string]any{"key": "reserve", "method": "POST", "execution_mode": "sync", "side_effect": "reserve", "timeout_default_seconds": 10, "timeout_max_seconds": 30, "idempotency_supported": true, "compensation_operation": "release"}, map[string]any{"key": "release", "method": "DELETE", "execution_mode": "sync", "side_effect": "write", "timeout_default_seconds": 10, "timeout_max_seconds": 30}}, "required": true}}},
		{Name: "invalid_with_repair", Value: map[string]any{"expected_schema_hash": "$instance.schema_hash", "payload": map[string]any{"key": "invalid_api", "type": "http", "provider": "default", "operations": []any{map[string]any{"key": "get", "method": "FETCH", "execution_mode": "sync", "side_effect": "read", "timeout_default_seconds": 10, "timeout_max_seconds": 30}}}}, ExpectedErrorCodes: []string{"backend.integration.connector.operation_method_invalid"}},
	}
}

func integrationConnectorOperationExamples() []capabilitycontract.CapabilityAuthoringExample {
	return []capabilitycontract.CapabilityAuthoringExample{
		{Name: "minimal_valid", Value: map[string]any{"key": "get_order", "method": "GET", "execution_mode": "sync", "side_effect": "read", "timeout_default_seconds": 10, "timeout_max_seconds": 30}},
		{Name: "representative", Value: map[string]any{"key": "create_order", "method": "POST", "execution_mode": "sync", "side_effect": "write", "input": []any{map[string]any{"key": "customer_id", "name": "Customer ID", "type": "text", "required": true}}, "output": []any{map[string]any{"key": "external_id", "name": "External ID", "type": "text", "required": true}}, "timeout_default_seconds": 10, "timeout_max_seconds": 30, "idempotency_supported": true, "test_supported": true}},
		{Name: "invalid_with_repair", Value: map[string]any{"key": "invalid", "method": "FETCH", "execution_mode": "sync", "side_effect": "read", "timeout_default_seconds": 10, "timeout_max_seconds": 30}, ExpectedErrorCodes: []string{"backend.integration.connector.operation_method_invalid"}},
	}
}

func integrationConnectorConfigurationRoutes() []string {
	return metadatacontract.VersionedMetadataDefinitionRoutes("connector")
}
func integrationStringArraySchema() capabilitycontract.CapabilityAuthoringSchema {
	return capabilitycontract.CapabilityAuthoringSchema{Type: "array", Items: &capabilitycontract.CapabilityAuthoringSchema{Type: "string"}}
}
func integrationAnyEnums(values []string) []any {
	result := make([]any, len(values))
	for index, value := range values {
		result[index] = value
	}
	return result
}
func integrationFloatPointer(value float64) *float64 { return &value }
func integrationIntPointer(value int) *int           { return &value }
