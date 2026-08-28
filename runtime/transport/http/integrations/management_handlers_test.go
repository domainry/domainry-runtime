package integrations_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	integrationapplication "github.com/domainry/domainry-runtime/runtime/application/integration"
	connectortest "github.com/domainry/domainry-runtime/runtime/bootstrap/testkit/connectors"
	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	integrationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/integration"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

type integrationHTTPProviderAdapter struct{}

type integrationHTTPConnectionHistory struct{}

func (integrationHTTPConnectionHistory) Events(context.Context, auditmodel.AuditEventQuery, principalmodel.Principal) ([]auditmodel.AuditEvent, error) {
	return []auditmodel.AuditEvent{{ID: "revision-1", Event: "integration_connection_upserted", ObjectKey: "integration_connection", RecordID: "webhook-connection"}}, nil
}

func (integrationHTTPProviderAdapter) Call(_ context.Context, _ integrationcontract.CallRequest) (integrationcontract.CallResult, error) {
	return integrationcontract.CallResult{Response: map[string]any{"ok": true}}, nil
}

func (integrationHTTPProviderAdapter) ValidateConfig(integrationmodel.IntegrationConnection) error {
	return nil
}

func (adapter integrationHTTPProviderAdapter) TestConnection(ctx context.Context, request integrationcontract.CallRequest) (integrationcontract.CallResult, error) {
	return adapter.Call(ctx, request)
}

