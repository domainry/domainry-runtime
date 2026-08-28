// Integration application service credential lifecycle tests.
package integration

import (
	"context"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"testing"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func TestCredentialLifecyclePersistsLastTestRotateExpireAndRevoke(t *testing.T) {
	repository := &independentConfigRepository{connections: map[string]integrationmodel.IntegrationConnection{}, secrets: map[string]integrationmodel.IntegrationSecret{}, materials: map[string]string{}}
	delivery := &independentDeliveryRepository{}
	connector := integrationmodel.ConnectorSchema{Key: "probe", Type: "http", Provider: "probe", SecretRefs: []string{"token"}, Config: map[string]any{"required_secret_refs": []any{"token"}}, Providers: []integrationmodel.ConnectorProviderSchema{{Key: "probe", SecretFields: []definitionmodel.FieldSchema{{Key: "token", Type: "text", Required: true, Config: map[string]any{"credential_kind": "bearer_token"}}}}}, Operations: []integrationmodel.ConnectorOperationSchema{{Key: "test_connection", Method: "POST", SideEffect: "read"}}}
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{connector}})
	application := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, DeliveryRepository: delivery, Registry: registry})
	registerTestProviderAdapter(application, "probe", "probe", lifecycleAdapter{})
	admin := integrationWorkspaceAdmin("admin", "workspace")
	expires := time.Now().UTC().Add(time.Hour).Format(time.RFC3339)
	secret, err := application.UpsertIntegrationSecret(t.Context(), "token", integrationmodel.IntegrationSecretUpsertRequest{Kind: "bearer_token", Value: "secret-value", ExpiresAt: expires}, admin)
	if err != nil || secret.ExpiresAt != expires {
		t.Fatalf("secret=%+v error=%v", secret, err)
	}
	if _, err := application.UpsertIntegrationConnection(t.Context(), "probe", integrationmodel.IntegrationConnectionUpsertRequest{ConnectorKey: "probe", ProviderKey: "probe", Status: "configured", Config: map[string]any{"url": "https://example.invalid"}, SecretRefs: map[string]string{"token": "secret:token"}}, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := application.TestConnectorOperation(t.Context(), "probe", ConnectorOperationTestRequest{Operation: "test_connection", Confirm: true}, admin); err != nil {
		t.Fatal(err)
	}
	tested := repository.secrets["token"]
	if tested.LastTestStatus != "succeeded" || tested.LastTestedAt == "" || tested.LastTestError != "" {
		t.Fatalf("test evidence=%+v", tested)
	}

	revoked, err := application.RevokeIntegrationSecret(t.Context(), "token", admin)
	if err != nil || revoked.Status != "revoked" || revoked.RevokedAt == "" {
		t.Fatalf("revoked=%+v error=%v", revoked, err)
	}
	if _, err := application.TestConnectorOperation(t.Context(), "probe", ConnectorOperationTestRequest{Operation: "test_connection", Confirm: true}, admin); testErrorCode(err) != "backend.integration.secret.unavailable" {
		t.Fatalf("revoked credential used: %v", err)
	}
	rotated, err := application.RotateIntegrationSecret(t.Context(), "token", integrationmodel.IntegrationSecretUpsertRequest{Value: "new-value", ExpiresAt: expires}, admin)
	if err != nil || rotated.Status != "active" || rotated.RotatedAt == "" || rotated.RevokedAt != "" {
		t.Fatalf("rotated=%+v error=%v", rotated, err)
	}
	expired, err := application.ExpireIntegrationSecret(t.Context(), "token", admin)
	if err != nil || expired.Status != "expired" || expired.ExpiresAt == "" {
		t.Fatalf("expired=%+v error=%v", expired, err)
	}
}

func TestCredentialLifecycleCancellationDoesNotMutate(t *testing.T) {
	repository := &independentConfigRepository{connections: map[string]integrationmodel.IntegrationConnection{}, secrets: map[string]integrationmodel.IntegrationSecret{}, materials: map[string]string{}}
	application := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository})
	admin := integrationWorkspaceAdmin("admin", "workspace")
	if _, err := application.UpsertIntegrationSecret(t.Context(), "token", integrationmodel.IntegrationSecretUpsertRequest{Kind: "api_key", Value: "value"}, admin); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := application.RevokeIntegrationSecret(ctx, "token", admin); err != context.Canceled {
		t.Fatalf("error=%v", err)
	}
	if repository.secrets["token"].Status != "active" {
		t.Fatalf("cancelled revoke mutated secret: %+v", repository.secrets["token"])
	}
}
