// Integration application service secret-update tests.
package integration

import integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"

import (
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"context"
	"errors"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"sync"
	"sync/atomic"
	"testing"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

type rotatingSecretAdapter struct {
	cancel context.CancelFunc
}

type refreshLeaseAdapter struct{ refreshes atomic.Int32 }

type refreshFailureAdapter struct{}

func (refreshFailureAdapter) Call(context.Context, integrationcontract.CallRequest) (integrationcontract.CallResult, error) {
	return integrationcontract.CallResult{ResponseRef: "oauth:refresh_failed"}, errors.New("oauth provider unavailable")
}

func (adapter *refreshLeaseAdapter) Call(_ context.Context, request integrationcontract.CallRequest) (integrationcontract.CallResult, error) {
	if request.Secrets["access_token"] == "old-token" {
		adapter.refreshes.Add(1)
		time.Sleep(25 * time.Millisecond)
		return integrationcontract.CallResult{Response: map[string]any{"rotated": true}, SecretUpdates: map[string]string{"access_token": "new-token"}}, nil
	}
	return integrationcontract.CallResult{Response: map[string]any{"rotated": true}}, nil
}

func (adapter rotatingSecretAdapter) Call(context.Context, integrationcontract.CallRequest) (integrationcontract.CallResult, error) {
	if adapter.cancel != nil {
		adapter.cancel()
	}
	return integrationcontract.CallResult{
		Response:      map[string]any{"rotated": true},
		ResponseRef:   "test:rotated",
		SecretUpdates: map[string]string{"access_token": "new-token"},
	}, nil
}

func newSecretUpdateTestService(reference string, adapter integrationcontract.Adapter) (*IntegrationApplicationService, *independentConfigRepository, principalmodel.Principal) {
	connection := integrationmodel.IntegrationConnection{
		Key: "oauth", WorkspaceID: "default", ConnectorKey: "oauth_test", ProviderKey: "oauth",
		Status: "active", SecretRefs: map[string]string{"access_token": reference},
	}
	repository := &independentConfigRepository{
		connections: map[string]integrationmodel.IntegrationConnection{"oauth": connection},
		secrets: map[string]integrationmodel.IntegrationSecret{"oauth_access": {
			Key: "oauth_access", WorkspaceID: "default", Status: "active", ValueRef: "material:oauth_access",
		}},
		materials: map[string]string{"default:oauth_access": "old-token"},
	}
	delivery := &independentDeliveryRepository{}
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{
		Key: "oauth_test", Type: "oauth_test", Provider: "oauth",
		Providers: []integrationmodel.ConnectorProviderSchema{{Key: "oauth", SecretFields: []definitionmodel.FieldSchema{
			{Key: "access_token", Type: "text"},
			{Key: "refresh_token", Type: "text"},
		}}},
		Operations: []integrationmodel.ConnectorOperationSchema{{Key: "refresh", Method: "POST", SideEffect: "read",
			Output: []definitionmodel.FieldSchema{{Key: "rotated", Type: "boolean", Required: true}}}},
	}}})
	service := NewIntegrationApplicationService(ApplicationDependencies{
		ConfigRepository: repository, DeliveryRepository: delivery, Registry: registry,
	})
	registerTestProviderAdapter(service, "oauth_test", "oauth", adapter)
	principal := integrationWorkspaceAdmin("admin", "default")
	return service, repository, principal
}

func TestAdapterSecretUpdatesPersistThroughCallerContext(t *testing.T) {
	service, repository, principal := newSecretUpdateTestService("secret:oauth_access", rotatingSecretAdapter{})
	result, err := service.TestConnectorOperation(t.Context(), "oauth", ConnectorOperationTestRequest{Operation: "refresh", Confirm: true}, principal)
	if err != nil || result.Response["rotated"] != true {
		t.Fatalf("rotate operation result=%#v err=%v", result, err)
	}
	if got := repository.materials["default:oauth_access"]; got != "new-token" {
		t.Fatalf("rotated material=%q", got)
	}
	if secret := repository.secrets["oauth_access"]; secret.ValueRef != "material:oauth_access" || secret.Fingerprint == "" {
		t.Fatalf("rotated secret metadata=%#v", secret)
	}
}

