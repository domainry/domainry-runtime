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

type cancellingOutboxFailureAdapter struct {
	cancel context.CancelFunc
}

func (a cancellingOutboxFailureAdapter) Call(context.Context, integrationcontract.CallRequest) (integrationcontract.CallResult, error) {
	a.cancel()
	return integrationcontract.CallResult{ResponseRef: "oauth:refresh_failed"}, errors.New("refresh failed")
}

func TestFinalConnectionEvidenceAndSecretLifecycleConditions(t *testing.T) {
	principal := integrationManagementPrincipal(PermissionSecretManage)
	connectionRepo := &connectionResolutionRepo{}
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: connectionRepo})
	connection := integrationmodel.IntegrationConnection{Key: "connection", WorkspaceID: "workspace", Status: "active"}
	if saved, err := service.PersistConnectionTestStatus(t.Context(), connection, "degraded", principal); err != nil || saved.Status != "degraded" {
		t.Fatalf("degraded connection=%#v err=%v", saved, err)
	}

	secretRepo := newIntegrationSecretCommandRepository()
	secretRepo.secrets["secret"] = integrationmodel.IntegrationSecret{Key: "secret", WorkspaceID: "workspace", Status: "active", ExpiresAt: "2020-01-01T00:00:00Z"}
	service = NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: secretRepo})
	rotated, err := service.RotateIntegrationSecret(t.Context(), "secret", integrationmodel.IntegrationSecretUpsertRequest{ValueRef: "secret:other", Status: "disabled"}, principal)
	if err != nil || rotated.Status != "disabled" {
		t.Fatalf("disabled rotation=%#v err=%v", rotated, err)
	}
	if valueRef, fingerprint, err := secretMaterial(integrationmodel.IntegrationSecretUpsertRequest{ValueRef: "secret:other"}); err != nil || valueRef != "secret:other" || fingerprint == "" {
		t.Fatalf("secret material ref=%q fingerprint=%q err=%v", valueRef, fingerprint, err)
	}
	secretRepo.secrets["secret"] = integrationmodel.IntegrationSecret{Key: "secret", WorkspaceID: "workspace", Status: "active"}
	transitioned, err := service.transitionIntegrationSecret(t.Context(), "secret", "disabled", principal)
	if err != nil || transitioned.Status != "disabled" || transitioned.ExpiresAt != "" || transitioned.RevokedAt != "" {
		t.Fatalf("direct transition=%#v err=%v", transitioned, err)
	}
}

func TestFinalEventExecutionContextConditions(t *testing.T) {
	connection := integrationmodel.IntegrationConnection{Key: "connection", WorkspaceID: "workspace", ConnectorKey: "connector", ProviderKey: "provider"}
	repository := &independentConfigRepository{connections: map[string]integrationmodel.IntegrationConnection{"connection": connection}, secrets: map[string]integrationmodel.IntegrationSecret{}, materials: map[string]string{}}
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository})
	event := integrationmodel.IntegrationEvent{WorkspaceID: "workspace"}
	for _, raw := range []map[string]any{
		{"connection_key": "connection"},
		{"connection_key": "connection", "connector_key": "connector"},
		{"connection_key": "missing", "connector_key": "connector", "provider_key": "provider"},
		{"connection_key": "connection", "connector_key": "other", "provider_key": "provider"},
		{"connection_key": "connection", "connector_key": "connector", "provider_key": "other"},
	} {
		event.Payload = map[string]any{EventContextKey: raw}
		if _, executionEvent, ok := service.EventExecutionContext(t.Context(), event); ok || executionEvent.Payload[EventContextKey] != nil {
			t.Fatalf("invalid context raw=%#v event=%#v ok=%v", raw, executionEvent, ok)
		}
	}
}

func TestFinalProviderSecretAndDeliveryResolutionConditions(t *testing.T) {
	secret := integrationmodel.IntegrationSecret{Key: "token", WorkspaceID: "workspace", Kind: "api_key", Status: "active", LastTestStatus: "pending"}
	repository := &independentConfigRepository{connections: map[string]integrationmodel.IntegrationConnection{}, secrets: map[string]integrationmodel.IntegrationSecret{"token": secret}, materials: map[string]string{}}
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository})
	connector := integrationmodel.ConnectorSchema{Key: "connector", Providers: []integrationmodel.ConnectorProviderSchema{
		{Key: "other"},
		{Key: "provider", SecretFields: []definitionmodel.FieldSchema{{Key: "token", Config: map[string]any{"credential_kind": "api_key", "test_requirement": "optional"}}}},
	}}
	if err := service.ValidateProviderSecretRefs(t.Context(), connector, "provider", "active", map[string]string{"token": "secret:token"}, "workspace"); err != nil {
		t.Fatalf("optional provider secret error=%v", err)
	}

	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: "connector", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "provider"}}}}})
	service = NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, Registry: registry})
	if _, err := service.ResolveIntegrationDeliveryProvider(t.Context(), "connector", "missing", "provider", "workspace"); err == nil {
		t.Fatal("missing delivery connection accepted")
	}
}

func TestFinalOutboxCallCancellationCondition(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	connection := integrationmodel.IntegrationConnection{Key: "primary", WorkspaceID: "workspace", ConnectorKey: "email", ProviderKey: "provider", Status: "active"}
	service := integrationOutboxAdapterService(connection, cancellingOutboxFailureAdapter{cancel: cancel}, &independentDeliveryRepository{})
	message := integrationmodel.IntegrationOutboxMessage{ID: "message", WorkspaceID: "workspace", ConnectorKey: "email", ConnectionKey: "primary", Operation: "send"}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "worker"}}
	if _, err := service.SendAdapterOutboxMessage(ctx, message, principal); err == nil {
		t.Fatal("cancelled adapter call succeeded")
	}
}
