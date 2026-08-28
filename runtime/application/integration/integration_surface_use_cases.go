package integration

import (
	"context"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type TenantAdminConnectorProviderDTO struct {
	Key           string                              `json:"key"`
	Name          string                              `json:"name,omitempty"`
	Description   string                              `json:"description,omitempty"`
	OperationKeys []string                            `json:"operation_keys,omitempty"`
	Availability  TenantAdminConnectorAvailabilityDTO `json:"availability"`
}

type TenantAdminConnectorAvailabilityDTO struct {
	CatalogAvailable bool `json:"catalog_available"`
	Compiled         bool `json:"compiled"`
	Enabled          bool `json:"enabled"`
	Bound            bool `json:"bound"`
	Configured       bool `json:"configured"`
	Healthy          bool `json:"healthy"`
	Degraded         bool `json:"degraded"`
}

type TenantAdminConnectorOperationDTO struct {
	Key                  string `json:"key"`
	Name                 string `json:"name,omitempty"`
	Description          string `json:"description,omitempty"`
	SideEffect           string `json:"side_effect,omitempty"`
	IdempotencySupported bool   `json:"idempotency_supported"`
	TestSupported        bool   `json:"test_supported"`
	DryRunSupported      bool   `json:"dry_run_supported"`
}

type TenantAdminConnectorDTO struct {
	Key          string                              `json:"key"`
	Name         string                              `json:"name,omitempty"`
	Description  string                              `json:"description,omitempty"`
	Status       string                              `json:"status,omitempty"`
	Providers    []TenantAdminConnectorProviderDTO   `json:"providers"`
	Operations   []TenantAdminConnectorOperationDTO  `json:"operation_allowlist"`
	ConfigFields []string                            `json:"config_fields,omitempty"`
	SecretRefs   []string                            `json:"secret_ref_names,omitempty"`
	Availability TenantAdminConnectorAvailabilityDTO `json:"availability"`
}

type TenantAdminIntegrationConnectionDTO struct {
	Key          string            `json:"key"`
	ConnectorKey string            `json:"connector_key"`
	ProviderKey  string            `json:"provider_key"`
	Name         string            `json:"name,omitempty"`
	Status       string            `json:"status"`
	Config       map[string]any    `json:"config,omitempty"`
	SecretRefs   map[string]string `json:"secret_refs,omitempty"`
	CreatedAt    string            `json:"created_at,omitempty"`
	UpdatedAt    string            `json:"updated_at,omitempty"`
}

type TenantAdminIntegrationSecretRefDTO struct {
	Key            string `json:"key"`
	Kind           string `json:"kind"`
	Status         string `json:"status"`
	Configured     bool   `json:"configured"`
	Description    string `json:"description,omitempty"`
	ExpiresAt      string `json:"expires_at,omitempty"`
	RotatedAt      string `json:"rotated_at,omitempty"`
	RevokedAt      string `json:"revoked_at,omitempty"`
	LastTestedAt   string `json:"last_tested_at,omitempty"`
	LastTestStatus string `json:"last_test_status,omitempty"`
}

type TenantAdminIntegrationCatalogDTO struct {
	Connectors  []TenantAdminConnectorDTO             `json:"connectors"`
	Connections []TenantAdminIntegrationConnectionDTO `json:"connections"`
}

type OpsIntegrationProviderHealthDTO struct {
	ConnectorKey string `json:"connector_key"`
	ProviderKey  string `json:"provider_key"`
	Readiness    string `json:"readiness"`
}

type OpsIntegrationEventDTO struct {
	ID             string `json:"id"`
	Provider       string `json:"provider"`
	EventType      string `json:"event_type"`
	Status         string `json:"status"`
	Error          string `json:"error,omitempty"`
	AttemptCount   int    `json:"attempt_count"`
	NextRetryAt    string `json:"next_retry_at,omitempty"`
	LastAttemptAt  string `json:"last_attempt_at,omitempty"`
	LeaseOwner     string `json:"lease_owner,omitempty"`
	LeaseExpiresAt string `json:"lease_expires_at,omitempty"`
	FencingToken   int64  `json:"fencing_token,omitempty"`
	ReceivedAt     string `json:"received_at,omitempty"`
	UpdatedAt      string `json:"updated_at,omitempty"`
}

type OpsIntegrationInvocationDTO struct {
	ID            string `json:"id"`
	ConnectorKey  string `json:"connector_key"`
	ProviderKey   string `json:"provider_key,omitempty"`
	ConnectionKey string `json:"connection_key,omitempty"`
	Operation     string `json:"operation"`
	Status        string `json:"status"`
	DurationMS    int64  `json:"duration_ms,omitempty"`
	Error         string `json:"error,omitempty"`
	EventID       string `json:"event_id,omitempty"`
	CreatedAt     string `json:"created_at,omitempty"`
	UpdatedAt     string `json:"updated_at,omitempty"`
}

type OpsIntegrationOutboxDTO struct {
	ID             string `json:"id"`
	ConnectorKey   string `json:"connector_key"`
	ConnectionKey  string `json:"connection_key,omitempty"`
	Operation      string `json:"operation"`
	Status         string `json:"status"`
	Error          string `json:"error,omitempty"`
	EventID        string `json:"event_id,omitempty"`
	AttemptCount   int    `json:"attempt_count"`
	NextAttemptAt  string `json:"next_attempt_at,omitempty"`
	LastAttemptAt  string `json:"last_attempt_at,omitempty"`
	LeaseOwner     string `json:"lease_owner,omitempty"`
	LeaseExpiresAt string `json:"lease_expires_at,omitempty"`
	FencingToken   int64  `json:"fencing_token,omitempty"`
	CreatedAt      string `json:"created_at,omitempty"`
	UpdatedAt      string `json:"updated_at,omitempty"`
}

type OpsIntegrationActivityDTO struct {
	ProviderHealth []OpsIntegrationProviderHealthDTO `json:"provider_health"`
	Events         []OpsIntegrationEventDTO          `json:"events"`
	Invocations    []OpsIntegrationInvocationDTO     `json:"invocations"`
	Outbox         []OpsIntegrationOutboxDTO         `json:"outbox"`
}

type OpsIntegrationActivityQuery struct {
	Kind         string
	Status       string
	ConnectorKey string
	Provider     string
	ResourceID   string
	Search       string
	Limit        int
}

func (s *IntegrationApplicationService) TenantAdminIntegrationCatalog(ctx context.Context, principal principalmodel.Principal) (TenantAdminIntegrationCatalogDTO, error) {
	connectors, err := s.IntegrationConnectorCatalog(ctx, principal)
	if err != nil {
		return TenantAdminIntegrationCatalogDTO{}, err
	}
	connections, err := s.ListIntegrationConnections(ctx, principal)
	if err != nil {
		return TenantAdminIntegrationCatalogDTO{}, err
	}
	result := TenantAdminIntegrationCatalogDTO{
		Connectors:  make([]TenantAdminConnectorDTO, 0, len(connectors)),
		Connections: make([]TenantAdminIntegrationConnectionDTO, 0, len(connections)),
	}
	_, compiledProviders := s.registry.Catalog()
	for _, connector := range connectors {
		result.Connectors = append(result.Connectors, projectTenantAdminConnector(connector, connections, compiledProviders))
	}
	for _, connection := range connections {
		result.Connections = append(result.Connections, ProjectTenantAdminIntegrationConnection(connection))
	}
	return result, nil
}

func (s *IntegrationApplicationService) TenantAdminIntegrationSecrets(ctx context.Context, principal principalmodel.Principal) ([]TenantAdminIntegrationSecretRefDTO, error) {
	secrets, err := s.ListIntegrationSecrets(ctx, principal)
	if err != nil {
		return nil, err
	}
	out := make([]TenantAdminIntegrationSecretRefDTO, 0, len(secrets))
	for _, secret := range secrets {
		out = append(out, ProjectTenantAdminIntegrationSecretRef(secret))
	}
	return out, nil
}

func (s *IntegrationApplicationService) OpsIntegrationActivity(ctx context.Context, principal principalmodel.Principal) (OpsIntegrationActivityDTO, error) {
	return s.OpsIntegrationActivityWithQuery(ctx, OpsIntegrationActivityQuery{}, principal)
}

func (s *IntegrationApplicationService) OpsIntegrationActivityWithQuery(ctx context.Context, query OpsIntegrationActivityQuery, principal principalmodel.Principal) (OpsIntegrationActivityDTO, error) {
	if err := AuthorizeOpsIntegrationActivity(principal); err != nil {
		return OpsIntegrationActivityDTO{}, err
	}
	kind := strings.TrimSpace(query.Kind)
	switch kind {
	case "", "events", "invocations", "outbox":
	default:
		return OpsIntegrationActivityDTO{}, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.integration.activity_kind_invalid"}
	}
	limit := query.Limit
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	connectors, err := s.integrationConnectorCatalogForWorkspace(ctx, principalWorkspaceID(principal))
	if err != nil {
		return OpsIntegrationActivityDTO{}, err
	}
	result := OpsIntegrationActivityDTO{
		ProviderHealth: []OpsIntegrationProviderHealthDTO{},
		Events:         []OpsIntegrationEventDTO{},
		Invocations:    []OpsIntegrationInvocationDTO{},
		Outbox:         []OpsIntegrationOutboxDTO{},
	}
	for _, connector := range connectors {
		for _, provider := range connector.Providers {
			result.ProviderHealth = append(result.ProviderHealth, OpsIntegrationProviderHealthDTO{ConnectorKey: connector.Key, ProviderKey: provider.Key, Readiness: provider.Readiness})
		}
	}
	if kind == "" || kind == "events" {
		events, listErr := s.ListIntegrationEvents(ctx, query.Provider, query.Status, limit, principal)
		if listErr != nil {
			return OpsIntegrationActivityDTO{}, listErr
		}
		for _, event := range events {
			projected := ProjectOpsIntegrationEvent(event)
			if opsIntegrationActivityMatches(projected.ID, query.ResourceID, query.Search, projected.Provider, projected.EventType, projected.Status, projected.Error) {
				result.Events = append(result.Events, projected)
			}
		}
	}
	if kind == "" || kind == "invocations" {
		invocations, listErr := s.ListIntegrationInvocations(ctx, query.ConnectorKey, "", "", query.Status, query.Provider, "", limit, principal)
		if listErr != nil {
			return OpsIntegrationActivityDTO{}, listErr
		}
		for _, invocation := range invocations {
			projected := ProjectOpsIntegrationInvocation(invocation)
			if opsIntegrationActivityMatches(projected.ID, query.ResourceID, query.Search, projected.ConnectorKey, projected.ProviderKey, projected.ConnectionKey, projected.Operation, projected.Status, projected.Error) {
				result.Invocations = append(result.Invocations, projected)
			}
		}
	}
	if kind == "" || kind == "outbox" {
		outbox, listErr := s.ListIntegrationOutboxMessages(ctx, query.ConnectorKey, query.Status, limit, principal)
		if listErr != nil {
			return OpsIntegrationActivityDTO{}, listErr
		}
		for _, message := range outbox {
			projected := ProjectOpsIntegrationOutbox(message)
			if opsIntegrationActivityMatches(projected.ID, query.ResourceID, query.Search, projected.ConnectorKey, projected.ConnectionKey, projected.Operation, projected.Status, projected.Error) {
				result.Outbox = append(result.Outbox, projected)
			}
		}
	}
	return result, nil
}

func opsIntegrationActivityMatches(id, resourceID, search string, values ...string) bool {
	if expected := strings.TrimSpace(resourceID); expected != "" && id != expected {
		return false
	}
	needle := strings.ToLower(strings.TrimSpace(search))
	if needle == "" {
		return true
	}
	if strings.Contains(strings.ToLower(id), needle) {
		return true
	}
	for _, value := range values {
		if strings.Contains(strings.ToLower(value), needle) {
			return true
		}
	}
	return false
}

func AuthorizeOpsIntegrationActivity(principal principalmodel.Principal) error {
	if !integrationHasExactPermission(principal, PermissionAuditView) {
		return &apperror.AppError{Kind: apperror.KindForbidden, Code: "auth.permission_denied"}
	}
	return nil
}

func AuthorizeOpsIntegrationRetry(principal principalmodel.Principal) error {
	if !integrationHasExactPermission(principal, PermissionRetry) {
		return &apperror.AppError{Kind: apperror.KindForbidden, Code: "auth.permission_denied"}
	}
	return nil
}

func integrationHasExactPermission(principal principalmodel.Principal, permission string) bool {
	if !principal.Known {
		return false
	}
	return principal.HasExactPermission(permission)
}

func ProjectTenantAdminIntegrationConnection(value integrationmodel.IntegrationConnection) TenantAdminIntegrationConnectionDTO {
	return TenantAdminIntegrationConnectionDTO{
		Key: value.Key, ConnectorKey: value.ConnectorKey, ProviderKey: value.ProviderKey, Name: value.Name, Status: value.Status,
		Config: cloneMap(value.Config), SecretRefs: integrationSurfaceCloneStringMap(value.SecretRefs), CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func ProjectTenantAdminIntegrationSecretRef(value integrationmodel.IntegrationSecret) TenantAdminIntegrationSecretRefDTO {
	return TenantAdminIntegrationSecretRefDTO{
		Key: value.Key, Kind: value.Kind, Status: value.Status, Configured: strings.TrimSpace(value.ValueRef) != "", Description: value.Description,
		ExpiresAt: value.ExpiresAt, RotatedAt: value.RotatedAt, RevokedAt: value.RevokedAt,
		LastTestedAt: value.LastTestedAt, LastTestStatus: value.LastTestStatus,
	}
}

func ProjectOpsIntegrationEvent(value integrationmodel.IntegrationEvent) OpsIntegrationEventDTO {
	return OpsIntegrationEventDTO{
		ID: value.ID, Provider: value.Provider, EventType: value.EventType, Status: value.Status, Error: value.Error,
		AttemptCount: value.AttemptCount, NextRetryAt: value.NextRetryAt, LastAttemptAt: value.LastAttemptAt,
		LeaseOwner: value.LeaseOwner, LeaseExpiresAt: value.LeaseExpiresAt, FencingToken: value.FencingToken,
		ReceivedAt: value.ReceivedAt, UpdatedAt: value.UpdatedAt,
	}
}

func ProjectOpsIntegrationInvocation(value integrationmodel.IntegrationInvocation) OpsIntegrationInvocationDTO {
	return OpsIntegrationInvocationDTO{
		ID: value.ID, ConnectorKey: value.ConnectorKey, ProviderKey: value.ProviderKey, ConnectionKey: value.ConnectionKey,
		Operation: value.Operation, Status: value.Status, DurationMS: value.DurationMS, Error: value.Error,
		EventID: value.EventID, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func ProjectOpsIntegrationOutbox(value integrationmodel.IntegrationOutboxMessage) OpsIntegrationOutboxDTO {
	return OpsIntegrationOutboxDTO{
		ID: value.ID, ConnectorKey: value.ConnectorKey, ConnectionKey: value.ConnectionKey, Operation: value.Operation,
		Status: value.Status, Error: value.Error, EventID: value.EventID, AttemptCount: value.AttemptCount,
		NextAttemptAt: value.NextAttemptAt, LastAttemptAt: value.LastAttemptAt, LeaseOwner: value.LeaseOwner,
		LeaseExpiresAt: value.LeaseExpiresAt, FencingToken: value.FencingToken, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}

func integrationSurfaceCloneStringMap(input map[string]string) map[string]string {
	if input == nil {
		return nil
	}
	out := make(map[string]string, len(input))
	for key, value := range input {
		out[key] = value
	}
	return out
}
