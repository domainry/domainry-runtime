package integrationcontract

import (
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func IntegrationBindingValidationAuthoringCapability() capabilitycontract.CapabilityAuthoringDefinition {
	open := true
	generic := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &open}
	return integrationBindingValidationCapability(generic, generic, generic, nil, nil, nil)
}

func SpecializeIntegrationBindingValidationAuthoringCapability(connector integrationmodel.ConnectorSchema, provider *integrationmodel.ConnectorProviderSchema, operation *integrationmodel.ConnectorOperationSchema) capabilitycontract.CapabilityAuthoringDefinition {
	connectionDraft := *integrationConnectionInputSchema(provider)
	input, output := capabilitycontract.CapabilityAuthoringSchema{}, capabilitycontract.CapabilityAuthoringSchema{}
	open := true
	input, output = capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &open}, capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &open}
	if operation != nil {
		input, output = integrationProtocolObjectSchema(operation.Input), integrationProtocolObjectSchema(operation.Output)
	}
	return integrationBindingValidationCapability(connectionDraft, input, output, &connector, provider, operation)
}

func integrationBindingValidationCapability(connectionDraft, input, output capabilitycontract.CapabilityAuthoringSchema, connector *integrationmodel.ConnectorSchema, provider *integrationmodel.ConnectorProviderSchema, operation *integrationmodel.ConnectorOperationSchema) capabilitycontract.CapabilityAuthoringDefinition {
	closed := false
	connectorSchema, operationSchema := capabilitycontract.CapabilityAuthoringSchema{Type: "string"}, capabilitycontract.CapabilityAuthoringSchema{Type: "string"}
	if connector != nil {
		connectorSchema.Const, connectorSchema.Enum = connector.Key, []any{connector.Key}
	}
	if operation != nil {
		operationSchema.Const, operationSchema.Enum = operation.Key, []any{operation.Key}
	}
	properties := map[string]capabilitycontract.CapabilityAuthoringSchema{
		"connector_key": connectorSchema, "operation_key": operationSchema, "connection_key": {Type: "string"}, "connection_draft": connectionDraft, "input": input, "output": output,
	}
	capability := capabilitycontract.CapabilityAuthoringDefinition{
		Key: "integration.binding_validation", Status: "supported", Lifecycle: "side_effect_free_validation", Requires: []string{"integration.connector_operation", "integration.connection"},
		Parameters:  []capabilitycontract.CapabilityAuthoringParameter{{Key: "connector_key", Type: "connector_key", Required: true}, {Key: "operation_key", Type: "operation_key"}, {Key: "connection_key", Type: "connection_key"}, {Key: "connection_draft", Type: "integration_connection_draft"}, {Key: "input", Type: "operation_input"}, {Key: "output", Type: "operation_output"}},
		Permissions: []string{"integration.catalog.view"}, ValidationEndpoint: "POST /integrations/bindings/validate", InputSchema: &capabilitycontract.CapabilityAuthoringSchema{Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed, Required: []string{"connector_key"}, Properties: properties}, OutputSchema: integrationBindingValidationOutputSchema(),
		OutputVariables:    []capabilitycontract.CapabilityAuthoringOutput{{Name: "valid", JSONPointer: "/valid", Type: "boolean", VisibleTo: "subsequent_capability_calls"}, {Name: "connection_ready", JSONPointer: "/connection_ready", Type: "boolean", VisibleTo: "subsequent_capability_calls"}},
		ReferenceContracts: []capabilitycontract.CapabilityAuthoringReference{{Kind: "connector_key", InputJSONPointer: "/connector_key", ResolverEndpoint: "/tenant-admin/platform-capabilities/references/connector_key"}, {Kind: "operation_key", InputJSONPointer: "/operation_key", ScopeFrom: "/connector_key", ResolverEndpoint: "/tenant-admin/platform-capabilities/references/operation_key"}, {Kind: "connection_key", InputJSONPointer: "/connection_key", ResolverEndpoint: "/tenant-admin/platform-capabilities/references/connection_key"}},
		Execution:          &capabilitycontract.CapabilityAuthoringExecution{ReadSet: []string{"integration.connector_definition", "integration.connection", "integration.secret"}, Transaction: "read_only_validation", Idempotency: "naturally_idempotent", SideEffectLevel: "none", PermissionModel: "integration.catalog.view"},
		Errors:             integrationBindingErrors(), Examples: integrationBindingExamples(connector, provider, operation),
		Sources: []capabilitycontract.CapabilityAuthoringSource{{Kind: "model", Path: "runtime/application/integration/integration_application_binding_validation.go", Symbol: "BindingValidationRequest"}, {Kind: "validation", Path: "runtime/application/integration/integration_application_binding_validation.go", Symbol: "IntegrationApplicationService.ValidateIntegrationBinding"}},
	}
	return capability
}

