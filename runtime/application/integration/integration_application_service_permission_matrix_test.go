// Integration application service permission tests.
package integration

import (
	"context"
	"errors"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"testing"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func TestIntegrationPermissionMatrixSeparatesViewManageTestInvokeRetryAndAudit(t *testing.T) {
	repository := &independentConfigRepository{
		connections: map[string]integrationmodel.IntegrationConnection{"probe": {Key: "probe", WorkspaceID: "workspace", ConnectorKey: "probe", ProviderKey: "probe", Status: "configured", Config: map[string]any{"url": "https://example.invalid"}}},
		secrets:     map[string]integrationmodel.IntegrationSecret{}, materials: map[string]string{},
	}
	delivery := &independentDeliveryRepository{}
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: "probe", Type: "http", Provider: "probe", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "probe"}}, Operations: []integrationmodel.ConnectorOperationSchema{{Key: "ping", Method: "POST", ExecutionMode: "sync", SideEffect: "read", TimeoutDefaultSeconds: 5, TimeoutMaxSeconds: 10}}}}})
	application := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, DeliveryRepository: delivery, Registry: registry})
	registerTestProviderAdapter(application, "probe", "probe", lifecycleAdapter{})
	principal := func(permission string) principalmodel.Principal {
		return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: permission, WorkspaceID: "workspace"}}, accessfixture.Bundle{Key: permission, Permissions: []string{permission}})
	}

	viewer := principal(PermissionCatalogView)
	if _, err := application.IntegrationConnectorCatalog(t.Context(), viewer); err != nil {
		t.Fatalf("catalog viewer denied: %v", err)
	}
	if _, err := application.ListIntegrationConnections(t.Context(), viewer); err != nil {
		t.Fatalf("connection viewer denied: %v", err)
	}
	if _, err := application.UpsertIntegrationConnection(t.Context(), "new", integrationmodel.IntegrationConnectionUpsertRequest{ConnectorKey: "probe", ProviderKey: "probe", Status: "configured"}, viewer); testErrorCode(err) != "auth.permission_denied" {
		t.Fatalf("catalog viewer managed connection: %v", err)
	}

	manager := principal(PermissionConnectionManage)
	if _, err := application.UpsertIntegrationConnection(t.Context(), "managed", integrationmodel.IntegrationConnectionUpsertRequest{ConnectorKey: "probe", ProviderKey: "probe", Status: "configured", Config: map[string]any{"url": "https://example.invalid"}}, manager); err != nil {
		t.Fatalf("connection manager denied: %v", err)
	}
	secretManager := principal(PermissionSecretManage)
	if _, err := application.UpsertIntegrationSecret(t.Context(), "credential", integrationmodel.IntegrationSecretUpsertRequest{Kind: "api_key", Value: "write-only"}, secretManager); err != nil {
		t.Fatalf("secret manager denied: %v", err)
	}

	tester := principal(PermissionConnectionTest)
	if _, err := application.TestConnectorOperation(t.Context(), "probe", ConnectorOperationTestRequest{Operation: "ping", Confirm: true}, tester); err != nil {
		t.Fatalf("connection tester denied: %v", err)
	}
	if _, err := application.ListIntegrationInvocations(t.Context(), "", "", "", "", "", "", 10, tester); testErrorCode(err) != "auth.permission_denied" {
		t.Fatalf("tester viewed audit evidence: %v", err)
	}
	if _, err := application.ListIntegrationInvocations(t.Context(), "", "", "", "", "", "", 10, principal(PermissionAuditView)); err != nil {
		t.Fatalf("audit viewer denied: %v", err)
	}
	if _, err := application.EnqueueIntegrationOutboxMessage(t.Context(), integrationmodel.IntegrationOutboxEnqueueRequest{ConnectorKey: "probe", ConnectionKey: "probe", Operation: "ping", RequestRef: "permission-probe"}, principal(PermissionInvoke)); err != nil {
		t.Fatalf("invoker denied: %v", err)
	}
	if _, err := application.ScheduleIntegrationOutboxRetry(t.Context(), "message", integrationmodel.IntegrationOutboxRetryRequest{}, principal(PermissionRetry)); err != nil {
		t.Fatalf("retry operator denied: %v", err)
	}
}

func TestIntegrationPermissionAuthorizedCallPreservesCallerCancellation(t *testing.T) {
	application, _, _ := newSecretUpdateTestService("secret:oauth_access", rotatingSecretAdapter{})
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "tester", WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Permissions: []string{PermissionConnectionTest}})
	_, err := application.TestConnectorOperation(ctx, "oauth", ConnectorOperationTestRequest{Operation: "refresh", Confirm: true}, principal)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled authorized call error=%v", err)
	}
}
