package integration

import (
	"context"
	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/idempotency"
	"sort"
	"strings"
)

type IntegrationConnectionRevision struct {
	RevisionID string         `json:"revision_id"`
	Event      string         `json:"event"`
	Before     map[string]any `json:"before,omitempty"`
	After      map[string]any `json:"after,omitempty"`
	CreatedAt  string         `json:"created_at"`
}

func (s *IntegrationApplicationService) AuthorizeIntegrationConnectionUpsert(principal principalmodel.Principal) error {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return err
	}
	if !HasPermission(principal, PermissionConnectionManage) {
		return forbidden("auth.permission_denied")
	}
	return nil
}

func (s *IntegrationApplicationService) IntegrationConnectionAuthoringHash(ctx context.Context, connectionKey string, principal principalmodel.Principal) (string, bool, error) {
	connection, found, err := s.findConnection(ctx, strings.TrimSpace(connectionKey), principalWorkspaceID(principal))
	if err != nil || !found {
		return "", found, err
	}
	hash, err := idempotency.Fingerprint(idempotency.FingerprintInput{UseCase: "integration.connection.resource", ResourceType: "integration_connection", TargetID: connection.Key, Payload: connectionAuditShape(connection)})
	return hash, true, err
}

func (s *IntegrationApplicationService) GetIntegrationConnection(ctx context.Context, connectionKey string, principal principalmodel.Principal) (integrationmodel.IntegrationConnection, error) {
	connections, err := s.ListIntegrationConnections(ctx, principal)
	if err != nil {
		return integrationmodel.IntegrationConnection{}, err
	}
	connectionKey = strings.TrimSpace(connectionKey)
	for _, connection := range connections {
		if connection.Key == connectionKey {
			return connection, nil
		}
	}
	return integrationmodel.IntegrationConnection{}, notFound("backend.integration.connection.not_found", "connection", connectionKey)
}

func (s *IntegrationApplicationService) IntegrationConnectionVersions(ctx context.Context, connectionKey string, principal principalmodel.Principal) ([]IntegrationConnectionRevision, error) {
	connection, err := s.GetIntegrationConnection(ctx, connectionKey, principal)
	if err != nil {
		return nil, err
	}
	if s.connectionHistory == nil {
		return nil, &apperror.AppError{Kind: apperror.KindUnavailable, Code: "backend.integration.connection.history_unavailable"}
	}
	events, err := s.connectionHistory.Events(ctx, auditmodel.AuditEventQuery{ObjectKey: "integration_connection", RecordID: connection.Key, Limit: 1000}, principal)
	if err != nil {
		return nil, err
	}
	allowed := map[string]bool{
		"integration_connection_upserted": true,
		"integration_connection_rotated":  true,
		"integration_connection_disabled": true,
		"integration_connection_deleted":  true,
	}
	items := make([]IntegrationConnectionRevision, 0, len(events))
	for _, event := range events {
		if !allowed[event.Event] {
			continue
		}
		items = append(items, IntegrationConnectionRevision{RevisionID: event.ID, Event: event.Event, Before: event.Before, After: event.After, CreatedAt: event.CreatedAt})
	}
	return items, nil
}

func NormalizeConnectionStatus(value string) (string, error) {
	status := strings.TrimSpace(value)
	if status == "" {
		return "configured", nil
	}
	switch status {
	case "error":
		return "degraded", nil
	case "rotating":
		return "configured", nil
	}
	connectionStatuses := integrationmodel.RuntimeIntegrationConnectionStatuses()
	for _, allowed := range connectionStatuses {
		if status == allowed {
			return status, nil
		}
	}
	return "", badRequest("backend.integration.connection.invalid_status", "field_path", "status", "allowed", strings.Join(connectionStatuses, ","), "actual", status)
}

func (s *IntegrationApplicationService) NormalizeSecretRefs(ctx context.Context, values map[string]string, workspaceID string) (map[string]string, error) {
	if err := integrationAuthorizeWorkspaceQuery(workspaceID); err != nil {
		return nil, err
	}
	out, err := NormalizeSecretRefsSyntax(values)
	if err != nil {
		return nil, err
	}
	for _, value := range out {
		if !strings.HasPrefix(value, "secret:") {
			continue
		}
		secretKey := strings.TrimSpace(strings.TrimPrefix(value, "secret:"))
		secret, ok, err := s.findSecret(ctx, secretKey, workspaceID)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, notFound("backend.integration.secret.not_found")
		}
		if secret.Status == "disabled" {
			return nil, badRequest("backend.integration.secret.disabled")
		}
	}
	return out, nil
}

func NormalizeSecretRefsSyntax(values map[string]string) (map[string]string, error) {
	if len(values) == 0 {
		return nil, nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if key == "" || value == "" {
			return nil, badRequest("backend.integration.secret_ref.invalid")
		}
		if !strings.HasPrefix(value, "env:") && !strings.HasPrefix(value, "secret:") {
			return nil, badRequest("backend.integration.secret_ref.must_be_reference")
		}
		out[key] = value
	}
	return out, nil
}

func ConnectionAuditShape(connection integrationmodel.IntegrationConnection) map[string]any {
	return connectionAuditShape(connection)
}

func (s *IntegrationApplicationService) connectorDefinition(key string) (integrationmodel.ConnectorSchema, bool) {
	if s.registry == nil {
		return integrationmodel.ConnectorSchema{}, false
	}
	key = strings.TrimSpace(key)
	for _, connector := range s.registry.Schema().Connectors {
		if strings.TrimSpace(connector.Key) == key {
			return connector, true
		}
	}
	return integrationmodel.ConnectorSchema{}, false
}

func resolveConnectorProvider(connector integrationmodel.ConnectorSchema, requested string) (string, error) {
	requested = strings.TrimSpace(requested)
	providers := connectorProviderKeys(connector)
	if requested != "" {
		for _, key := range providers {
			if key == requested {
				return requested, nil
			}
		}
		return "", badRequest("backend.integration.connection.provider_unsupported", "field_path", "provider_key", "connector", connector.Key, "provider", requested, "allowed", strings.Join(providers, ","))
	}
	switch len(providers) {
	case 0:
		return "", badRequest("backend.integration.connection.provider_unavailable", "field_path", "provider_key", "connector", connector.Key)
	case 1:
		return providers[0], nil
	default:
		return "", badRequest("backend.integration.connection.provider_required", "field_path", "provider_key", "connector", connector.Key, "allowed", strings.Join(providers, ","))
	}
}

func connectorProviderKeys(connector integrationmodel.ConnectorSchema) []string {
	seen := map[string]bool{}
	for _, provider := range connector.Providers {
		if key := strings.TrimSpace(provider.Key); key != "" {
			seen[key] = true
		}
	}
	keys := make([]string, 0, len(seen))
	for key := range seen {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
