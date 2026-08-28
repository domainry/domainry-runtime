package integration

import (
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"

	"context"
	"errors"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"sort"
	"strings"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationpolicy "github.com/domainry/domainry-runtime/runtime/domain/integration/policy"

	apperror "github.com/domainry/domainry-foundation/apperror"
	capability "github.com/domainry/domainry-runtime/runtime/domain/capability/contract"
	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
)

type BindingValidationRequest struct {
	ConnectorKey    string                                               `json:"connector_key"`
	OperationKey    string                                               `json:"operation_key,omitempty"`
	ConnectionKey   string                                               `json:"connection_key,omitempty"`
	ConnectionDraft *integrationmodel.IntegrationConnectionUpsertRequest `json:"connection_draft,omitempty"`
	Input           map[string]any                                       `json:"input,omitempty"`
	Output          map[string]any                                       `json:"output,omitempty"`
}

type BindingValidationResult struct {
	Valid           bool                     `json:"valid"`
	ConnectionReady bool                     `json:"connection_ready"`
	ConnectorReady  bool                     `json:"connector_ready"`
	OperationReady  bool                     `json:"operation_ready"`
	SecretRefsReady bool                     `json:"secret_refs_ready"`
	Errors          []BindingValidationIssue `json:"errors"`
	ContractVersion string                   `json:"contract_version"`
}

type BindingValidationIssue struct {
	Section         string            `json:"section"`
	FieldPath       string            `json:"field_path"`
	ConnectorKey    string            `json:"connector_key,omitempty"`
	ConnectionKey   string            `json:"connection_key,omitempty"`
	OperationKey    string            `json:"operation_key,omitempty"`
	ErrorCode       string            `json:"error_code"`
	MessageKey      string            `json:"message_key"`
	CapabilityKey   string            `json:"capability_key"`
	ContractVersion string            `json:"contract_version"`
	Params          map[string]string `json:"params,omitempty"`
}

type bindingValidator struct {
	integration *IntegrationApplicationService
	principal   principalmodel.Principal
	request     BindingValidationRequest
	result      BindingValidationResult
	connector   integrationmodel.ConnectorSchema
	operation   *integrationmodel.ConnectorOperationSchema
	connection  integrationmodel.IntegrationConnection
}

func (s *IntegrationApplicationService) ValidateIntegrationBinding(ctx context.Context, request BindingValidationRequest, principal principalmodel.Principal) (BindingValidationResult, error) {
	if err := integrationAuthorizeQuery(principal); err != nil {
		return BindingValidationResult{}, err
	}
	if !HasAnyPermission(principal, PermissionCatalogView, PermissionConnectionManage) {
		return BindingValidationResult{}, forbidden("auth.permission_denied")
	}
	validator := &bindingValidator{integration: s, principal: principal, request: request, result: BindingValidationResult{Errors: []BindingValidationIssue{}, ContractVersion: capability.RuntimeAuthoringContractVersion}}
	if !validator.resolveConnector() {
		return validator.finish(), nil
	}
	validator.resolveOperation()
	validator.resolveConnection(ctx)
	validator.validateProtocolValues("input", request.Input)
	validator.validateProtocolValues("output", request.Output)
	return validator.finish(), nil
}

func (s *IntegrationApplicationService) ValidateIntegrationConnectionDraft(ctx context.Context, connectionKey string, request integrationmodel.IntegrationConnectionUpsertRequest, principal principalmodel.Principal) error {
	result, err := s.ValidateIntegrationBinding(ctx, BindingValidationRequest{ConnectorKey: strings.TrimSpace(request.ConnectorKey), ConnectionKey: strings.TrimSpace(connectionKey), ConnectionDraft: &request}, principal)
	if err != nil || len(result.Errors) == 0 {
		return err
	}
	return bindingIssueError(result.Errors[0])
}

func (v *bindingValidator) resolveConnector() bool {
	key := strings.TrimSpace(v.request.ConnectorKey)
	connector, exists := v.integration.ConnectorDefinition(key)
	if !exists {
		v.issue("connector", "connector_key", "backend.integration.connector.not_found", "integration.connector_definition", map[string]string{"connector": key, "actual": key})
		return false
	}
	v.connector = connector
	if connectorLifecycleStatus(connector) != "active" {
		v.issue("connector", "connector_key", "backend.integration.connector.reclassified", "integration.connection", map[string]string{"connector": key, "replacement_capability": connector.ReplacementCapability})
		return true
	}
	if !integrationpolicy.IntegrationConnectorDefinitionReady(connector) {
		v.issue("connector", "connector_key", "backend.integration.connector.definition_not_ready", "integration.connector_definition", map[string]string{"connector": key})
		return true
	}
	if !v.integration.ConnectorAdapterReady(connector) {
		v.issue("connector", "connector_key", "backend.integration.connector.adapter_not_ready", "integration.connection", map[string]string{"connector": key})
		return true
	}
	v.result.ConnectorReady = true
	return true
}