func TestIntegrationCredentialWebhookAndBindingHTTP(t *testing.T) {
	store, application := newIntegrationManagementHTTPApplication(t)
	defer store.Close()
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1", UserID: "admin"}}, accessfixture.Bundle{Key: "admin", Permissions: []string{"workspace.admin", "*"}})
	handler := integrationEventHTTPHandler(application, &principal)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	call := func(method, path, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		return response
	}

	t.Run("api keys", func(t *testing.T) {
		if response := call(http.MethodGet, "/tenant-admin/integrations/api-keys", ""); response.Code != http.StatusOK || responseCount(t, response, "count") != 0 {
			t.Fatalf("initial list status=%d body=%s", response.Code, response.Body.String())
		}
		if response := call(http.MethodPost, "/tenant-admin/integrations/api-keys", `{`); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid create status=%d body=%s", response.Code, response.Body.String())
		}
		if response := call(http.MethodPost, "/tenant-admin/integrations/api-keys", `{}`); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid create request status=%d body=%s", response.Code, response.Body.String())
		}
		created := call(http.MethodPost, "/tenant-admin/integrations/api-keys", `{"key":"api-client","name":"API client","actor_id":"service-user","role_key":"integration-client","scopes":["customer.read"]}`)
		var createResult integrationmodel.IntegrationAPIKeyCreateResult
		if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &createResult) != nil || createResult.APIKey.Key != "api-client" || !strings.HasPrefix(createResult.Token, integrationapplication.APIKeyTokenPrefix) {
			t.Fatalf("create status=%d result=%+v body=%s", created.Code, createResult, created.Body.String())
		}
		if response := call(http.MethodGet, "/tenant-admin/integrations/api-keys", ""); response.Code != http.StatusOK || responseCount(t, response, "count") != 1 || strings.Contains(response.Body.String(), createResult.Token) {
			t.Fatalf("list status=%d body=%s", response.Code, response.Body.String())
		}
		if response := call(http.MethodPost, "/tenant-admin/integrations/api-keys/api-client/disable", ""); response.Code != http.StatusOK {
			t.Fatalf("disable status=%d body=%s", response.Code, response.Body.String())
		}
		rotated := call(http.MethodPost, "/tenant-admin/integrations/api-keys/api-client/rotate", "")
		var rotateResult integrationmodel.IntegrationAPIKeyRotateResult
		if rotated.Code != http.StatusOK || json.Unmarshal(rotated.Body.Bytes(), &rotateResult) != nil || rotateResult.Token == "" || rotateResult.Token == createResult.Token {
			t.Fatalf("rotate status=%d result=%+v body=%s", rotated.Code, rotateResult, rotated.Body.String())
		}
		if response := call(http.MethodPost, "/tenant-admin/integrations/api-keys/missing/disable", ""); response.Code != http.StatusNotFound {
			t.Fatalf("missing disable status=%d body=%s", response.Code, response.Body.String())
		}
		if response := call(http.MethodPost, "/tenant-admin/integrations/api-keys/missing/rotate", ""); response.Code != http.StatusNotFound {
			t.Fatalf("missing rotate status=%d body=%s", response.Code, response.Body.String())
		}
	})

	t.Run("secrets", func(t *testing.T) {
		if response := call(http.MethodGet, "/tenant-admin/integrations/secrets", ""); response.Code != http.StatusOK || responseCount(t, response, "count") != 0 {
			t.Fatalf("initial list status=%d body=%s", response.Code, response.Body.String())
		}
		if response := call(http.MethodPut, "/tenant-admin/integrations/secrets/token", `{`); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid upsert status=%d body=%s", response.Code, response.Body.String())
		}
		if response := call(http.MethodPut, "/tenant-admin/integrations/secrets/token", `{"kind":"api_key"}`); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid upsert request status=%d body=%s", response.Code, response.Body.String())
		}
		created := call(http.MethodPut, "/tenant-admin/integrations/secrets/token", `{"kind":"api_key","value":"secret-value","description":"probe"}`)
		if created.Code != http.StatusOK || strings.Contains(created.Body.String(), "secret-value") {
			t.Fatalf("upsert status=%d body=%s", created.Code, created.Body.String())
		}
		if response := call(http.MethodGet, "/tenant-admin/integrations/secrets", ""); response.Code != http.StatusOK || responseCount(t, response, "count") != 1 || !strings.Contains(response.Body.String(), `"configured":true`) || strings.Contains(response.Body.String(), "secret-value") || strings.Contains(response.Body.String(), "value_ref") {
			t.Fatalf("list status=%d body=%s", response.Code, response.Body.String())
		}
		if response := call(http.MethodPost, "/tenant-admin/integrations/secrets/token/rotate", `{`); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid rotate status=%d body=%s", response.Code, response.Body.String())
		}
		for _, step := range []struct {
			path, body, secretValue string
		}{
			{path: "/tenant-admin/integrations/secrets/token/rotate", body: `{"value":"rotated-value"}`, secretValue: "rotated-value"},
			{path: "/tenant-admin/integrations/secrets/token/expire"},
			{path: "/tenant-admin/integrations/secrets/token/rotate", body: `{"value":"active-again"}`, secretValue: "active-again"},
			{path: "/tenant-admin/integrations/secrets/token/disable"},
			{path: "/tenant-admin/integrations/secrets/token/rotate", body: `{"value":"final-value"}`, secretValue: "final-value"},
			{path: "/tenant-admin/integrations/secrets/token/revoke"},
		} {
			if response := call(http.MethodPost, step.path, step.body); response.Code != http.StatusOK || step.secretValue != "" && strings.Contains(response.Body.String(), step.secretValue) {
				t.Fatalf("%s status=%d body=%s", step.path, response.Code, response.Body.String())
			}
		}
		if response := call(http.MethodPost, "/tenant-admin/integrations/secrets/missing/revoke", ""); response.Code != http.StatusNotFound {
			t.Fatalf("missing revoke status=%d body=%s", response.Code, response.Body.String())
		}
	})

	t.Run("webhook subscriptions", func(t *testing.T) {
		if response := call(http.MethodPut, "/tenant-admin/integrations/webhook-subscriptions/orders", `{`); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid upsert status=%d body=%s", response.Code, response.Body.String())
		}
		if response := call(http.MethodPut, "/tenant-admin/integrations/webhook-subscriptions/orders", `{}`); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid upsert request status=%d body=%s", response.Code, response.Body.String())
		}
		created := call(http.MethodPut, "/tenant-admin/integrations/webhook-subscriptions/orders", `{"name":"Orders","connector_key":"webhook","connection_key":"webhook-connection","event_types":["order.paid","order.created","order.paid"]}`)
		if created.Code != http.StatusOK {
			t.Fatalf("upsert status=%d body=%s", created.Code, created.Body.String())
		}
		if response := call(http.MethodGet, "/tenant-admin/integrations/webhook-subscriptions?connector_key=webhook&event_type=order.paid&status=active&limit=999", ""); response.Code != http.StatusOK || responseCount(t, response, "count") != 1 {
			t.Fatalf("list status=%d body=%s", response.Code, response.Body.String())
		}
		if response := call(http.MethodPost, "/tenant-admin/integrations/webhook-subscriptions/publish", `{`); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid publish status=%d body=%s", response.Code, response.Body.String())
		}
		if response := call(http.MethodPost, "/tenant-admin/integrations/webhook-subscriptions/publish", `{}`); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid publish request status=%d body=%s", response.Code, response.Body.String())
		}
		published := call(http.MethodPost, "/tenant-admin/integrations/webhook-subscriptions/publish", `{"event_type":"order.paid","object_key":"sales_order","record_id":"order-1","payload":{"amount":42}}`)
		var result integrationmodel.IntegrationWebhookPublishResult
		if published.Code != http.StatusAccepted || json.Unmarshal(published.Body.Bytes(), &result) != nil || result.Enqueued != 1 {
			t.Fatalf("publish status=%d result=%+v body=%s", published.Code, result, published.Body.String())
		}
		if response := call(http.MethodPost, "/tenant-admin/integrations/webhook-subscriptions/orders/disable", ""); response.Code != http.StatusOK {
			t.Fatalf("disable status=%d body=%s", response.Code, response.Body.String())
		}
		if response := call(http.MethodPost, "/tenant-admin/integrations/webhook-subscriptions/missing/disable", ""); response.Code != http.StatusNotFound {
			t.Fatalf("missing disable status=%d body=%s", response.Code, response.Body.String())
		}
		if response := call(http.MethodDelete, "/tenant-admin/integrations/webhook-subscriptions/orders", ""); response.Code != http.StatusNoContent {
			t.Fatalf("delete status=%d body=%s", response.Code, response.Body.String())
		}
		if response := call(http.MethodDelete, "/tenant-admin/integrations/webhook-subscriptions/orders", ""); response.Code != http.StatusNotFound {
			t.Fatalf("missing delete status=%d body=%s", response.Code, response.Body.String())
		}
	})

	t.Run("binding validation and permission errors", func(t *testing.T) {
		if response := call(http.MethodPost, "/tenant-admin/integrations/bindings/validate", `{`); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid binding status=%d body=%s", response.Code, response.Body.String())
		}
		if response := call(http.MethodPost, "/tenant-admin/integrations/bindings/validate", `{"connector_key":"webhook","connection_key":"webhook-connection"}`); response.Code != http.StatusOK {
			t.Fatalf("binding status=%d body=%s", response.Code, response.Body.String())
		}
		accessfixture.Set(&principal, accessfixture.Bundle{})
		for _, request := range []struct{ method, path, body string }{
			{http.MethodGet, "/tenant-admin/integrations/api-keys", ""},
			{http.MethodGet, "/tenant-admin/integrations/secrets", ""},
			{http.MethodGet, "/tenant-admin/integrations/webhook-subscriptions", ""},
			{http.MethodPost, "/tenant-admin/integrations/bindings/validate", `{"connector_key":"webhook"}`},
		} {
			if response := call(request.method, request.path, request.body); response.Code != http.StatusForbidden {
				t.Fatalf("%s permission status=%d body=%s", request.path, response.Code, response.Body.String())
			}
		}
	})
}

