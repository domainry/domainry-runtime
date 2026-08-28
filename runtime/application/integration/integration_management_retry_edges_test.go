package integration

import (
	"errors"
	"testing"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestScheduleIntegrationOutboxRetryEdges(t *testing.T) {
	principal := integrationManagementPrincipal(PermissionRetry)
	if _, err := NewIntegrationApplicationService(ApplicationDependencies{}).ScheduleIntegrationOutboxRetry(t.Context(), "message", integrationmodel.IntegrationOutboxRetryRequest{}, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("authorization error=%v", err)
	}
	if _, err := NewIntegrationApplicationService(ApplicationDependencies{}).ScheduleIntegrationOutboxRetry(t.Context(), "message", integrationmodel.IntegrationOutboxRetryRequest{}, integrationManagementPrincipal()); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("permission error=%v", err)
	}
	if _, err := NewIntegrationApplicationService(ApplicationDependencies{DeliveryRepository: workspaceAuthorizationDeliveryProbe{calls: new(int)}}).ScheduleIntegrationOutboxRetry(t.Context(), "message", integrationmodel.IntegrationOutboxRetryRequest{}, principal); apperror.KindOf(err) != apperror.KindInternal {
		t.Fatalf("unsupported reader error=%v", err)
	}
	repository := &integrationManagementDeliveryRepo{found: true, outboxes: []integrationmodel.IntegrationOutboxMessage{{ID: "message", WorkspaceID: "workspace", ConnectorKey: "__automation__", Status: "failed"}}}
	service := NewIntegrationApplicationService(ApplicationDependencies{DeliveryRepository: repository})
	repository.err = errIntegrationManagementTest
	if _, err := service.ScheduleIntegrationOutboxRetry(t.Context(), "message", integrationmodel.IntegrationOutboxRetryRequest{}, principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("reader error=%v", err)
	}
	repository.err, repository.found = nil, false
	if _, err := service.ScheduleIntegrationOutboxRetry(t.Context(), "message", integrationmodel.IntegrationOutboxRetryRequest{}, principal); apperror.CodeOf(err) != "backend.integration.outbox.not_found" {
		t.Fatalf("missing message error=%v", err)
	}
	repository.found = true
	for _, message := range []integrationmodel.IntegrationOutboxMessage{
		{ID: "message", Status: "quarantined"},
		{ID: "message", Status: "failed", Error: "backend.integration.outbox.outcome_uncertain"},
		{ID: "message", Status: "failed", ResponseRef: "receipt"},
	} {
		repository.outboxes[0] = message
		if _, err := service.ScheduleIntegrationOutboxRetry(t.Context(), "message", integrationmodel.IntegrationOutboxRetryRequest{}, principal); apperror.CodeOf(err) != "backend.integration.outbox.reconciliation_required" {
			t.Fatalf("reconciliation message=%#v err=%v", message, err)
		}
	}
	repository.outboxes[0] = integrationmodel.IntegrationOutboxMessage{
		ID: "message", WorkspaceID: "workspace", ConnectorKey: "__automation__", Status: "quarantined",
		Error: "backend.integration.google.http_status_429", ResponseRef: "http:429",
	}
	if message, err := service.ScheduleIntegrationOutboxRetry(t.Context(), "message", integrationmodel.IntegrationOutboxRetryRequest{}, principal); err != nil || message.Status != "queued" || message.NextAttemptAt == "" {
		t.Fatalf("explicit 429 quarantine was not retryable: message=%#v err=%v", message, err)
	}
	repository.outboxes[0] = integrationmodel.IntegrationOutboxMessage{ID: "message", Status: "sent"}
	if _, err := service.ScheduleIntegrationOutboxRetry(t.Context(), "message", integrationmodel.IntegrationOutboxRetryRequest{}, principal); apperror.CodeOf(err) != "backend.integration.outbox.not_retryable" {
		t.Fatalf("non-retryable error=%v", err)
	}
	repository.outboxes[0] = integrationmodel.IntegrationOutboxMessage{ID: "message", Status: "queued"}
	if _, err := service.ScheduleIntegrationOutboxRetry(t.Context(), "message", integrationmodel.IntegrationOutboxRetryRequest{}, principal); apperror.CodeOf(err) != "backend.integration.outbox.not_retryable" {
		t.Fatalf("fresh queued message became retryable: %v", err)
	}
	repository.outboxes[0] = integrationmodel.IntegrationOutboxMessage{
		ID: "message", WorkspaceID: "workspace", ConnectorKey: "__automation__", Status: "queued",
		Error: "backend.integration.sync_call.http_failed", NextAttemptAt: "2026-07-26T10:00:00Z",
	}
	if message, err := service.ScheduleIntegrationOutboxRetry(t.Context(), "message", integrationmodel.IntegrationOutboxRetryRequest{}, principal); err != nil || message.NextAttemptAt == "2026-07-26T10:00:00Z" {
		t.Fatalf("scheduled automatic retry was not expedited: message=%#v err=%v", message, err)
	}
	for _, status := range []string{"dead_letter", "cancelled"} {
		repository.outboxes[0] = integrationmodel.IntegrationOutboxMessage{ID: "message", WorkspaceID: "workspace", ConnectorKey: "__automation__", Status: status}
		if _, err := service.ScheduleIntegrationOutboxRetry(t.Context(), "message", integrationmodel.IntegrationOutboxRetryRequest{}, principal); err != nil {
			t.Fatalf("retryable status %q error=%v", status, err)
		}
	}
	repository.outboxes[0] = integrationmodel.IntegrationOutboxMessage{ID: "message", WorkspaceID: "workspace", ConnectorKey: "__automation__", Status: "failed", NextAttemptAt: "already"}
	if message, err := service.ScheduleIntegrationOutboxRetry(t.Context(), "message", integrationmodel.IntegrationOutboxRetryRequest{}, principal); err != nil || message.NextAttemptAt != "already" {
		t.Fatalf("already scheduled message=%#v err=%v", message, err)
	}
	repository.outboxes[0] = integrationmodel.IntegrationOutboxMessage{ID: "message", WorkspaceID: "workspace", ConnectorKey: "__automation__", Status: "failed"}
	if message, err := service.ScheduleIntegrationOutboxRetry(t.Context(), " message ", integrationmodel.IntegrationOutboxRetryRequest{DelaySeconds: 2, Error: " retry "}, principal); err != nil || message.NextAttemptAt == "" {
		t.Fatalf("automation retry message=%#v err=%v", message, err)
	}

	config := &independentConfigRepository{connections: map[string]integrationmodel.IntegrationConnection{"connection": {Key: "connection", WorkspaceID: "workspace", ConnectorKey: "connector", ProviderKey: "provider", Status: "active", Config: map[string]any{}}}, secrets: map[string]integrationmodel.IntegrationSecret{}, materials: map[string]string{}}
	registry := NewConnectorRegistry(integrationAdapterSchema(1))
	registerTestRegistryProvider(registry, "connector", "provider", &adapterEdge{})
	repository.outboxes[0] = integrationmodel.IntegrationOutboxMessage{ID: "message", WorkspaceID: "workspace", ConnectorKey: "connector", ConnectionKey: "connection", Operation: "send", Status: "failed", Payload: map[string]any{}}
	service = NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: config, DeliveryRepository: repository, Registry: registry})
	if message, err := service.ScheduleIntegrationOutboxRetry(t.Context(), "message", integrationmodel.IntegrationOutboxRetryRequest{}, principal); err != nil || message.NextAttemptAt == "" {
		t.Fatalf("adapter retry message=%#v err=%v", message, err)
	}
	config.connections["connection"] = integrationmodel.IntegrationConnection{Key: "connection", WorkspaceID: "workspace", ConnectorKey: "connector", ProviderKey: "provider", Status: "disabled"}
	if _, err := service.ScheduleIntegrationOutboxRetry(t.Context(), "message", integrationmodel.IntegrationOutboxRetryRequest{}, principal); apperror.CodeOf(err) != "backend.integration.outbox.connection_unavailable" {
		t.Fatalf("connection validation error=%v", err)
	}
	config.connections["connection"] = integrationmodel.IntegrationConnection{Key: "connection", WorkspaceID: "workspace", ConnectorKey: "connector", ProviderKey: "provider", Status: "active", Config: map[string]any{}, SecretRefs: map[string]string{"token": "secret:missing"}}
	if _, err := service.ScheduleIntegrationOutboxRetry(t.Context(), "message", integrationmodel.IntegrationOutboxRetryRequest{}, principal); apperror.CodeOf(err) != "backend.integration.secret.unavailable" {
		t.Fatalf("secret validation error=%v", err)
	}
	config.connections["connection"] = integrationmodel.IntegrationConnection{Key: "connection", WorkspaceID: "workspace", ConnectorKey: "connector", ProviderKey: "provider", Status: "active", Config: map[string]any{}}
	repository.outboxes[0].Operation = "missing"
	if _, err := service.ScheduleIntegrationOutboxRetry(t.Context(), "message", integrationmodel.IntegrationOutboxRetryRequest{}, principal); err == nil {
		t.Fatal("invalid operation accepted")
	}
	repository.outboxes[0].Operation = "send"
	repository.scheduleErr = errIntegrationManagementTest
	if _, err := service.ScheduleIntegrationOutboxRetry(t.Context(), "message", integrationmodel.IntegrationOutboxRetryRequest{}, principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("schedule error=%v", err)
	}
}
