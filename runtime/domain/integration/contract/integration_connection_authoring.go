package integrationcontract

import (
	"fmt"
	"strings"

	capabilitycontract "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func IntegrationConnectionAuthoringCapability() capabilitycontract.CapabilityAuthoringDefinition {
	capability := integrationConnectionMutationCapability("integration.connection", "immediate_audited_configuration", "PUT /integrations/connections/{connectionKey}", "integration_connection_upserted", true)
	capability.ValidationEndpoint = "POST /integrations/connections/{connectionKey}/validate"
	capability.ConfigurationRoutes = append(capability.ConfigurationRoutes, "GET /integrations/connections/{connectionKey}", "GET /integrations/connections/{connectionKey}/versions", "DELETE /integrations/connections/{connectionKey}")
	capability.ResourceOperations = &capabilitycontract.CapabilityAuthoringResourceOperations{
		PersistenceMode: "audited_resource",
		Validate:        capability.ValidationEndpoint,
		Upsert:          "PUT /integrations/connections/{connectionKey}",
		UpsertHeaders:   capabilitycontract.DirectAuthoringUpsertHeaders(),
		SuccessSchema:   capabilitycontract.DirectAuthoringSuccessSchema(),
		Get:             "GET /integrations/connections/{connectionKey}",
		Versions:        "GET /integrations/connections/{connectionKey}/versions",
		Delete:          "DELETE /integrations/connections/{connectionKey}",
	}
	return capability
}

func IntegrationConnectionRotateAuthoringCapability() capabilitycontract.CapabilityAuthoringDefinition {
	capability := integrationConnectionMutationCapability("integration.connection.rotate", "explicit_connection_rotation", "POST /integrations/connections/{connectionKey}/rotate", "integration_connection_rotated", true)
	capability.Requires = []string{"integration.connection"}
	return capability
}

func IntegrationConnectionDisableAuthoringCapability() capabilitycontract.CapabilityAuthoringDefinition {
	return integrationConnectionCommandCapability("integration.connection.disable", "POST /integrations/connections/{connectionKey}/disable", "integration_connection_disabled", "runtime/application/integration/integration_application_connection_resolution.go", "IntegrationApplicationService.DisableIntegrationConnection")
}

func IntegrationConnectionDeleteAuthoringCapability() capabilitycontract.CapabilityAuthoringDefinition {
	capability := integrationConnectionCommandCapability("integration.connection.delete", "DELETE /integrations/connections/{connectionKey}", "integration_connection_deleted", "runtime/application/integration/integration_application_connection_delete.go", "IntegrationApplicationService.DeleteIntegrationConnection")
	capability.OutputSchema = integrationEmptyOutputSchema()
	return capability
}