func (v *bindingValidator) resolveOperation() {
	key := strings.TrimSpace(v.request.OperationKey)
	if key == "" {
		if v.request.Input != nil || v.request.Output != nil {
			v.issue("operation", "operation_key", "backend.integration.binding.operation_required", "integration.connector_operation", nil)
		}
		return
	}
	for index := range v.connector.Operations {
		if strings.TrimSpace(v.connector.Operations[index].Key) == key {
			v.operation, v.result.OperationReady = &v.connector.Operations[index], true
			return
		}
	}
	v.issue("operation", "operation_key", "backend.integration.binding.operation_not_found", "integration.connector_operation", map[string]string{"connector": v.connector.Key, "operation": key, "actual": key})
}

func (v *bindingValidator) resolveConnection(ctx context.Context) {
	if v.request.ConnectionDraft != nil {
		v.validateConnectionDraft(ctx, *v.request.ConnectionDraft)
		return
	}
	key := strings.TrimSpace(v.request.ConnectionKey)
	if key == "" {
		v.issue("connection", "connection_key", "backend.integration.binding.connection_required", "integration.connection", nil)
		return
	}
	connection, exists := v.integration.LookupConnection(ctx, key, principalWorkspaceID(v.principal))
	if !exists {
		v.issue("connection", "connection_key", "backend.integration.connection.not_found", "integration.connection", map[string]string{"connection": key, "actual": key})
		return
	}
	v.connection = connection
	if strings.TrimSpace(connection.ConnectorKey) != strings.TrimSpace(v.connector.Key) {
		v.issue("connection", "connection_key", "backend.integration.binding.connection_connector_mismatch", "integration.connection", map[string]string{"connection": key, "expected": v.connector.Key, "actual": connection.ConnectorKey})
	}
	if !connectionCanSend(connection) {
		v.issue("connection", "connection.status", "backend.integration.connection_unavailable", "integration.connection", map[string]string{"connection": key, "actual": connection.Status, "allowed": "active,verified"})
	}
	v.validateSecretRefs(ctx, connection.SecretRefs)
	if err := ValidateConnectionConfig(v.connector, connection.ProviderKey, connection.Status, connection.Config); err != nil {
		v.appendServiceError("connection", "connection.config", err, "integration.connection")
	}
	v.result.ConnectionReady = !v.hasSectionError("connection") && v.result.ConnectorReady && v.result.SecretRefsReady
}

func (v *bindingValidator) validateConnectionDraft(ctx context.Context, draft integrationmodel.IntegrationConnectionUpsertRequest) {
	key := valueOrDefault(strings.TrimSpace(v.request.ConnectionKey), strings.TrimSpace(draft.Key))
	v.connection = integrationmodel.IntegrationConnection{Key: key, ConnectorKey: strings.TrimSpace(draft.ConnectorKey), ProviderKey: strings.TrimSpace(draft.ProviderKey), Status: strings.TrimSpace(draft.Status), Config: cloneMap(draft.Config), SecretRefs: cloneStringMap(draft.SecretRefs)}
	if key == "" {
		v.issue("connection", "connection_key", "backend.integration.connection.missing_key", "integration.connection", nil)
	}
	if v.connection.ConnectorKey != strings.TrimSpace(v.connector.Key) {
		v.issue("connection", "connection_draft.connector_key", "backend.integration.binding.connection_connector_mismatch", "integration.connection", map[string]string{"expected": v.connector.Key, "actual": v.connection.ConnectorKey})
	}
	providerKey, err := resolveConnectorProvider(v.connector, draft.ProviderKey)
	if err != nil {
		v.appendServiceError("connection", "connection_draft.provider_key", err, "integration.connection")
	} else {
		v.connection.ProviderKey = providerKey
	}
	status, err := NormalizeConnectionStatus(draft.Status)
	if err != nil {
		v.appendServiceError("connection", "connection_draft.status", err, "integration.connection")
	} else {
		v.connection.Status = status
		if status == "disabled" {
			v.issue("connection", "connection_draft.status", "backend.integration.connection_unavailable", "integration.connection", map[string]string{"actual": status, "allowed": "draft,configured,verified,active,degraded"})
		}
	}
	if _, err := NormalizeSecretRefsSyntax(draft.SecretRefs); err != nil {
		v.appendServiceError("secret_refs", "connection_draft.secret_refs", err, "integration.connection")
	}
	v.validateSecretRefs(ctx, draft.SecretRefs)
	if err := v.integration.ValidateProviderSecretRefs(ctx, v.connector, v.connection.ProviderKey, v.connection.Status, draft.SecretRefs, principalWorkspaceID(v.principal)); err != nil {
		v.appendServiceError("secret_refs", "connection_draft.secret_refs", err, "integration.connection")
	}
	if err := ValidateConnectionConfig(v.connector, v.connection.ProviderKey, v.connection.Status, draft.Config); err != nil {
		v.appendServiceError("connection", "connection_draft.config", err, "integration.connection")
	}
	v.result.ConnectionReady = !v.hasSectionError("connection") && !v.hasSectionError("secret_refs") && v.result.ConnectorReady
}

