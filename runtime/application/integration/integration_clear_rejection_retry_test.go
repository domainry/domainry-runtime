package integration

import (
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestPrepareRegisteredOutboxCallRetriesOnlyExplicitNonEffectRejection(t *testing.T) {
	registry := NewConnectorRegistry(integrationAdapterSchema(1))
	registerTestRegistryProvider(registry, "connector", "provider", &adapterEdge{})
	service := NewIntegrationApplicationService(ApplicationDependencies{Registry: registry})
	connection := integrationmodel.IntegrationConnection{ConnectorKey: "connector", ProviderKey: "provider", Status: "active", Config: map[string]any{}}
	message := integrationmodel.IntegrationOutboxMessage{
		ID: "message", ConnectorKey: "connector", ConnectionKey: "connection", Operation: "unsafe", AttemptCount: 1,
		Error: "backend.integration.google.http_status_429", ResponseRef: "http:429",
	}

	if prepared, err := service.prepareRegisteredOutboxCall(t.Context(), message, connection, principalmodel.Principal{}, nil); err != nil || prepared.Operation != "unsafe" {
		t.Fatalf("explicit 429 rejection was not safely retryable: prepared=%#v err=%v", prepared, err)
	}
	message.Error = "backend.integration.google.http_status_503"
	message.ResponseRef = "http:503"
	if _, err := service.prepareRegisteredOutboxCall(t.Context(), message, connection, principalmodel.Principal{}, nil); apperror.CodeOf(err) != "backend.integration.provider.retry_strategy_required" {
		t.Fatalf("uncertain 503 retry error=%v", err)
	}
}

func TestScheduleIntegrationOutboxRetryPreservesExplicitProviderRejection(t *testing.T) {
	const providerError = "backend.integration.google.http_status_429"
	repository := &integrationManagementDeliveryRepo{
		found: true,
		outboxes: []integrationmodel.IntegrationOutboxMessage{{
			ID: "message", WorkspaceID: "workspace", ConnectorKey: "connector", ConnectionKey: "connection",
			Operation: "unsafe", Status: "dead_letter", Error: providerError, ResponseRef: "http:429", Payload: map[string]any{},
		}},
	}
	config := &independentConfigRepository{
		connections: map[string]integrationmodel.IntegrationConnection{
			"connection": {Key: "connection", WorkspaceID: "workspace", ConnectorKey: "connector", ProviderKey: "provider", Status: "active", Config: map[string]any{}},
		},
		secrets:   map[string]integrationmodel.IntegrationSecret{},
		materials: map[string]string{},
	}
	registry := NewConnectorRegistry(integrationAdapterSchema(1))
	registerTestRegistryProvider(registry, "connector", "provider", &adapterEdge{})
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: config, DeliveryRepository: repository, Registry: registry})

	message, err := service.ScheduleIntegrationOutboxRetry(t.Context(), "message", integrationmodel.IntegrationOutboxRetryRequest{Error: "operator requested retry"}, integrationManagementPrincipal(PermissionRetry))
	if err != nil {
		t.Fatalf("schedule explicit rejection retry: %v", err)
	}
	if message.Error != providerError {
		t.Fatalf("provider rejection evidence was overwritten: got=%q want=%q", message.Error, providerError)
	}
}