func integrationConnectionMutationCapability(key, lifecycle, route, auditEvent string, providerSelection bool) capabilitycontract.CapabilityAuthoringDefinition {
	parameters := []capabilitycontract.CapabilityAuthoringParameter{
		{Key: "key", Type: "connection_key"}, {Key: "connector_key", Type: "connector_key", Required: true}, {Key: "provider_key", Type: "provider_key", Required: true}, {Key: "name", Type: "string"},
		{Key: "status", Type: "string", Enum: integrationmodel.RuntimeIntegrationConnectionStatuses(), Default: "configured"}, {Key: "config", Type: "provider_config"}, {Key: "secret_refs", Type: "provider_secret_refs"},
	}
	capability := capabilitycontract.CapabilityAuthoringDefinition{
		Key: key, Status: "supported", Lifecycle: lifecycle, Parameters: parameters, Requires: []string{"integration.connector_definition"}, Permissions: []string{"integration.connection.manage"},
		AuditEvents: []string{auditEvent}, ValidationEndpoint: "POST /integrations/bindings/validate", ConfigurationRoutes: []string{route}, ResourceKeyPathParameter: "connectionKey", InputSchema: integrationConnectionInputSchema(nil), OutputSchema: integrationConnectionOutputSchema(),
		OutputVariables:    []capabilitycontract.CapabilityAuthoringOutput{{Name: "connection_key", JSONPointer: "/key", Type: "connection_key", VisibleTo: "subsequent_capability_calls"}},
		ReferenceContracts: []capabilitycontract.CapabilityAuthoringReference{{Kind: "connector_key", InputJSONPointer: "/connector_key", ResolverEndpoint: "/tenant-admin/platform-capabilities/references/connector_key"}, {Kind: "provider_key", InputJSONPointer: "/provider_key", ScopeFrom: "/connector_key", ResolverEndpoint: "/tenant-admin/platform-capabilities/references/provider_key"}},
		Execution:          &capabilitycontract.CapabilityAuthoringExecution{ReadSet: []string{"integration.connector_definition", "integration.secret"}, WriteSet: []string{"integration.connection"}, Transaction: "integration_connection_transaction", Idempotency: "connection_key", SideEffects: []string{auditEvent}, SideEffectLevel: "internal", PermissionModel: "integration.connection.manage", ChangeControl: "direct_on_configuring_runtime_change_plan_on_existing_runtime"},
		Errors:             integrationConnectionErrors(), Examples: integrationConnectionExamples("example_connector", "default", nil),
		Sources: []capabilitycontract.CapabilityAuthoringSource{{Kind: "model", Path: "runtime/domain/integration/model/integration_runtime.go", Symbol: "IntegrationConnectionUpsertRequest"}, {Kind: "validation", Path: "runtime/application/integration/integration_application_binding_validation.go", Symbol: "IntegrationApplicationService.ValidateIntegrationConnectionDraft"}, {Kind: "service", Path: "runtime/application/integration/integration_application_connection_upsert.go", Symbol: "IntegrationApplicationService.UpsertIntegrationConnection"}},
	}
	if key == "integration.connection.rotate" {
		capability.Sources[2] = capabilitycontract.CapabilityAuthoringSource{Kind: "service", Path: "runtime/application/integration/integration_application_connection_rotation.go", Symbol: "IntegrationApplicationService.RotateIntegrationConnection"}
	}
	_ = providerSelection
	return capability
}

func integrationConnectionCommandCapability(key, route, auditEvent, sourcePath, sourceSymbol string) capabilitycontract.CapabilityAuthoringDefinition {
	parameters := []capabilitycontract.CapabilityAuthoringParameter{{Key: "connection_key", Type: "connection_key", Required: true}}
	return capabilitycontract.CapabilityAuthoringDefinition{
		Key: key, Status: "supported", Lifecycle: "audited_connection_command", Parameters: parameters, Requires: []string{"integration.connection"}, Permissions: []string{"integration.connection.manage"}, AuditEvents: []string{auditEvent},
		ConfigurationRoutes: []string{route}, ResourceKeyPathParameter: "connectionKey", InputSchema: integrationClosedObjectSchema(parameters), OutputSchema: integrationConnectionOutputSchema(),
		OutputVariables:    []capabilitycontract.CapabilityAuthoringOutput{{Name: "connection_key", JSONPointer: "/key", Type: "connection_key", VisibleTo: "subsequent_capability_calls"}},
		ReferenceContracts: []capabilitycontract.CapabilityAuthoringReference{{Kind: "connection_key", InputJSONPointer: "/connection_key", ResolverEndpoint: "/tenant-admin/platform-capabilities/references/connection_key"}},
		Execution:          &capabilitycontract.CapabilityAuthoringExecution{ReadSet: []string{"integration.connection"}, WriteSet: []string{"integration.connection"}, Transaction: "integration_connection_transaction", Idempotency: "connection_state_transition", SideEffects: []string{auditEvent}, SideEffectLevel: "internal", PermissionModel: "integration.connection.manage"},
		Errors:             integrationConnectionErrors(), Examples: integrationConnectionCommandExamples(), Sources: []capabilitycontract.CapabilityAuthoringSource{{Kind: "service", Path: sourcePath, Symbol: sourceSymbol}},
	}
}