func (v *bindingValidator) validateSecretRefs(ctx context.Context, values map[string]string) {
	ready := true
	for _, name := range requiredSecretRefNames(v.connector) {
		if strings.TrimSpace(values[name]) == "" {
			v.issue("secret_refs", "connection.secret_refs."+name, "backend.integration.binding.secret_ref_required", "integration.connection", map[string]string{"secret_ref_name": name})
			ready = false
		}
	}
	workspaceID := principalWorkspaceID(v.principal)
	for name, raw := range values {
		ref, path := strings.TrimSpace(raw), "connection.secret_refs."+strings.TrimSpace(name)
		if !strings.HasPrefix(ref, "secret:") {
			continue
		}
		secret, exists := v.integration.LookupSecret(ctx, strings.TrimSpace(strings.TrimPrefix(ref, "secret:")), workspaceID)
		if !exists {
			v.issue("secret_refs", path, "backend.integration.secret.not_found", "integration.connection", map[string]string{"secret_ref_name": name})
			ready = false
		} else if secret.Status == "disabled" {
			v.issue("secret_refs", path, "backend.integration.secret.disabled", "integration.connection", map[string]string{"secret_ref_name": name, "actual": secret.Status, "allowed": "active"})
			ready = false
		}
	}
	v.result.SecretRefsReady = ready
}

func (v *bindingValidator) validateProtocolValues(direction string, values map[string]any) {
	if values == nil || v.operation == nil {
		return
	}
	fields := v.operation.Input
	if direction == "output" {
		fields = v.operation.Output
	}
	byKey := make(map[string]definitionmodel.FieldSchema, len(fields))
	for _, field := range fields {
		byKey[strings.TrimSpace(field.Key)] = field
		if field.Required {
			if value, exists := values[field.Key]; !exists || recordvalidation.RecordIsEmptyValue(value) {
				v.issue(direction, direction+"."+field.Key, "backend.integration.binding.protocol_value_required", "integration.connector_operation", map[string]string{"field": field.Key, "direction": direction, "operation": v.operation.Key})
			}
		}
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		field, exists := byKey[strings.TrimSpace(key)]
		path := direction + "." + strings.TrimSpace(key)
		if !exists {
			v.issue(direction, path, "backend.integration.binding.protocol_field_unknown", "integration.connector_operation", map[string]string{"field": key, "direction": direction, "operation": v.operation.Key})
			continue
		}
		actual, known := integrationcontract.IntegrationProtocolValueType(values[key])
		if known && !integrationcontract.IntegrationProtocolTypesCompatible(actual, field.Type) {
			v.issue(direction, path, "backend.integration.binding.protocol_type_mismatch", "integration.connector_operation", map[string]string{"field": key, "direction": direction, "operation": v.operation.Key, "expected": field.Type, "actual": actual})
		}
	}
}

func (v *bindingValidator) issue(section, path, code, capabilityKey string, params map[string]string) {
	v.result.Errors = append(v.result.Errors, BindingValidationIssue{Section: section, FieldPath: path, ConnectorKey: strings.TrimSpace(v.request.ConnectorKey), ConnectionKey: strings.TrimSpace(v.request.ConnectionKey), OperationKey: strings.TrimSpace(v.request.OperationKey), ErrorCode: code, MessageKey: code, CapabilityKey: capabilityKey, ContractVersion: capability.RuntimeAuthoringContractVersion, Params: params})
}

func (v *bindingValidator) appendServiceError(section, fallbackPath string, err error, capabilityKey string) {
	code, params := "backend.internal", map[string]string(nil)
	var appErr *apperror.AppError
	if errors.As(err, &appErr) {
		code, params = appErr.ErrorCode(), appErr.ErrorParams()
	}
	path := fallbackPath
	for _, key := range []string{"field_path", "field", "parameter_path", "path"} {
		if value := strings.TrimSpace(params[key]); value != "" {
			path = value
			break
		}
	}
	v.issue(section, path, code, capabilityKey, params)
}

func (v *bindingValidator) hasSectionError(section string) bool {
	for _, issue := range v.result.Errors {
		if issue.Section == section {
			return true
		}
	}
	return false
}

func (v *bindingValidator) finish() BindingValidationResult {
	v.result.Valid = len(v.result.Errors) == 0
	return v.result
}

func bindingIssueError(issue BindingValidationIssue) error {
	params := make(map[string]string, len(issue.Params)+1)
	for key, value := range issue.Params {
		params[key] = value
	}
	params["field_path"] = issue.FieldPath
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	values := make([]string, 0, len(keys)*2)
	for _, key := range keys {
		values = append(values, key, params[key])
	}
	return badRequest(issue.ErrorCode, values...)
}

func requiredSecretRefNames(connector integrationmodel.ConnectorSchema) []string {
	return integrationmodel.RequiredConnectorSecretRefNames(connector)
}

func RequiredSecretRefNames(connector integrationmodel.ConnectorSchema) []string {
	return requiredSecretRefNames(connector)
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}
