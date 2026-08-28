package integration

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/domainry/domainry-connector-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationpolicy "github.com/domainry/domainry-runtime/runtime/domain/integration/policy"
	localizationmodel "github.com/domainry/domainry-runtime/runtime/domain/localization/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func toIntegrationProviderSchema(descriptor connector.ProviderDescriptor) integrationmodel.ConnectorProviderSchema {
	operations := make([]string, 0, len(descriptor.Operations))
	for _, operation := range descriptor.Operations {
		operations = append(operations, operation.Key)
	}
	sort.Strings(operations)
	return integrationmodel.ConnectorProviderSchema{
		Key: descriptor.ProviderKey, ProviderRevision: descriptor.ProviderRevision,
		ConfigFields:  toIntegrationConfigFields(descriptor.ConfigFields),
		SecretFields:  toIntegrationSecretFields(descriptor.SecretFields),
		OperationKeys: operations,
	}
}

func toIntegrationConfigFields(fields []connector.ConfigField) []definitionmodel.FieldSchema {
	result := make([]definitionmodel.FieldSchema, 0, len(fields))
	for _, field := range fields {
		config := map[string]any{"contract_owner": "connector"}
		if len(field.RequiredWith) > 0 {
			config["required_with"] = append([]string(nil), field.RequiredWith...)
		}
		var defaultValue any
		if len(field.Default) > 0 {
			decoder := json.NewDecoder(strings.NewReader(string(field.Default)))
			decoder.UseNumber()
			_ = decoder.Decode(&defaultValue) // Registry validation already proved the public default valid.
		}
		result = append(result, definitionmodel.FieldSchema{
			Key: field.Key, Name: field.Name, Description: field.Description, Type: string(field.Type),
			I18n: toIntegrationFieldLocalization(field.I18n), Config: config,
			Validation: definitionmodel.FieldValidation{
				MinLength: field.Validation.MinLength, MaxLength: field.Validation.MaxLength,
				Min: cloneIntegrationFloat(field.Validation.Min), Max: cloneIntegrationFloat(field.Validation.Max),
				Pattern: field.Validation.Pattern, Options: append([]string(nil), field.Validation.Options...),
			},
			Options: append([]string(nil), field.Validation.Options...), Required: field.Required, Default: defaultValue,
		})
	}
	return result
}

func toIntegrationSecretFields(fields []connector.SecretField) []definitionmodel.FieldSchema {
	result := make([]definitionmodel.FieldSchema, 0, len(fields))
	for _, field := range fields {
		result = append(result, definitionmodel.FieldSchema{
			Key: field.Key, Name: field.Name, Description: field.Description, Type: "text",
			I18n: toIntegrationFieldLocalization(field.I18n), Required: field.Required,
			Config: map[string]any{
				"contract_owner": "connector", "sensitive": true, "write_only": true,
				"credential_kind": string(field.CredentialKind), "material_format": string(field.MaterialFormat),
				"rotation_policy": string(field.RotationPolicy), "expiry_policy": string(field.ExpiryPolicy),
				"test_requirement": string(field.TestRequirement),
			},
		})
	}
	return result
}

func toIntegrationFieldLocalization(values map[string]connector.FieldLocalization) localizationmodel.LocalizedTextMap {
	if values == nil {
		return nil
	}
	result := make(localizationmodel.LocalizedTextMap, len(values))
	for locale, value := range values {
		result[locale] = map[string]string{"name": value.Name, "description": value.Description}
	}
	return result
}

func cloneIntegrationFloat(value *float64) *float64 {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}

func toConnectorConnection(connection integrationmodel.IntegrationConnection) connector.Connection {
	return connector.Connection{
		Key: connection.Key, WorkspaceID: connection.WorkspaceID, ConnectorKey: connection.ConnectorKey,
		ProviderKey: connection.ProviderKey, Name: connection.Name, Status: connection.Status,
		Config: cloneIntegrationAny(connection.Config), SecretRefs: cloneIntegrationStrings(connection.SecretRefs),
		CreatedBy: connection.CreatedBy, CreatedAt: connection.CreatedAt, UpdatedAt: connection.UpdatedAt,
	}
}

func toConnectorPrincipal(principal principalmodel.Principal) connector.Principal {
	return connector.Principal{
		UserID: principal.UserID, RoleKey: principal.RoleKey, DepartmentID: principal.DepartmentID,
		RequestID: principal.RequestID, CorrelationID: principal.CorrelationID, CausationID: principal.CausationID,
		SurfaceKey: principal.SurfaceKey, WorkspaceID: principal.WorkspaceID, IsAuthenticated: principal.Known,
	}
}

