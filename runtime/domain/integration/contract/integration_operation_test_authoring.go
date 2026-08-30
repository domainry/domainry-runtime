package integrationcontract

import (
	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func IntegrationOperationTestAuthoringCapability() capabilitycontract.CapabilityAuthoringDefinition {
	open := true
	input := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &open}
	return capabilitycontract.CapabilityAuthoringDefinition{
		Key: "integration.operation_test", Status: "supported", Lifecycle: "explicit_confirmed_test", Requires: []string{"integration.connection", "integration.connector_operation"},
		Parameters:  []capabilitycontract.CapabilityAuthoringParameter{{Key: "operation", Type: "operation_key", Required: true}, {Key: "input", Type: "operation_input"}, {Key: "confirm", Type: "boolean", Required: true}},
		Permissions: []string{"integration.connection.test"}, AuditEvents: []string{"integration_operation_tested"}, ValidationEndpoint: "POST /integrations/connections/{connectionKey}/test-operation",
		ConfigurationRoutes: []string{"POST /integrations/connections/{connectionKey}/test-operation"}, ResourceKeyPathParameter: "connectionKey", InputSchema: integrationOperationTestInputSchema(input, nil), OutputSchema: integrationOperationTestOutputSchema(input),
		OutputVariables:    []capabilitycontract.CapabilityAuthoringOutput{{Name: "response", JSONPointer: "/response", Type: "operation_response", VisibleTo: "subsequent_capability_calls"}, {Name: "invocation", JSONPointer: "/invocation", Type: "integration_invocation", VisibleTo: "subsequent_capability_calls"}},
		ReferenceContracts: []capabilitycontract.CapabilityAuthoringReference{{Kind: "operation_key", InputJSONPointer: "/operation", ScopeFrom: "/connector_key", ResolverEndpoint: "/tenant-admin/platform-capabilities/references/operation_key"}},
		Execution:          &capabilitycontract.CapabilityAuthoringExecution{ReadSet: []string{"integration.connection", "integration.connector_definition", "integration.secret"}, WriteSet: []string{"integration.invocation"}, Transaction: "integration_test_invocation", Idempotency: "explicit_test_invocation", SideEffects: []string{"provider_test_call", "integration_operation_tested"}, SideEffectLevel: "external_confirmed", Compensation: "no_automatic_compensation_test_invocation_is_explicit_and_audited", PermissionModel: "integration.connection.test", ChangeControl: "explicit_confirm_required"},
		Errors:             []capabilitycontract.CapabilityAuthoringError{{Code: "backend.integration.operation_test_confirmation_required", FieldPath: "confirm", MessageKey: "backend.integration.operation_test_confirmation_required"}, {Code: "backend.automation.connector_operation_not_found", FieldPath: "operation", MessageKey: "backend.automation.connector_operation_not_found"}, {Code: "backend.integration.binding.protocol_value_required", FieldPath: "input", MessageKey: "backend.integration.binding.protocol_value_required"}, {Code: "backend.integration.binding.protocol_field_unknown", FieldPath: "input", MessageKey: "backend.integration.binding.protocol_field_unknown"}, {Code: "backend.integration.binding.protocol_type_mismatch", FieldPath: "input", MessageKey: "backend.integration.binding.protocol_type_mismatch"}},
		Examples:           integrationOperationTestExamples("test_connection", nil),
		Sources:            []capabilitycontract.CapabilityAuthoringSource{{Kind: "model", Path: "runtime/application/integration/integration_contracts.go", Symbol: "ConnectorOperationTestRequest"}, {Kind: "service", Path: "runtime/application/integration/integration_application_operation_test_command.go", Symbol: "IntegrationApplicationService.TestConnectorOperation"}, {Kind: "transport", Path: "runtime/transport/http/integrations/integrations_routes.go", Symbol: "RegisterRoutes"}},
	}
}

func SpecializeIntegrationOperationTestAuthoringCapability(connector integrationmodel.ConnectorSchema, operationKey string) (capabilitycontract.CapabilityAuthoringDefinition, bool) {
	for index := range connector.Operations {
		operation := connector.Operations[index]
		if operation.Key != operationKey {
			continue
		}
		input, output := integrationProtocolObjectSchema(operation.Input), integrationProtocolObjectSchema(operation.Output)
		capability := IntegrationOperationTestAuthoringCapability()
		capability.InputSchema = integrationOperationTestInputSchema(input, &operation)
		capability.OutputSchema = integrationOperationTestOutputSchema(output)
		capability.Examples = integrationOperationTestExamples(operation.Key, &operation)
		return capability, true
	}
	return capabilitycontract.CapabilityAuthoringDefinition{}, false
}

func integrationProtocolObjectSchema(fields []definitionmodel.FieldSchema) capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	result := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{}, Required: []string{}}
	for _, field := range fields {
		result.Properties[field.Key] = integrationFieldValueSchema(field)
		if field.Required {
			result.Required = append(result.Required, field.Key)
		}
	}
	return result
}

func integrationOperationTestInputSchema(input capabilitycontract.CapabilityAuthoringSchema, operation *integrationmodel.ConnectorOperationSchema) *capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	operationSchema := capabilitycontract.CapabilityAuthoringSchema{Type: "string"}
	if operation != nil {
		operationSchema.Const, operationSchema.Enum = operation.Key, []any{operation.Key}
	}
	return &capabilitycontract.CapabilityAuthoringSchema{Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed, Required: []string{"operation", "confirm"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{"operation": operationSchema, "input": input, "confirm": {Type: "boolean", Const: true}}}
}

func integrationOperationTestOutputSchema(response capabilitycontract.CapabilityAuthoringSchema) *capabilitycontract.CapabilityAuthoringSchema {
	closed, open := false, true
	return &capabilitycontract.CapabilityAuthoringSchema{Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed, Required: []string{"connection", "operation", "response", "invocation"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{"connection": {Type: "object", AdditionalProperties: &open}, "operation": {Type: "object", AdditionalProperties: &open}, "response": response, "invocation": {Type: "object", AdditionalProperties: &open}}}
}

func integrationOperationTestExamples(operationKey string, operation *integrationmodel.ConnectorOperationSchema) []capabilitycontract.CapabilityAuthoringExample {
	minimalInput, representativeInput := map[string]any{}, map[string]any{}
	if operation != nil {
		for _, field := range operation.Input {
			value := integrationExampleFieldValue(field)
			representativeInput[field.Key] = value
			if field.Required {
				minimalInput[field.Key] = value
			}
		}
	}
	minimal := map[string]any{"operation": operationKey, "confirm": true}
	if len(minimalInput) > 0 {
		minimal["input"] = minimalInput
	}
	return []capabilitycontract.CapabilityAuthoringExample{{Name: "minimal_valid", Value: minimal}, {Name: "representative", Value: map[string]any{"operation": operationKey, "input": representativeInput, "confirm": true}}, {Name: "invalid_with_repair", Value: map[string]any{"operation": operationKey, "input": minimalInput, "confirm": false}, ExpectedErrorCodes: []string{"backend.integration.operation_test_confirmation_required"}}}
}
