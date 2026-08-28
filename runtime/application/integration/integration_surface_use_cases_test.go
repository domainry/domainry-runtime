package integration

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type integrationSurfaceConfigRepo struct {
	integrationManagementConfigRepo
	listCalls int
	failAt    int
}

func (r *integrationSurfaceConfigRepo) ListConnections(ctx context.Context, workspaceID string) ([]integrationmodel.IntegrationConnection, error) {
	r.listCalls++
	if r.listCalls == r.failAt {
		return nil, errIntegrationManagementTest
	}
	return r.integrationManagementConfigRepo.ListConnections(ctx, workspaceID)
}

func TestIntegrationSurfaceDTOsDoNotCrossLeak(t *testing.T) {
	connection := integrationmodel.IntegrationConnection{
		Key: "crm", WorkspaceID: "workspace-a", ConnectorKey: "crm", ProviderKey: "provider-a",
		Status: "active", Config: map[string]any{"region": "cn"}, SecretRefs: map[string]string{"token": "secret-token"},
	}
	adminJSON, err := json.Marshal(ProjectTenantAdminIntegrationConnection(connection))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(adminJSON), "workspace_id") {
		t.Fatalf("Tenant Admin connection leaked workspace internals: %s", adminJSON)
	}

	secretJSON, err := json.Marshal(ProjectTenantAdminIntegrationSecretRef(integrationmodel.IntegrationSecret{
		Key: "secret-token", ValueRef: "vault://production/token", Fingerprint: "sha256:secret", LastTestError: "provider internals",
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbiddenField := range []string{"value_ref", "fingerprint", "last_test_error", "vault://"} {
		if strings.Contains(string(secretJSON), forbiddenField) {
			t.Fatalf("Tenant Admin secret projection leaked %q: %s", forbiddenField, secretJSON)
		}
	}
	if !strings.Contains(string(secretJSON), `"configured":true`) {
		t.Fatalf("Tenant Admin secret projection omitted configured state: %s", secretJSON)
	}
	emptySecretJSON, err := json.Marshal(ProjectTenantAdminIntegrationSecretRef(integrationmodel.IntegrationSecret{Key: "empty"}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(emptySecretJSON), `"configured":false`) {
		t.Fatalf("Tenant Admin empty secret projection misreported configured state: %s", emptySecretJSON)
	}

	eventJSON, err := json.Marshal(ProjectOpsIntegrationEvent(integrationmodel.IntegrationEvent{
		ID: "event", Provider: "provider-a", EventType: "changed", Status: "failed",
		Payload: map[string]any{"customer_name": "private"}, ExternalID: "business-id",
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbiddenField := range []string{"payload", "customer_name", "external_id", "business-id"} {
		if strings.Contains(string(eventJSON), forbiddenField) {
			t.Fatalf("Ops event projection leaked %q: %s", forbiddenField, eventJSON)
		}
	}

	outboxJSON, err := json.Marshal(ProjectOpsIntegrationOutbox(integrationmodel.IntegrationOutboxMessage{
		ID: "message", ConnectorKey: "crm", Operation: "send", Status: "failed",
		Payload: map[string]any{"phone": "private"}, RequestRef: "request-secret", DedupKey: "dedup-secret", RequestFingerprint: "fingerprint-secret",
	}))
	if err != nil {
		t.Fatal(err)
	}
	for _, forbiddenField := range []string{"payload", "phone", "request_ref", "dedup_key", "request_fingerprint"} {
		if strings.Contains(string(outboxJSON), forbiddenField) {
			t.Fatalf("Ops outbox projection leaked %q: %s", forbiddenField, outboxJSON)
		}
	}
}

func TestWorkspaceAdminDoesNotImplicitlyGrantIntegrationOps(t *testing.T) {
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{Permissions: []string{"workspace.admin"}})
	if err := AuthorizeOpsIntegrationActivity(principal); apperror.KindOf(err) != apperror.KindForbidden {
		t.Fatalf("workspace.admin unexpectedly granted Integration Ops activity: %v", err)
	}
	if err := AuthorizeOpsIntegrationRetry(principal); apperror.KindOf(err) != apperror.KindForbidden {
		t.Fatalf("workspace.admin unexpectedly granted Integration Ops recovery: %v", err)
	}
	accessfixture.Set(&principal, accessfixture.Bundle{Permissions: []string{PermissionAuditView, PermissionRetry}})
	if err := AuthorizeOpsIntegrationActivity(principal); err != nil {
		t.Fatalf("explicit activity permissions rejected: %v", err)
	}
	if err := AuthorizeOpsIntegrationRetry(principal); err != nil {
		t.Fatalf("explicit retry permission rejected: %v", err)
	}
}

func TestOpsIntegrationActivityUsesAuditPermissionAndServerFilters(t *testing.T) {
	events := &integrationManagementEventRepo{event: integrationmodel.IntegrationEvent{
		ID: "event-failed", Provider: "provider-a", EventType: "customer.changed", Status: "failed", Error: "provider timeout",
	}}
	delivery := &integrationManagementDeliveryRepo{
		invocations: []integrationmodel.IntegrationInvocation{{ID: "invocation-one", ConnectorKey: "crm", Operation: "sync", Status: "succeeded"}},
		outboxes:    []integrationmodel.IntegrationOutboxMessage{{ID: "outbox-one", ConnectorKey: "crm", Operation: "push", Status: "failed"}},
	}
	service := NewIntegrationApplicationService(ApplicationDependencies{
		ConfigRepository:   &integrationManagementConfigRepo{},
		EventRepository:    events,
		DeliveryRepository: delivery,
		Registry:           NewConnectorRegistry(integrationmodel.IntegrationSchema{}),
	})
	principal := integrationManagementPrincipal(PermissionAuditView)
	result, err := service.OpsIntegrationActivityWithQuery(t.Context(), OpsIntegrationActivityQuery{
		Kind: "events", ResourceID: "event-failed", Search: "TIMEOUT", Limit: 999,
	}, principal)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Events) != 1 || result.Events[0].ID != "event-failed" || len(result.Invocations) != 0 || len(result.Outbox) != 0 {
		t.Fatalf("filtered activity = %+v", result)
	}
	if events.lastLimit != 100 {
		t.Fatalf("normalized limit = %d, want 100", events.lastLimit)
	}
	if _, err := service.OpsIntegrationActivityWithQuery(t.Context(), OpsIntegrationActivityQuery{Kind: "unknown"}, principal); apperror.CodeOf(err) != "backend.integration.activity_kind_invalid" {
		t.Fatalf("invalid kind error = %v", err)
	}
	if opsIntegrationActivityMatches("event-failed", "other", "", "failed") || !opsIntegrationActivityMatches("event-failed", "", "EVENT", "failed") {
		t.Fatal("activity matching did not enforce resource ID and case-insensitive search")
	}
}

func TestTenantAdminConnectorProjectionSeparatesHealthFromConfiguration(t *testing.T) {
	projected := projectTenantAdminConnector(integrationmodel.ConnectorSchema{
		Key: "crm", Readiness: "connection_ready", DefinitionReady: true, AdapterReady: true, ConnectionReady: true,
		LifecycleStatus: "active", Providers: []integrationmodel.ConnectorProviderSchema{
			{Key: "provider-a", Readiness: "connection_ready", OperationKeys: []string{"push"}},
			{Key: "provider-b", Readiness: "provider_available", OperationKeys: []string{"push"}},
		},
		Operations: []integrationmodel.ConnectorOperationSchema{{Key: "push", IdempotencySupported: true}},
	}, []integrationmodel.IntegrationConnection{
		{Key: "healthy", ConnectorKey: "crm", ProviderKey: "provider-a", Status: "active"},
		{Key: "degraded", ConnectorKey: "crm", ProviderKey: "provider-b", Status: "degraded"},
	}, map[string]bool{"crm:provider-a": true})
	payload, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	for _, operationalField := range []string{"readiness", "definition_ready", "adapter_ready", "connection_ready"} {
		if strings.Contains(string(payload), operationalField) {
			t.Fatalf("Tenant Admin catalog leaked provider health %q: %s", operationalField, payload)
		}
	}
	if len(projected.Operations) != 1 || projected.Operations[0].Key != "push" || len(projected.Providers) != 2 {
		t.Fatalf("configuration projection = %+v", projected)
	}
	first, second := projected.Providers[0].Availability, projected.Providers[1].Availability
	if !first.CatalogAvailable || !first.Compiled || !first.Enabled || !first.Bound || !first.Configured || !first.Healthy || first.Degraded {
		t.Fatalf("healthy Provider availability = %+v", first)
	}
	if !second.CatalogAvailable || second.Compiled || !second.Enabled || !second.Bound || !second.Configured || second.Healthy || !second.Degraded {
		t.Fatalf("degraded uncompiled Provider availability = %+v", second)
	}
	if !projected.Availability.Compiled || !projected.Availability.Bound || !projected.Availability.Configured || !projected.Availability.Healthy || !projected.Availability.Degraded {
		t.Fatalf("aggregate Connector availability = %+v", projected.Availability)
	}
}

func TestIntegrationSurfaceUseCaseFailureAndFilterEdges(t *testing.T) {
	principal := integrationManagementPrincipal(PermissionCatalogView, PermissionSecretManage, PermissionAuditView)
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{
		Key: "crm", LifecycleStatus: "active", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "provider"}},
	}}})

	config := &integrationSurfaceConfigRepo{failAt: 2}
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: config, Registry: registry})
	if _, err := service.TenantAdminIntegrationCatalog(t.Context(), principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("connection-list catalog error=%v", err)
	}

	config = &integrationSurfaceConfigRepo{integrationManagementConfigRepo: integrationManagementConfigRepo{
		connections: []integrationmodel.IntegrationConnection{{Key: "connection", ConnectorKey: "crm", ProviderKey: "provider", Status: "active"}},
	}}
	service = NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: config, Registry: registry})
	if catalog, err := service.TenantAdminIntegrationCatalog(t.Context(), principal); err != nil || len(catalog.Connectors) != 1 || len(catalog.Connections) != 1 {
		t.Fatalf("catalog=%+v error=%v", catalog, err)
	}
	if secrets, err := service.TenantAdminIntegrationSecrets(t.Context(), principal); err != nil || len(secrets) != 1 {
		t.Fatalf("secrets=%+v error=%v", secrets, err)
	}
	config.err = errIntegrationManagementTest
	if _, err := service.TenantAdminIntegrationCatalog(t.Context(), principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("connector catalog error=%v", err)
	}
	if _, err := service.TenantAdminIntegrationSecrets(t.Context(), principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("secret-list error=%v", err)
	}

	config = &integrationSurfaceConfigRepo{}
	config.err = errIntegrationManagementTest
	service = NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: config, Registry: registry})
	if _, err := service.OpsIntegrationActivityWithQuery(t.Context(), OpsIntegrationActivityQuery{}, principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("activity catalog error=%v", err)
	}

	events := &integrationManagementEventRepo{event: integrationmodel.IntegrationEvent{ID: "event-one", Provider: "provider"}}
	delivery := &integrationManagementDeliveryRepo{
		invocations: []integrationmodel.IntegrationInvocation{
			{ID: "invocation-match", ConnectorKey: "target"},
			{ID: "invocation-other", ConnectorKey: "other"},
		},
		outboxes: []integrationmodel.IntegrationOutboxMessage{
			{ID: "outbox-match", ConnectorKey: "target"},
			{ID: "outbox-other", ConnectorKey: "other"},
		},
	}
	config = &integrationSurfaceConfigRepo{}
	service = NewIntegrationApplicationService(ApplicationDependencies{
		ConfigRepository: config, EventRepository: events, DeliveryRepository: delivery, Registry: registry,
	})
	if _, err := service.OpsIntegrationActivityWithQuery(t.Context(), OpsIntegrationActivityQuery{}, principalmodel.Principal{}); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("activity authorization error=%v", err)
	}
	if result, err := service.OpsIntegrationActivity(t.Context(), principal); err != nil || len(result.Events) != 1 || len(result.Invocations) != 2 || len(result.Outbox) != 2 {
		t.Fatalf("activity=%+v error=%v", result, err)
	}
	if result, err := service.OpsIntegrationActivityWithQuery(t.Context(), OpsIntegrationActivityQuery{Kind: "events", ResourceID: "other"}, principal); err != nil || len(result.Events) != 0 {
		t.Fatalf("filtered events=%+v error=%v", result.Events, err)
	}
	events.err = errIntegrationManagementTest
	if _, err := service.OpsIntegrationActivityWithQuery(t.Context(), OpsIntegrationActivityQuery{Kind: "events"}, principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("event-list error=%v", err)
	}
	events.err = nil
	if result, err := service.OpsIntegrationActivityWithQuery(t.Context(), OpsIntegrationActivityQuery{Kind: "invocations", Search: "target", Limit: 2}, principal); err != nil || len(result.Invocations) != 1 || result.Invocations[0].ID != "invocation-match" {
		t.Fatalf("filtered invocations=%+v error=%v", result.Invocations, err)
	}
	delivery.err = errIntegrationManagementTest
	if _, err := service.OpsIntegrationActivityWithQuery(t.Context(), OpsIntegrationActivityQuery{Kind: "invocations"}, principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("invocation-list error=%v", err)
	}
	delivery.err = nil
	if result, err := service.OpsIntegrationActivityWithQuery(t.Context(), OpsIntegrationActivityQuery{Kind: "outbox", Search: "target"}, principal); err != nil || len(result.Outbox) != 1 || result.Outbox[0].ID != "outbox-match" {
		t.Fatalf("filtered outbox=%+v error=%v", result.Outbox, err)
	}
	delivery.err = errIntegrationManagementTest
	if _, err := service.OpsIntegrationActivityWithQuery(t.Context(), OpsIntegrationActivityQuery{Kind: "outbox"}, principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("outbox-list error=%v", err)
	}

	if integrationHasExactPermission(principalmodel.Principal{}, PermissionAuditView) {
		t.Fatal("unknown principal has exact permission")
	}
	if integrationSurfaceCloneStringMap(nil) != nil {
		t.Fatal("nil secret refs were not preserved")
	}
}
