package validation

import (
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"

	"fmt"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
)

var connectorProtocolFieldTypes = integrationmodel.RuntimeConnectorProtocolFieldTypes()

func MetadataValidateConnectorDefinition(connector integrationmodel.ConnectorSchema) error {
	return MetadataFirstDefinitionIssueError(MetadataValidateConnectorDefinitionIssues(connector))
}

func MetadataValidateConnectorDefinitionIssues(connector integrationmodel.ConnectorSchema) []metadatamodel.MetadataDefinitionValidationIssue {
	issues := make([]metadatamodel.MetadataDefinitionValidationIssue, 0)
	connectorType := strings.TrimSpace(connector.Type)
	if !enumContains(integrationmodel.RuntimeConnectorTypes(), connectorType) {
		issues = append(issues, connectorIssue("backend.integration.connector.type_invalid", "type", "", map[string]string{"type": connector.Type, "allowed": strings.Join(integrationmodel.RuntimeConnectorTypes(), ","), "actual": connector.Type}))
	}
	for _, identity := range []struct{ path, value string }{{"key", connector.Key}, {"provider", connector.Provider}} {
		if strings.TrimSpace(identity.value) == "" {
			issues = append(issues, connectorIssue("backend.integration.connector.identity_required", identity.path, "", map[string]string{"field": identity.path}))
		}
	}
	if len(connector.Operations) == 0 {
		return append(issues, connectorIssue("backend.integration.connector.operation_required", "operations", "", map[string]string{"field": "operations"}))
	}
	for key := range connector.Config {
		if key == "providers" || strings.HasSuffix(strings.TrimSpace(key), "_providers") {
			issues = append(issues, connectorIssue("backend.integration.connector.legacy_provider_config_forbidden", "config."+key, "", map[string]string{"actual": key, "expected": "providers[]"}))
		}
	}
	declaredSecretRefs := MetadataStringSet(connector.SecretRefs)
	for index, name := range integrationmodel.RequiredConnectorSecretRefNames(connector) {
		if !declaredSecretRefs[name] {
			path := fmt.Sprintf("config.required_secret_refs[%d]", index)
			issues = append(issues, connectorIssue("backend.integration.connector.required_secret_ref_unknown", path, "", map[string]string{"secret_ref_name": name, "actual": name, "allowed": strings.Join(connector.SecretRefs, ",")}))
		}
	}
	seen := map[string]bool{}
	for index, operation := range connector.Operations {
		operationKey := strings.TrimSpace(operation.Key)
		prefix := fmt.Sprintf("operations[%d]", index)
		if operationKey == "" || seen[operationKey] {
			issues = append(issues, connectorIssue("backend.integration.connector.operation_key_invalid", prefix+".key", operationKey, map[string]string{"operation": operation.Key, "actual": operation.Key}))
		} else {
			seen[operationKey] = true
		}
		issues = append(issues, validateConnectorOperationContract(operation, operationKey, prefix)...)
	}
	for index, operation := range connector.Operations {
		operationKey := strings.TrimSpace(operation.Key)
		compensation := strings.TrimSpace(operation.CompensationOperation)
		path := fmt.Sprintf("operations[%d].compensation_operation", index)
		if compensation != "" && !seen[compensation] {
			issues = append(issues, connectorIssue("backend.integration.connector.compensation_operation_not_found", path, operationKey, map[string]string{"operation": operation.Key, "compensation_operation": compensation, "actual": compensation}))
		}
		if strings.TrimSpace(operation.SideEffect) == "reserve" && (!operation.IdempotencySupported || compensation == "") {
			issues = append(issues, connectorIssue("backend.integration.connector.reserve_contract_incomplete", fmt.Sprintf("operations[%d]", index), operationKey, map[string]string{"operation": operation.Key}))
		}
	}
	return issues
}