func TestSyncAndOutboxCallsUseRefreshCredentialLease(t *testing.T) {
	t.Setenv("OAUTH_REFRESH_TOKEN", "refresh-token")
	t.Setenv("OAUTH_ACCESS_TOKEN", "access-token")
	service, repository, principal := newSecretUpdateTestService("secret:oauth_access", rotatingSecretAdapter{})
	connection := repository.connections["oauth"]
	connection.SecretRefs["refresh_token"] = "env:OAUTH_REFRESH_TOKEN"
	repository.connections["oauth"] = connection
	if _, err := service.ExecuteIntegrationSyncCall(t.Context(), SyncCallRequest{ConnectorKey: "oauth_test", ConnectionKey: "oauth", Operation: "refresh"}, principal); err != nil {
		t.Fatalf("sync refresh lease error=%v", err)
	}
	message := integrationmodel.IntegrationOutboxMessage{ID: "message", WorkspaceID: "default", ConnectorKey: "oauth_test", ConnectionKey: "oauth", Operation: "refresh", Payload: map[string]any{}}
	if result, err := service.SendAdapterOutboxMessage(t.Context(), message, principal); err != nil || result.Status != "sent" {
		t.Fatalf("outbox refresh lease result=%#v err=%v", result, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	hold, err := service.registry.AcquireCredentialLease(t.Context(), "default:oauth")
	if err != nil {
		t.Fatal(err)
	}
	defer hold()
	if _, err := service.ExecuteIntegrationSyncCall(cancelled, SyncCallRequest{ConnectorKey: "oauth_test", ConnectionKey: "oauth", Operation: "refresh"}, principal); !errors.Is(err, context.Canceled) {
		t.Fatalf("sync lease cancellation error=%v", err)
	}
	if _, err := service.SendAdapterOutboxMessage(cancelled, message, principal); !errors.Is(err, context.Canceled) {
		t.Fatalf("outbox lease cancellation error=%v", err)
	}
	hold()
	connection = repository.connections["oauth"]
	connection.SecretRefs["access_token"] = "env:OAUTH_ACCESS_TOKEN"
	repository.connections["oauth"] = connection
	if _, err := service.SendAdapterOutboxMessage(t.Context(), message, principal); testErrorCode(err) != "backend.integration.secret.rotation_requires_runtime_secret" {
		t.Fatalf("outbox secret persistence error=%v", err)
	}
	failureService, failureRepository, failurePrincipal := newSecretUpdateTestService("secret:oauth_access", refreshFailureAdapter{})
	failureConnection := failureRepository.connections["oauth"]
	failureConnection.SecretRefs["refresh_token"] = "env:OAUTH_REFRESH_TOKEN"
	failureRepository.connections["oauth"] = failureConnection
	if _, err := failureService.SendAdapterOutboxMessage(t.Context(), message, failurePrincipal); err == nil {
		t.Fatal("outbox refresh failure accepted")
	}
}

func TestAdapterSecretUpdatesDoNotPersistAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	service, repository, principal := newSecretUpdateTestService("secret:oauth_access", rotatingSecretAdapter{cancel: cancel})
	_, err := service.TestConnectorOperation(ctx, "oauth", ConnectorOperationTestRequest{Operation: "refresh", Confirm: true}, principal)
	if err == nil || repository.materials["default:oauth_access"] != "old-token" {
		t.Fatalf("canceled rotation err=%v material=%q", err, repository.materials["default:oauth_access"])
	}
}

func TestAdapterSecretUpdatesRejectEnvironmentReference(t *testing.T) {
	t.Setenv("OAUTH_ACCESS_TOKEN", "old-token")
	service, repository, principal := newSecretUpdateTestService("env:OAUTH_ACCESS_TOKEN", rotatingSecretAdapter{})
	_, err := service.TestConnectorOperation(t.Context(), "oauth", ConnectorOperationTestRequest{Operation: "refresh", Confirm: true}, principal)
	if testErrorCode(err) != "backend.integration.secret.rotation_requires_runtime_secret" {
		t.Fatalf("expected runtime-secret rotation error, got %v", err)
	}
	if repository.materials["default:oauth_access"] != "old-token" {
		t.Fatalf("environment reference must not mutate material")
	}
}

func TestCredentialRefreshLeaseSerializesCallsAndResolvesFreshToken(t *testing.T) {
	adapter := &refreshLeaseAdapter{}
	service, repository, principal := newSecretUpdateTestService("secret:oauth_access", adapter)
	connection := repository.connections["oauth"]
	connection.SecretRefs["refresh_token"] = "secret:oauth_refresh"
	repository.connections["oauth"] = connection
	repository.secrets["oauth_refresh"] = integrationmodel.IntegrationSecret{Key: "oauth_refresh", WorkspaceID: "default", Status: "active", ValueRef: "material:oauth_refresh"}
	repository.materials["default:oauth_refresh"] = "refresh-token"
	var wait sync.WaitGroup
	wait.Add(2)
	errors := make(chan error, 2)
	for range 2 {
		go func() {
			defer wait.Done()
			_, err := service.TestConnectorOperation(t.Context(), "oauth", ConnectorOperationTestRequest{Operation: "refresh", Confirm: true}, principal)
			errors <- err
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	if adapter.refreshes.Load() != 1 || repository.materials["default:oauth_access"] != "new-token" {
		t.Fatalf("refreshes=%d material=%q", adapter.refreshes.Load(), repository.materials["default:oauth_access"])
	}
}

func TestCredentialLeaseWaitHonorsCallerCancellation(t *testing.T) {
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{})
	release, err := registry.AcquireCredentialLease(t.Context(), "workspace:connection")
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := registry.AcquireCredentialLease(ctx, "workspace:connection"); err != context.Canceled {
		t.Fatalf("error=%v", err)
	}
}

func TestStaleCredentialRefreshCannotOverwriteNewerMaterial(t *testing.T) {
	service, repository, _ := newSecretUpdateTestService("secret:oauth_access", rotatingSecretAdapter{})
	repository.materials["default:oauth_access"] = "newer-token"
	connection := repository.connections["oauth"]
	if err := service.PersistAdapterSecretUpdates(t.Context(), connection, map[string]string{"access_token": "old-token"}, map[string]string{"access_token": "late-token"}); err != nil {
		t.Fatal(err)
	}
	if repository.materials["default:oauth_access"] != "newer-token" {
		t.Fatalf("stale refresh overwrote material: %q", repository.materials["default:oauth_access"])
	}
}

func TestCredentialRefreshFailureDegradesConnectionAndAudits(t *testing.T) {
	service, repository, principal := newSecretUpdateTestService("secret:oauth_access", refreshFailureAdapter{})
	connection := repository.connections["oauth"]
	connection.SecretRefs["refresh_token"] = "secret:oauth_refresh"
	repository.connections["oauth"] = connection
	repository.secrets["oauth_refresh"] = integrationmodel.IntegrationSecret{Key: "oauth_refresh", WorkspaceID: "default", Status: "active", ValueRef: "material:oauth_refresh"}
	repository.materials["default:oauth_refresh"] = "refresh-token"
	audited := ""
	service.audit = func(_ context.Context, event, _, _ string, _ principalmodel.Principal, _ string, _, _, _ map[string]any) {
		if event == "integration_credential_refresh_failed" {
			audited = event
		}
	}
	_, err := service.TestConnectorOperation(t.Context(), "oauth", ConnectorOperationTestRequest{Operation: "refresh", Confirm: true}, principal)
	if err == nil || repository.connections["oauth"].Status != "degraded" || audited == "" {
		t.Fatalf("error=%v connection=%+v audit=%q", err, repository.connections["oauth"], audited)
	}
}