// SpecializeIntegrationConnectionAuthoringCapability closes config and
// secret_refs against the selected Runtime-owned connector provider.
func SpecializeIntegrationConnectionAuthoringCapability(key string, connector integrationmodel.ConnectorSchema, providerKey string) (capabilitycontract.CapabilityAuthoringDefinition, bool) {
	var capability capabilitycontract.CapabilityAuthoringDefinition
	switch key {
	case "integration.connection":
		capability = IntegrationConnectionAuthoringCapability()
	case "integration.connection.rotate":
		capability = IntegrationConnectionRotateAuthoringCapability()
	default:
		return capabilitycontract.CapabilityAuthoringDefinition{}, false
	}
	providerKey = strings.TrimSpace(providerKey)
	for _, provider := range connector.Providers {
		if provider.Key != providerKey {
			continue
		}
		capability.InputSchema = integrationConnectionInputSchema(&provider)
		capability.Examples = integrationConnectionExamples(connector.Key, provider.Key, &provider)
		return capability, true
	}
	return capabilitycontract.CapabilityAuthoringDefinition{}, false
}

func integrationConnectionInputSchema(provider *integrationmodel.ConnectorProviderSchema) *capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	config, secrets := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{}}, capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{}}
	if provider != nil {
		config = integrationProviderValueSchema(provider.ConfigFields, false)
		secrets = integrationProviderValueSchema(provider.SecretFields, true)
	}
	return &capabilitycontract.CapabilityAuthoringSchema{Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed, Required: []string{"connector_key", "provider_key"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"key": {Type: "string"}, "connector_key": {Type: "string"}, "provider_key": {Type: "string"}, "name": {Type: "string"}, "status": {Type: "string", Enum: integrationAnyEnums(integrationmodel.RuntimeIntegrationConnectionStatuses()), Default: "configured"}, "config": config, "secret_refs": secrets,
	}}
}

func integrationProviderValueSchema(fields []definitionmodel.FieldSchema, secretRefs bool) capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	result := capabilitycontract.CapabilityAuthoringSchema{Type: "object", AdditionalProperties: &closed, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{}, Required: []string{}}
	for _, field := range fields {
		property := integrationFieldValueSchema(field)
		if secretRefs {
			property = capabilitycontract.CapabilityAuthoringSchema{Type: "string", Description: "Reference to an Integration-owned secret of kind " + field.Type + "."}
		}
		result.Properties[field.Key] = property
		if field.Required {
			result.Required = append(result.Required, field.Key)
		}
	}
	return result
}

func integrationFieldValueSchema(field definitionmodel.FieldSchema) capabilitycontract.CapabilityAuthoringSchema {
	property := capabilitycontract.CapabilityAuthoringSchema{Description: field.Description, Default: field.Default, MinLength: integrationOptionalInt(field.Validation.MinLength), MaxLength: integrationOptionalInt(field.Validation.MaxLength), Minimum: field.Validation.Min, Maximum: field.Validation.Max}
	switch field.Type {
	case "integer":
		property.Type = "integer"
	case "decimal":
		property.Type = "number"
	case "boolean":
		property.Type = "boolean"
	case "json":
		property.Type = "object"
		open := true
		property.AdditionalProperties = &open
	default:
		property.Type = "string"
	}
	for _, option := range field.Validation.Options {
		property.Enum = append(property.Enum, option)
	}
	return property
}

func integrationConnectionOutputSchema() *capabilitycontract.CapabilityAuthoringSchema {
	closed, open := false, true
	return &capabilitycontract.CapabilityAuthoringSchema{Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed, Required: []string{"key", "connector_key", "provider_key", "status"}, Properties: map[string]capabilitycontract.CapabilityAuthoringSchema{
		"key": {Type: "string"}, "workspace_id": {Type: "string"}, "connector_key": {Type: "string"}, "provider_key": {Type: "string"}, "name": {Type: "string"}, "status": {Type: "string", Enum: integrationAnyEnums(integrationmodel.RuntimeIntegrationConnectionStatuses())}, "config": {Type: "object", AdditionalProperties: &open}, "secret_refs": {Type: "object", AdditionalProperties: &open}, "created_by": {Type: "string"}, "created_at": {Type: "string", Format: "date-time"}, "updated_at": {Type: "string", Format: "date-time"},
	}}
}

func integrationEmptyOutputSchema() *capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	return &capabilitycontract.CapabilityAuthoringSchema{Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed}
}