func validateConnectorOperationContract(operation integrationmodel.ConnectorOperationSchema, operationKey, prefix string) []metadatamodel.MetadataDefinitionValidationIssue {
	issues := make([]metadatamodel.MetadataDefinitionValidationIssue, 0)
	if mode := strings.TrimSpace(operation.ExecutionMode); !enumContains(integrationmodel.RuntimeConnectorExecutionModes(), mode) {
		issues = append(issues, connectorEnumIssue("backend.integration.connector.operation_execution_mode_invalid", prefix+".execution_mode", operationKey, operation.ExecutionMode, integrationmodel.RuntimeConnectorExecutionModes(), "execution_mode"))
	}
	method := strings.ToUpper(strings.TrimSpace(operation.Method))
	if !enumContains(integrationmodel.RuntimeConnectorMethods(), method) {
		issues = append(issues, connectorEnumIssue("backend.integration.connector.operation_method_invalid", prefix+".method", operationKey, operation.Method, integrationmodel.RuntimeConnectorMethods(), "method"))
	}
	if sideEffect := strings.TrimSpace(operation.SideEffect); !enumContains(integrationmodel.RuntimeConnectorSideEffects(), sideEffect) {
		issues = append(issues, connectorEnumIssue("backend.integration.connector.operation_side_effect_invalid", prefix+".side_effect", operationKey, operation.SideEffect, integrationmodel.RuntimeConnectorSideEffects(), "side_effect"))
	}
	if operation.TimeoutDefaultSeconds <= 0 || operation.TimeoutMaxSeconds <= 0 || operation.TimeoutDefaultSeconds > operation.TimeoutMaxSeconds {
		issues = append(issues, connectorIssue("backend.integration.connector.operation_timeout_invalid", prefix+".timeout_default_seconds", operationKey, map[string]string{"operation": operation.Key, "actual": fmt.Sprint(operation.TimeoutDefaultSeconds), "maximum": fmt.Sprint(operation.TimeoutMaxSeconds)}))
	}
	issues = append(issues, validateConnectorProtocolFieldIssues(operationKey, prefix+".input", "input", operation.Input)...)
	issues = append(issues, validateConnectorProtocolFieldIssues(operationKey, prefix+".output", "output", operation.Output)...)
	return issues
}

func validateConnectorProtocolFieldIssues(operationKey, prefix, direction string, fields []definitionmodel.FieldSchema) []metadatamodel.MetadataDefinitionValidationIssue {
	issues := make([]metadatamodel.MetadataDefinitionValidationIssue, 0)
	seen := map[string]bool{}
	allowed := MetadataStringSet(connectorProtocolFieldTypes)
	for index, field := range fields {
		key := strings.TrimSpace(field.Key)
		if key == "" || seen[key] {
			path := fmt.Sprintf("%s[%d].key", prefix, index)
			issues = append(issues, connectorIssue("backend.integration.connector.protocol_field_key_invalid", path, operationKey, map[string]string{"operation": operationKey, "direction": direction, "field": field.Key, "actual": field.Key}))
		} else {
			seen[key] = true
		}
		if fieldType := strings.TrimSpace(field.Type); !allowed[fieldType] {
			path := fmt.Sprintf("%s[%d].type", prefix, index)
			issues = append(issues, connectorIssue("backend.integration.connector.protocol_field_type_invalid", path, operationKey, map[string]string{"operation": operationKey, "direction": direction, "field": field.Key, "type": field.Type, "allowed": strings.Join(connectorProtocolFieldTypes, ","), "actual": field.Type}))
		}
	}
	return issues
}

func connectorEnumIssue(code, path, operationKey, actual string, allowed []string, legacyKey string) metadatamodel.MetadataDefinitionValidationIssue {
	return connectorIssue(code, path, operationKey, map[string]string{"operation": operationKey, legacyKey: actual, "allowed": strings.Join(allowed, ","), "actual": actual})
}

func connectorIssue(code, path, operationKey string, params map[string]string) metadatamodel.MetadataDefinitionValidationIssue {
	if params == nil {
		params = map[string]string{}
	}
	params["field"] = path
	return NewMetadataDefinitionValidationIssue(code, path, "", operationKey, params)
}

func enumContains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