func cloneIntegrationStrings(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func cloneIntegrationAny(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	result := make(map[string]any, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func decodePublicObject(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return map[string]any{}, nil
	}
	var result map[string]any
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.UseNumber()
	if err := decoder.Decode(&result); err != nil {
		return nil, err
	}
	if result == nil {
		return map[string]any{}, nil
	}
	return normalizePublicJSONNumbers(result).(map[string]any), nil
}

// Webhook payloads are persisted as JSON before replay comparison. Keep the
// standard JSON numeric representation here so the first in-memory event and
// the same event reloaded from storage have identical canonical fingerprints.
func decodePublicWebhookObject(raw json.RawMessage) (map[string]any, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return map[string]any{}, nil
	}
	var result map[string]any
	if err := json.Unmarshal(raw, &result); err != nil {
		return nil, err
	}
	if result == nil {
		return map[string]any{}, nil
	}
	return result, nil
}

func normalizePublicJSONNumbers(value any) any {
	switch typed := value.(type) {
	case json.Number:
		if integer, err := typed.Int64(); err == nil {
			return int(integer)
		}
		if decimal, err := typed.Float64(); err == nil {
			return decimal
		}
		return typed
	case map[string]any:
		for key, item := range typed {
			typed[key] = normalizePublicJSONNumbers(item)
		}
		return typed
	case []any:
		for index, item := range typed {
			typed[index] = normalizePublicJSONNumbers(item)
		}
		return typed
	default:
		return value
	}
}

func toIntegrationProviderError(err error) error {
	classification, classified := connector.ErrorClassificationOf(err)
	if !classified {
		return err
	}
	// A validated ProviderError classification and code are one atomic public
	// contract; ErrorClassificationOf cannot succeed while the code is invalid.
	code, _ := connector.ProviderErrorCodeOf(err)
	category := integrationpolicy.ErrorProviderPermanent
	switch classification {
	case connector.ErrorRetryable:
		category = integrationpolicy.ErrorProviderRetryable
	case connector.ErrorPermanent:
		category = integrationpolicy.ErrorProviderPermanent
	case connector.ErrorUncertain:
		category = integrationpolicy.ErrorProviderUncertain
	}
	return integrationpolicy.NewProviderError(category, code, err)
}

func toIntegrationWebhookSecurity(value *connector.WebhookSecurityEvidence) *integrationcontract.WebhookSecurityEvidence {
	if value == nil {
		return nil
	}
	return &integrationcontract.WebhookSecurityEvidence{
		SignatureVerified: value.SignatureVerified, Nonce: value.Nonce, DeviceIdentity: value.DeviceIdentity,
		EventTime: integrationTimeString(value.EventTime),
	}
}

func toIntegrationWebhookIdentity(value *connector.WebhookExternalIdentity) *integrationcontract.WebhookExternalIdentity {
	if value == nil {
		return nil
	}
	return &integrationcontract.WebhookExternalIdentity{Subject: value.Subject, SubjectType: value.SubjectType, Name: value.Name, Group: value.Group}
}

func toIntegrationWebhookReceipt(value *connector.WebhookDeliveryReceipt) *integrationcontract.WebhookDeliveryReceipt {
	if value == nil {
		return nil
	}
	return &integrationcontract.WebhookDeliveryReceipt{
		ResponseRef: value.ResponseRef, Status: value.Status, Error: value.Error, OccurredAt: integrationTimeString(value.OccurredAt),
	}
}

func (s *IntegrationApplicationService) IntegrationConnectorCatalog(ctx context.Context, principal principalmodel.Principal) ([]integrationmodel.ConnectorSchema, error) {
	if err := integrationAuthorizeQuery(principal); err != nil {
		return nil, err
	}
	if !HasPermission(principal, PermissionCatalogView) {
		return nil, forbidden("auth.permission_denied")
	}
	return s.integrationConnectorCatalogForWorkspace(ctx, principalWorkspaceID(principal))
}

func (s *IntegrationApplicationService) integrationConnectorCatalogForWorkspace(ctx context.Context, workspaceID string) ([]integrationmodel.ConnectorSchema, error) {
	connectors, adapters := s.registry.Catalog()
	connections, err := s.configRepo.ListConnections(ctx, workspaceID)
	if err != nil {
		return nil, err
	}
	for index := range connections {
		connections[index], err = s.normalizeConnection(ctx, connections[index])
		if err != nil {
			return nil, err
		}
	}
	activeConnections := map[string]bool{}
	for _, connection := range connections {
		if connectionCanSend(connection) && s.ValidateAdapterConfig(connection) == nil {
			activeConnections[integrationpolicy.IntegrationProviderIdentity(connection.ConnectorKey, connection.ProviderKey)] = true
		}
	}
	for index := range connectors {
		connector := &connectors[index]
		connector.DefinitionReady = integrationpolicy.IntegrationConnectorDefinitionReady(*connector)
		if connectorLifecycleStatus(*connector) != "active" {
			connector.AdapterReady, connector.ConnectionReady, connector.Readiness = false, false, "disabled"
			for providerIndex := range connector.Providers {
				connector.Providers[providerIndex].Readiness = "disabled"
			}
			continue
		}
		connector.AdapterReady, connector.ConnectionReady = false, false
		for providerIndex := range connector.Providers {
			provider := &connector.Providers[providerIndex]
			providerIdentity := integrationpolicy.IntegrationProviderIdentity(connector.Key, provider.Key)
			providerReady := adapters[providerIdentity]
			providerConnectionReady := providerReady && activeConnections[providerIdentity]
			switch {
			case providerConnectionReady:
				provider.Readiness = "connection_ready"
			case providerReady:
				provider.Readiness = "adapter_ready"
			default:
				provider.Readiness = "provider_available"
			}
			connector.AdapterReady = connector.AdapterReady || providerReady
			connector.ConnectionReady = connector.ConnectionReady || providerConnectionReady
		}
		switch {
		case connector.ConnectionReady:
			connector.Readiness = "connection_ready"
		case connector.AdapterReady:
			connector.Readiness = "adapter_ready"
		default:
			connector.Readiness = "catalog_only"
		}
	}
	return connectors, nil
}