func newIntegrationManagementHTTPApplication(t *testing.T) (*database.RuntimeStore, *integrationapplication.IntegrationApplicationService) {
	t.Helper()
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "integration-management.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		store.Close()
		t.Fatal(err)
	}
	configRepository := integrationpersistence.NewIntegrationConfigStore(store)
	if _, err := configRepository.UpsertConnection(t.Context(), "workspace-1", integrationmodel.IntegrationConnection{
		Key: "webhook-connection", WorkspaceID: "workspace-1", ConnectorKey: "webhook", ProviderKey: "probe", Status: "verified", Config: map[string]any{"url": "https://example.invalid"},
	}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	connector := integrationmodel.ConnectorSchema{
		Key: "webhook", Type: "http", Provider: "probe", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "probe"}},
		Operations: []integrationmodel.ConnectorOperationSchema{{Key: "ping", Method: http.MethodPost, ExecutionMode: "sync", SideEffect: "read", TestSupported: true}},
	}
	providers := connectortest.Registry(connectortest.Provider("webhook", "probe", integrationHTTPProviderAdapter{}, connector.Operations))
	application := integrationapplication.NewIntegrationApplicationService(integrationapplication.ApplicationDependencies{
		ConfigRepository:  configRepository,
		ConnectionHistory: integrationHTTPConnectionHistory{},
		EventRepository:   integrationpersistence.NewIntegrationEventStore(store), DeliveryRepository: integrationpersistence.NewIntegrationDeliveryStore(store), WorkerRepository: integrationpersistence.NewIntegrationWorkerStore(store),
		Registry:        integrationapplication.NewConnectorRegistryWithProviders(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{connector}}, providers),
		ConnectorExists: func(key string) bool { return key == "webhook" },
		InvocationProviderResolver: func(context.Context, string, string, string, string) (string, error) {
			return "probe", nil
		},
		PrincipalResolver: func(_ context.Context, actorID, roleKey, _ string) principalmodel.Principal {
			if actorID == "service-user" && roleKey == "integration-client" {
				return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: actorID}}, accessfixture.Bundle{Key: roleKey, Permissions: []string{"customer.read"}})
			}
			return principalmodel.Principal{}
		},
	})
	return store, application
}

func responseCount(t *testing.T, response *httptest.ResponseRecorder, key string) int {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	value, _ := payload[key].(float64)
	return int(value)
}
