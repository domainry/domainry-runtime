package integration

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type replayReadinessAdapter struct {
	validationError error
}

func (replayReadinessAdapter) Call(context.Context, integrationcontract.CallRequest) (integrationcontract.CallResult, error) {
	return integrationcontract.CallResult{}, nil
}

func (a replayReadinessAdapter) ValidateConfig(integrationmodel.IntegrationConnection) error {
	return a.validationError
}

type replayReadinessHandler struct{}

func (replayReadinessHandler) ProcessIntegrationEvent(context.Context, integrationmodel.IntegrationEvent, principalmodel.Principal) (EventProcessDecision, error) {
	return EventProcessDecision{}, nil
}

func TestValidateIntegrationEventReplayReadiness(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a"}}
	baseEvent := integrationmodel.IntegrationEvent{
		ID:          "event-1",
		WorkspaceID: "workspace-a",
		Provider:    "crm",
		EventType:   "updated",
		Payload:     map[string]any{},
	}
	connectionContext := map[string]any{
		"connection_key": "connection-1",
		"connector_key":  "crm",
		"provider_key":   "crm",
	}

	newService := func(
		connections map[string]integrationmodel.IntegrationConnection,
		schema integrationmodel.IntegrationSchema,
	) (*IntegrationApplicationService, *ConnectorRegistry) {
		repository := &independentConfigRepository{
			connections: connections,
			secrets:     map[string]integrationmodel.IntegrationSecret{},
			materials:   map[string]string{},
		}
		registry := NewConnectorRegistry(schema)
		return NewIntegrationApplicationService(ApplicationDependencies{
			ConfigRepository: repository,
			Registry:         registry,
		}), registry
	}

	t.Run("workspace mismatch", func(t *testing.T) {
		service, _ := newService(nil, integrationmodel.IntegrationSchema{})
		event := baseEvent
		event.WorkspaceID = "workspace-b"
		assertIntegrationErrorCode(t, service.validateIntegrationEventReplayReadiness(t.Context(), event, principal), "auth.permission_denied")
	})

	t.Run("declared connection unavailable", func(t *testing.T) {
		service, _ := newService(map[string]integrationmodel.IntegrationConnection{}, integrationmodel.IntegrationSchema{})
		event := baseEvent
		event.Payload = map[string]any{EventContextKey: connectionContext}
		assertIntegrationErrorCode(t, service.validateIntegrationEventReplayReadiness(t.Context(), event, principal), "backend.integration.event.replay_connection_unavailable")
	})

	t.Run("connection cannot send", func(t *testing.T) {
		connection := replayReadinessConnection("configured")
		service, _ := newService(map[string]integrationmodel.IntegrationConnection{connection.Key: connection}, integrationmodel.IntegrationSchema{})
		event := baseEvent
		event.Payload = map[string]any{EventContextKey: connectionContext}
		assertIntegrationErrorCode(t, service.validateIntegrationEventReplayReadiness(t.Context(), event, principal), "backend.integration.event.replay_connection_unavailable")
	})

	t.Run("adapter config invalid", func(t *testing.T) {
		connection := replayReadinessConnection("active")
		service, registry := newService(map[string]integrationmodel.IntegrationConnection{connection.Key: connection}, integrationmodel.IntegrationSchema{})
		registerTestRegistryProvider(registry, "crm", "crm", replayReadinessAdapter{validationError: errors.New("backend.integration.connection.invalid_replay_config: details")})
		event := baseEvent
		event.Payload = map[string]any{EventContextKey: connectionContext}
		assertIntegrationErrorCode(t, service.validateIntegrationEventReplayReadiness(t.Context(), event, principal), "backend.integration.connection.invalid_replay_config")
	})

	t.Run("secret resolution fails", func(t *testing.T) {
		connection := replayReadinessConnection("verified")
		connection.SecretRefs = map[string]string{"token": "literal-secret"}
		service, _ := newService(map[string]integrationmodel.IntegrationConnection{connection.Key: connection}, integrationmodel.IntegrationSchema{})
		event := baseEvent
		event.Payload = map[string]any{EventContextKey: connectionContext}
		assertIntegrationErrorCode(t, service.validateIntegrationEventReplayReadiness(t.Context(), event, principal), "backend.integration.secret_ref.must_be_reference")
	})

	t.Run("ready connection and registered handler", func(t *testing.T) {
		t.Setenv("CRM_TOKEN", "token")
		connection := replayReadinessConnection("active")
		connection.SecretRefs = map[string]string{"token": "env:CRM_TOKEN"}
		schema := integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: "crm", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "crm", SecretFields: []definitionmodel.FieldSchema{{Key: "token", Type: "text"}}}}}}}
		service, registry := newService(map[string]integrationmodel.IntegrationConnection{connection.Key: connection}, schema)
		registerTestRegistryProvider(registry, "crm", "crm", replayReadinessAdapter{})
		registry.RegisterEventHandler("crm", replayReadinessHandler{})
		event := baseEvent
		event.Payload = map[string]any{EventContextKey: connectionContext}
		if err := service.validateIntegrationEventReplayReadiness(t.Context(), event, principal); err != nil {
			t.Fatalf("readiness error = %v", err)
		}
	})

	t.Run("mapping validation fails", func(t *testing.T) {
		service, _ := newService(nil, integrationmodel.IntegrationSchema{EventMappings: []integrationmodel.IntegrationEventMappingSchema{{
			Key: "crm-update", Provider: "crm", EventType: "updated", TargetType: "workflow",
		}}})
		assertIntegrationErrorCode(t, service.validateIntegrationEventReplayReadiness(t.Context(), baseEvent, principal), "backend.integration.event_mapping.missing_workflow")
	})

	t.Run("handler and mapping unavailable", func(t *testing.T) {
		service, _ := newService(nil, integrationmodel.IntegrationSchema{})
		assertIntegrationErrorCode(t, service.validateIntegrationEventReplayReadiness(t.Context(), baseEvent, principal), "backend.integration.event.replay_handler_not_ready")
	})

	t.Run("mapping ready", func(t *testing.T) {
		service, _ := newService(nil, integrationmodel.IntegrationSchema{EventMappings: []integrationmodel.IntegrationEventMappingSchema{{
			Key: "crm-update", Provider: "crm", EventType: "updated", TargetType: "owner_task",
		}}})
		if err := service.validateIntegrationEventReplayReadiness(t.Context(), baseEvent, principal); err != nil {
			t.Fatalf("readiness error = %v", err)
		}
	})
}

func replayReadinessConnection(status string) integrationmodel.IntegrationConnection {
	return integrationmodel.IntegrationConnection{
		Key:          "connection-1",
		WorkspaceID:  "workspace-a",
		ConnectorKey: "crm",
		ProviderKey:  "crm",
		Status:       status,
	}
}

func assertIntegrationErrorCode(t *testing.T, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("error = nil, want code %q", want)
	}
	if got := testErrorCode(err); got != want {
		t.Fatalf("error code = %q, want %q (error: %v)", got, want, err)
	}
}