func integrationClosedObjectSchema(parameters []capabilitycontract.CapabilityAuthoringParameter) *capabilitycontract.CapabilityAuthoringSchema {
	closed := false
	properties, required := map[string]capabilitycontract.CapabilityAuthoringSchema{}, []string{}
	for _, parameter := range parameters {
		properties[parameter.Key] = capabilitycontract.CapabilityAuthoringSchema{Type: "string"}
		if parameter.Required {
			required = append(required, parameter.Key)
		}
	}
	return &capabilitycontract.CapabilityAuthoringSchema{Schema: "https://json-schema.org/draft/2020-12/schema", Type: "object", AdditionalProperties: &closed, Properties: properties, Required: required}
}

func integrationConnectionErrors() []capabilitycontract.CapabilityAuthoringError {
	items := []struct{ code, path string }{{"backend.integration.connection.missing_key", "key"}, {"backend.integration.connection.key_invalid", "key"}, {"backend.integration.connection.missing_connector", "connector_key"}, {"backend.integration.connector.not_found", "connector_key"}, {"backend.integration.connection.connector_immutable", "connector_key"}, {"backend.integration.connection.provider_immutable", "provider_key"}, {"backend.integration.connection.provider_in_config_forbidden", "config"}, {"backend.integration.connection.provider_secret_required", "secret_refs"}, {"backend.integration.connection.provider_secret_unknown", "secret_refs"}, {"backend.integration.connection.provider_secret_kind_mismatch", "secret_refs"}, {"backend.integration.connection.provider_secret_test_required", "secret_refs"}, {"backend.integration.connection.referenced", "connection_key"}, {"backend.integration.connection.invalid_status", "status"}, {"backend.integration.connection.url_required", "config.url"}, {"backend.integration.connection.mock_responses_required", "config.responses"}}
	result := make([]capabilitycontract.CapabilityAuthoringError, 0, len(items))
	for _, item := range items {
		result = append(result, capabilitycontract.CapabilityAuthoringError{Code: item.code, FieldPath: item.path, MessageKey: item.code})
	}
	return result
}

func integrationConnectionExamples(connectorKey, providerKey string, provider *integrationmodel.ConnectorProviderSchema) []capabilitycontract.CapabilityAuthoringExample {
	config, secrets := map[string]any{}, map[string]any{}
	if provider != nil {
		for _, field := range provider.ConfigFields {
			if field.Required {
				config[field.Key] = integrationExampleFieldValue(field)
			}
		}
		for _, field := range provider.SecretFields {
			if field.Required {
				secrets[field.Key] = "$secret." + field.Key
			}
		}
	}
	representative := map[string]any{"key": connectorKey + "_primary", "connector_key": connectorKey, "provider_key": providerKey, "name": "Primary " + connectorKey, "status": "configured", "config": config, "secret_refs": secrets}
	return []capabilitycontract.CapabilityAuthoringExample{{Name: "minimal_valid", Value: map[string]any{"connector_key": connectorKey, "provider_key": providerKey, "status": "draft"}}, {Name: "representative", Value: representative}, {Name: "invalid_with_repair", Value: map[string]any{"connector_key": connectorKey, "provider_key": providerKey, "status": "unknown"}, ExpectedErrorCodes: []string{"backend.integration.connection.invalid_status"}}}
}

func integrationConnectionCommandExamples() []capabilitycontract.CapabilityAuthoringExample {
	return []capabilitycontract.CapabilityAuthoringExample{{Name: "minimal_valid", Value: map[string]any{"connection_key": "erp_primary"}}, {Name: "representative", Value: map[string]any{"connection_key": "webhook_primary"}}, {Name: "invalid_with_repair", Value: map[string]any{"connection_key": ""}, ExpectedErrorCodes: []string{"backend.integration.connection.missing_key"}}}
}
func integrationExampleFieldValue(field definitionmodel.FieldSchema) any {
	if field.Default != nil {
		return field.Default
	}
	switch field.Type {
	case "integer":
		return 1
	case "decimal":
		return 1.0
	case "boolean":
		return true
	case "json":
		return map[string]any{}
	default:
		return fmt.Sprintf("example_%s", field.Key)
	}
}
func integrationOptionalInt(value int) *int {
	if value <= 0 {
		return nil
	}
	return &value
}