func integrationBindingValidationOutputSchema() *capabilitycontract.CapabilityAuthoringSchema {
	closed, open := false, true
	issue := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Required: []string{"section", "field_path", "error_code", "message_key", "capability_key", "contract_version"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{"section": {Type: "string"}, "field_path": {Type: "string"}, "connector_key": {Type: "string"}, "connection_key": {Type: "string"}, "operation_key": {Type: "string"}, "error_code": {Type: "string"}, "message_key": {Type: "string"}, "capability_key": {Type: "string"}, "contract_version": {Type: "string"}, "params": {Type: "object", AdditionalProperties: &open}}}
	return &capabilitycontract.CapabilityAuthoringSchema{Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed, Required: []string{"valid", "connection_ready", "connector_ready", "operation_ready", "secret_refs_ready", "errors", "contract_version"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{"valid": {Type: "boolean"}, "connection_ready": {Type: "boolean"}, "connector_ready": {Type: "boolean"}, "operation_ready": {Type: "boolean"}, "secret_refs_ready": {Type: "boolean"}, "errors": {Type: "array", Items: &issue}, "contract_version": {Type: "string"}}}
}

func integrationBindingErrors() []capabilitycontract.CapabilityAuthoringError {
	items := []struct{ code, path string }{{"backend.integration.connector.definition_not_ready", "connector_key"}, {"backend.integration.connector.adapter_not_ready", "connector_key"}, {"backend.integration.binding.operation_required", "operation_key"}, {"backend.integration.binding.operation_not_found", "operation_key"}, {"backend.integration.binding.connection_required", "connection_key"}, {"backend.integration.binding.connection_connector_mismatch", "connection_key"}, {"backend.integration.binding.secret_ref_required", "connection_draft.secret_refs"}, {"backend.integration.binding.protocol_value_required", "input"}, {"backend.integration.binding.protocol_field_unknown", "input"}, {"backend.integration.binding.protocol_type_mismatch", "input"}}
	result := make([]capabilitycontract.CapabilityAuthoringError, 0, len(items))
	for _, item := range items {
		result = append(result, capabilitycontract.CapabilityAuthoringError{Code: item.code, FieldPath: item.path, MessageKey: item.code})
	}
	return result
}

func integrationBindingExamples(connector *integrationmodel.ConnectorSchema, provider *integrationmodel.ConnectorProviderSchema, operation *integrationmodel.ConnectorOperationSchema) []capabilitycontract.CapabilityAuthoringExample {
	connectorKey, providerKey, operationKey := "example_connector", "default", ""
	if connector != nil {
		connectorKey = connector.Key
	}
	if provider != nil {
		providerKey = provider.Key
	}
	input, output := map[string]any{}, map[string]any{}
	if operation != nil {
		operationKey = operation.Key
		for _, field := range operation.Input {
			if field.Required {
				input[field.Key] = integrationExampleFieldValue(field)
			}
		}
		for _, field := range operation.Output {
			if field.Required {
				output[field.Key] = integrationExampleFieldValue(field)
			}
		}
	}
	minimal := map[string]any{"connector_key": connectorKey, "connection_key": connectorKey + "_primary"}
	representative := map[string]any{"connector_key": connectorKey, "connection_key": connectorKey + "_primary"}
	invalid := map[string]any{"connector_key": connectorKey, "operation_key": operationKey, "connection_key": connectorKey + "_primary", "input": map[string]any{"unknown": true}}
	if operationKey != "" {
		representative["operation_key"], representative["input"], representative["output"] = operationKey, input, output
	}
	_ = providerKey
	return []capabilitycontract.CapabilityAuthoringExample{{Name: "minimal_valid", Value: minimal}, {Name: "representative", Value: representative}, {Name: "invalid_with_repair", Value: invalid, ExpectedErrorCodes: []string{"backend.integration.binding.protocol_field_unknown"}}}
}
