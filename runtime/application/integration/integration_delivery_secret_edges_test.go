package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type deliveryCommandRepo struct {
	integrationrepository.IntegrationDeliveryRepository
	insertErr   error
	invocations []integrationmodel.IntegrationInvocation
	outboxes    []integrationmodel.IntegrationOutboxMessage
}

func (r *deliveryCommandRepo) InsertInvocation(_ context.Context, _ string, value integrationmodel.IntegrationInvocation) (integrationmodel.IntegrationInvocation, error) {
	if r.insertErr != nil {
		return integrationmodel.IntegrationInvocation{}, r.insertErr
	}
	value.ID = "invocation"
	r.invocations = append(r.invocations, value)
	return value, nil
}

func (r *deliveryCommandRepo) InsertOutbox(_ context.Context, _ string, value integrationmodel.IntegrationOutboxMessage) (integrationmodel.IntegrationOutboxMessage, error) {
	if r.insertErr != nil {
		return integrationmodel.IntegrationOutboxMessage{}, r.insertErr
	}
	value.ID = "outbox"
	r.outboxes = append(r.outboxes, value)
	return value, nil
}

type secretLifecycleRepo struct {
	integrationrepository.IntegrationConfigRepository
	secrets   []integrationmodel.IntegrationSecret
	listErr   error
	upsertErr error
	lastSaved integrationmodel.IntegrationSecret
}

type credentialLeaseRepo struct {
	integrationrepository.IntegrationConfigRepository
	acquire      bool
	acquireErr   error
	releases     int
	calls        int
	acquireAfter int
}

func (r *credentialLeaseRepo) TryAcquireCredentialRefreshLease(context.Context, string, string, string, string, string) (bool, error) {
	r.calls++
	return r.acquire || (r.acquireAfter > 0 && r.calls > r.acquireAfter), r.acquireErr
}

func (r *credentialLeaseRepo) ReleaseCredentialRefreshLease(context.Context, string, string, string) error {
	r.releases++
	return errIntegrationManagementTest
}

type credentialLeaseProviderConfigRepo struct {
	integrationrepository.IntegrationConfigRepository
	repository integrationrepository.IntegrationCredentialLeaseRepository
}

func (r credentialLeaseProviderConfigRepo) CredentialLeaseRepository() integrationrepository.IntegrationCredentialLeaseRepository {
	return r.repository
}

func (r *secretLifecycleRepo) ListSecrets(context.Context, string) ([]integrationmodel.IntegrationSecret, error) {
	return append([]integrationmodel.IntegrationSecret(nil), r.secrets...), r.listErr
}

func (r *secretLifecycleRepo) UpsertSecret(_ context.Context, _ string, value integrationmodel.IntegrationSecret) (integrationmodel.IntegrationSecret, error) {
	r.lastSaved = value
	return value, r.upsertErr
}

func TestIntegrationDeliveryCommandsAndEvidenceEdges(t *testing.T) {
	repository := &deliveryCommandRepo{}
	resolverErr := error(nil)
	service := NewIntegrationApplicationService(ApplicationDependencies{
		DeliveryRepository: repository,
		ConnectorExists:    func(key string) bool { return key == "connector" },
		InvocationProviderResolver: func(context.Context, string, string, string, string) (string, error) {
			return "provider", resolverErr
		},
	})
	principal := integrationManagementPrincipal(PermissionInvoke)
	principal.RequestID, principal.UserID, principal.RoleKey = "request", "actor", "role"
	invalidWorkspace := principal
	invalidWorkspace.WorkspaceID = ""
	if _, err := service.RecordIntegrationInvocation(t.Context(), integrationmodel.IntegrationInvocationRecordRequest{}, invalidWorkspace); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("invocation workspace error=%v", err)
	}
	if _, err := service.RecordIntegrationInvocation(t.Context(), integrationmodel.IntegrationInvocationRecordRequest{}, integrationManagementPrincipal()); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("invocation permission error=%v", err)
	}
	if _, err := service.EnqueueIntegrationOutboxMessage(t.Context(), integrationmodel.IntegrationOutboxEnqueueRequest{}, invalidWorkspace); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("outbox workspace error=%v", err)
	}
	if _, err := service.EnqueueIntegrationOutboxMessage(t.Context(), integrationmodel.IntegrationOutboxEnqueueRequest{}, integrationManagementPrincipal()); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("outbox permission error=%v", err)
	}
	for _, request := range []integrationmodel.IntegrationInvocationRecordRequest{{}, {ConnectorKey: "connector"}} {
		if _, err := service.RecordIntegrationInvocation(t.Context(), request, principal); apperror.CodeOf(err) != "backend.integration.invocation.missing_identity" {
			t.Fatalf("missing invocation identity=%v", err)
		}
	}
	withoutResolver := NewIntegrationApplicationService(ApplicationDependencies{DeliveryRepository: repository})
	if _, err := withoutResolver.RecordIntegrationInvocation(t.Context(), integrationmodel.IntegrationInvocationRecordRequest{ConnectorKey: "connector", Operation: "send"}, principal); apperror.KindOf(err) != apperror.KindNotFound {
		t.Fatalf("missing resolver error=%v", err)
	}
	resolverErr = errIntegrationManagementTest
	if _, err := service.RecordIntegrationInvocation(t.Context(), integrationmodel.IntegrationInvocationRecordRequest{ConnectorKey: "connector", Operation: "send"}, principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("resolver error=%v", err)
	}
	resolverErr = nil
	if _, err := service.RecordIntegrationInvocation(t.Context(), integrationmodel.IntegrationInvocationRecordRequest{ConnectorKey: "connector", Operation: "send", Status: "bad"}, principal); err == nil {
		t.Fatal("invalid invocation status accepted")
	}
	if _, err := service.RecordIntegrationInvocation(t.Context(), integrationmodel.IntegrationInvocationRecordRequest{ConnectorKey: "connector", Operation: "send", DurationMS: -1}, principal); err == nil {
		t.Fatal("negative invocation duration accepted")
	}
	saved, err := service.RecordIntegrationInvocation(t.Context(), integrationmodel.IntegrationInvocationRecordRequest{ConnectorKey: " connector ", Operation: " send ", Metadata: nil}, principal)
	if err != nil || saved.ProviderKey != "provider" || saved.Metadata["request_id"] != "request" || saved.Metadata["actor_id"] != "actor" || saved.Metadata["role_key"] != "role" {
		t.Fatalf("invocation=%#v err=%v", saved, err)
	}
	repository.insertErr = errIntegrationManagementTest
	if _, err := service.RecordIntegrationInvocation(t.Context(), integrationmodel.IntegrationInvocationRecordRequest{ConnectorKey: "connector", Operation: "send"}, principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("invocation insert error=%v", err)
	}
	repository.insertErr = nil

	for _, request := range []integrationmodel.IntegrationOutboxEnqueueRequest{{}, {ConnectorKey: "connector"}} {
		if _, err := service.EnqueueIntegrationOutboxMessage(t.Context(), request, principal); apperror.CodeOf(err) != "backend.integration.outbox.missing_identity" {
			t.Fatalf("missing outbox identity=%v", err)
		}
	}
	withoutConnector := NewIntegrationApplicationService(ApplicationDependencies{DeliveryRepository: repository, ConnectorExists: func(string) bool { return false }})
	if _, err := withoutConnector.EnqueueIntegrationOutboxMessage(t.Context(), integrationmodel.IntegrationOutboxEnqueueRequest{ConnectorKey: "connector", Operation: "send", RequestRef: "request"}, principal); apperror.KindOf(err) != apperror.KindNotFound {
		t.Fatalf("missing connector error=%v", err)
	}
	noRequestPrincipal := integrationManagementPrincipal(PermissionInvoke)
	if _, err := service.EnqueueIntegrationOutboxMessage(t.Context(), integrationmodel.IntegrationOutboxEnqueueRequest{ConnectorKey: "connector", Operation: "send"}, noRequestPrincipal); apperror.CodeOf(err) != "backend.idempotency.key_required" {
		t.Fatalf("missing request ref=%v", err)
	}
	message, err := service.EnqueueIntegrationOutboxMessage(t.Context(), integrationmodel.IntegrationOutboxEnqueueRequest{ConnectorKey: "connector", Operation: "send", Payload: nil}, principal)
	if err != nil || message.RequestRef != "request" || message.DedupKey != "request" || message.Payload["request_id"] != "request" {
		t.Fatalf("outbox=%#v err=%v", message, err)
	}
	repository.insertErr = errIntegrationManagementTest
	if _, err := service.EnqueueIntegrationOutboxMessage(t.Context(), integrationmodel.IntegrationOutboxEnqueueRequest{ConnectorKey: "connector", Operation: "send", RequestRef: "explicit"}, principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("outbox insert error=%v", err)
	}

	repository.insertErr = nil
	evidence := ExecutionEvidence{Connection: integrationmodel.IntegrationConnection{ConnectorKey: "connector", ProviderKey: "provider", Key: "connection"}, Operation: " send ", StartedAt: time.Now().Add(time.Hour), Source: "outbox", Request: map[string]any{"password": "secret"}}
	invocation, err := service.RecordIntegrationExecutionEvidence(t.Context(), evidence, principal)
	if err != nil || invocation.Status != "succeeded" || invocation.DurationMS != 0 || invocation.WorkspaceID != "workspace" {
		t.Fatalf("evidence invocation=%#v err=%v", invocation, err)
	}
	repository.insertErr = errIntegrationManagementTest
	if _, err := service.RecordIntegrationExecutionEvidence(t.Context(), evidence, principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("evidence insert error=%v", err)
	}
	if _, err := service.RecordIntegrationExecutionEvidence(t.Context(), evidence, invalidWorkspace); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("evidence workspace error=%v", err)
	}
}

func TestIntegrationSecretLifecycleEdges(t *testing.T) {
	repository := &secretLifecycleRepo{secrets: []integrationmodel.IntegrationSecret{{Key: "secret", WorkspaceID: "workspace", Kind: "token", Status: "active", Description: "desc", ValueRef: "ref", LastTestError: "error"}}}
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository})
	principal := integrationManagementPrincipal(PermissionSecretManage)
	invalidWorkspace := principal
	invalidWorkspace.WorkspaceID = ""
	if _, err := service.DisableIntegrationSecret(t.Context(), "secret", invalidWorkspace); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("secret workspace=%v", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.DisableIntegrationSecret(cancelled, "secret", principal); !errors.Is(err, context.Canceled) {
		t.Fatalf("secret cancellation=%v", err)
	}
	if _, err := service.DisableIntegrationSecret(t.Context(), "secret", integrationManagementPrincipal()); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("secret permission=%v", err)
	}
	repository.listErr = errIntegrationManagementTest
	if _, err := service.DisableIntegrationSecret(t.Context(), "secret", principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("secret list error=%v", err)
	}
	repository.listErr = nil
	if _, err := service.DisableIntegrationSecret(t.Context(), "missing", principal); apperror.KindOf(err) != apperror.KindNotFound {
		t.Fatalf("secret missing=%v", err)
	}
	repository.upsertErr = errIntegrationManagementTest
	if _, err := service.DisableIntegrationSecret(t.Context(), "secret", principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("secret upsert error=%v", err)
	}
	repository.upsertErr = nil
	disabled, err := service.DisableIntegrationSecret(t.Context(), " secret ", principal)
	if err != nil || disabled.Status != "disabled" || disabled.DisabledAt == "" || secretAuditShape(disabled)["disabled"] != true {
		t.Fatalf("disabled secret=%#v err=%v", disabled, err)
	}
	for _, transition := range []struct {
		name string
		call func() (integrationmodel.IntegrationSecret, error)
	}{
		{"expired", func() (integrationmodel.IntegrationSecret, error) {
			return service.ExpireIntegrationSecret(t.Context(), "secret", principal)
		}},
		{"revoked", func() (integrationmodel.IntegrationSecret, error) {
			return service.RevokeIntegrationSecret(t.Context(), "secret", principal)
		}},
	} {
		t.Run(transition.name, func(t *testing.T) {
			value, err := transition.call()
			if err != nil || value.Status != transition.name {
				t.Fatalf("secret=%#v err=%v", value, err)
			}
		})
	}
	if _, err := service.ExpireIntegrationSecret(t.Context(), "secret", invalidWorkspace); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("transition workspace=%v", err)
	}
	if _, err := service.ExpireIntegrationSecret(cancelled, "secret", principal); !errors.Is(err, context.Canceled) {
		t.Fatalf("transition cancellation=%v", err)
	}
	if _, err := service.ExpireIntegrationSecret(t.Context(), "secret", integrationManagementPrincipal()); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("transition permission=%v", err)
	}
	repository.listErr = errIntegrationManagementTest
	if _, err := service.ExpireIntegrationSecret(t.Context(), "secret", principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("transition list error=%v", err)
	}
	repository.listErr = nil
	if _, err := service.ExpireIntegrationSecret(t.Context(), "missing", principal); apperror.KindOf(err) != apperror.KindNotFound {
		t.Fatalf("transition missing=%v", err)
	}
	repository.upsertErr = errIntegrationManagementTest
	if _, err := service.ExpireIntegrationSecret(t.Context(), "secret", principal); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("transition upsert error=%v", err)
	}
	if shape := secretAuditShape(repository.secrets[0]); shape["description_set"] != true || shape["value_ref_set"] != true || shape["last_test_error_set"] != true {
		t.Fatalf("secret audit shape=%#v", shape)
	}
}

func TestCredentialLeaseDurationAndOwnerHelpers(t *testing.T) {
	for input, want := range map[time.Duration]time.Duration{
		0: 2 * time.Minute, time.Second: 2 * time.Minute, 2 * time.Minute: 150 * time.Second, 20 * time.Minute: 10 * time.Minute,
	} {
		if got := credentialRefreshLeaseDuration(input); got != want {
			t.Fatalf("lease duration(%s)=%s want=%s", input, got, want)
		}
	}
	owner := newCredentialLeaseOwner()
	if owner == "" {
		t.Fatalf("lease owner=%q", owner)
	}
}

func TestAcquireCredentialRefreshLeaseEdges(t *testing.T) {
	registry := NewConnectorRegistry(integrationmodel.IntegrationSchema{})
	connection := integrationmodel.IntegrationConnection{WorkspaceID: "workspace", Key: "connection"}
	localOnly := NewIntegrationApplicationService(ApplicationDependencies{Registry: registry, ConfigRepository: &connectionResolutionRepo{}})
	if _, err := localOnly.AcquireCredentialRefreshLease(t.Context(), integrationmodel.IntegrationConnection{}, 0); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("lease workspace error=%v", err)
	}
	release, err := localOnly.AcquireCredentialRefreshLease(t.Context(), connection, 0)
	if err != nil || release == nil {
		t.Fatalf("local lease missing=%v err=%v", release == nil, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := localOnly.AcquireCredentialRefreshLease(cancelled, connection, 0); !errors.Is(err, context.Canceled) {
		t.Fatalf("local lease cancellation=%v", err)
	}
	release()

	persistent := &credentialLeaseRepo{acquireErr: errIntegrationManagementTest}
	service := NewIntegrationApplicationService(ApplicationDependencies{Registry: registry, ConfigRepository: persistent})
	if _, err := service.AcquireCredentialRefreshLease(t.Context(), connection, time.Minute); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("persistent acquire error=%v", err)
	}
	persistent.acquireErr, persistent.acquire = nil, true
	release, err = service.AcquireCredentialRefreshLease(t.Context(), connection, time.Minute)
	if err != nil || release == nil {
		t.Fatalf("persistent lease missing=%v err=%v", release == nil, err)
	}
	release()
	if persistent.releases != 1 {
		t.Fatalf("persistent releases=%d", persistent.releases)
	}
	persistent.acquire = false
	timed, stop := context.WithTimeout(t.Context(), 10*time.Millisecond)
	defer stop()
	if _, err := service.AcquireCredentialRefreshLease(timed, connection, time.Minute); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("persistent wait cancellation=%v", err)
	}
	providerLease := &credentialLeaseRepo{acquireAfter: 1}
	providerService := NewIntegrationApplicationService(ApplicationDependencies{Registry: registry, ConfigRepository: credentialLeaseProviderConfigRepo{repository: providerLease}})
	release, err = providerService.AcquireCredentialRefreshLease(t.Context(), connection, time.Minute)
	if err != nil || release == nil || providerLease.calls < 2 {
		t.Fatalf("provider lease calls=%d missing=%v err=%v", providerLease.calls, release == nil, err)
	}
	release()
}

func TestSecretNormalizationAndRedactionHelperEdges(t *testing.T) {
	for input, want := range map[string]string{"": "active", "active": "active", "disabled": "disabled", "expired": "expired", "revoked": "revoked", "rotating": "active"} {
		if got, err := normalizeSecretStatus(input); err != nil || got != want {
			t.Fatalf("secret status %q=%q err=%v", input, got, err)
		}
	}
	if _, err := normalizeSecretStatus("invalid"); err == nil {
		t.Fatal("invalid secret status accepted")
	}
	kinds := map[string]string{
		"": "api_key", "api-key": "api_key", "bearer": "bearer_token", "basic_auth": "basic_auth_password", "password": "basic_auth_password",
		"oauth_secret": "oauth_client_secret", "client_secret": "oauth_client_secret", "webhook": "webhook_secret", "signing": "signing_secret", "db_password": "database_password",
		"refresh_token": "refresh_token", "private_key": "private_key", "certificate": "certificate", "connection_string": "connection_string", "service_account": "service_account", "identifier": "identifier", "generic_secret": "generic_secret",
	}
	for input, want := range kinds {
		if got, err := normalizeSecretKind(input); err != nil || got != want {
			t.Fatalf("secret kind %q=%q err=%v", input, got, err)
		}
	}
	if _, err := normalizeSecretKind("invalid"); err == nil {
		t.Fatal("invalid secret kind accepted")
	}
	if expiry, err := normalizeSecretExpiry(""); err != nil || expiry != "" {
		t.Fatalf("empty expiry=%q err=%v", expiry, err)
	}
	if expiry, err := normalizeSecretExpiry("2026-07-20T01:02:03+08:00"); err != nil || expiry != "2026-07-19T17:02:03Z" {
		t.Fatalf("normalized expiry=%q err=%v", expiry, err)
	}
	if _, err := normalizeSecretExpiry("invalid"); err == nil {
		t.Fatal("invalid expiry accepted")
	}
	if !SecretExpired("invalid", time.Now()) || SecretExpired("", time.Now()) || SecretExpired(time.Now().Add(time.Hour).Format(time.RFC3339), time.Now()) {
		t.Fatal("secret expiry matrix mismatch")
	}
	if _, _, err := secretMaterial(integrationmodel.IntegrationSecretUpsertRequest{}); err == nil {
		t.Fatal("missing secret material accepted")
	}
	if _, _, err := secretMaterial(integrationmodel.IntegrationSecretUpsertRequest{ValueRef: "literal"}); err == nil {
		t.Fatal("literal value reference accepted")
	}
	if ref, fingerprint, err := secretMaterial(integrationmodel.IntegrationSecretUpsertRequest{ValueRef: "env:TOKEN"}); err != nil || ref != "env:TOKEN" || fingerprint == "" {
		t.Fatalf("reference material ref=%q fingerprint=%q err=%v", ref, fingerprint, err)
	}
	if ref, fingerprint, err := secretMaterial(integrationmodel.IntegrationSecretUpsertRequest{Value: "literal"}); err != nil || ref != "" || fingerprint == "" {
		t.Fatalf("raw material ref=%q fingerprint=%q err=%v", ref, fingerprint, err)
	}

	response := RedactProviderResponse(map[string]any{
		"plain":   17,
		"nested":  map[string]any{"password": "value"},
		"items":   []any{"secret-value", map[string]any{"api-key": "value"}},
		"objects": []map[string]any{{"authorization": "value"}},
	}, map[string]string{"token": "secret-value", "empty": ""})
	if response["plain"] != 17 || response["nested"].(map[string]any)["password"] != "[REDACTED]" || response["items"].([]any)[0] != "[REDACTED]" || response["objects"].([]map[string]any)[0]["authorization"] != "[REDACTED]" {
		t.Fatalf("redacted response=%#v", response)
	}
	if RedactProviderResponse(nil, nil) != nil {
		t.Fatal("nil provider response changed")
	}
	for _, key := range []string{"password", "client_secret", "x-api-key", "session id", "refresh-token"} {
		if !isProviderCredentialKey(key) {
			t.Fatalf("credential key %q not detected", key)
		}
	}
	if isProviderCredentialKey("cursor") {
		t.Fatal("business cursor classified as credential")
	}
}

func TestSecretReferenceMaterialRepositoryEdges(t *testing.T) {
	t.Setenv("DOMAINRY_SECRET_EDGE", " value ")
	for _, reference := range []string{"literal", "env:"} {
		if _, err := ResolveSecretRef(reference); err == nil {
			t.Fatalf("invalid reference %q accepted", reference)
		}
	}
	if _, err := ResolveSecretRef("env:DOMAINRY_SECRET_EDGE_MISSING"); err == nil {
		t.Fatal("missing environment secret accepted")
	}
	if value, err := ResolveSecretRef(" env:DOMAINRY_SECRET_EDGE "); err != nil || value != "value" {
		t.Fatalf("environment value=%q err=%v", value, err)
	}

	unsupported := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: &integrationManagementConfigRepo{}})
	if _, err := unsupported.ResolveSecretMaterial(t.Context(), "", integrationmodel.IntegrationSecret{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("material workspace error=%v", err)
	}
	if value, err := unsupported.ResolveSecretMaterial(t.Context(), "workspace", integrationmodel.IntegrationSecret{ValueRef: "env:DOMAINRY_SECRET_EDGE"}); err != nil || value != "value" {
		t.Fatalf("environment material=%q err=%v", value, err)
	}
	if _, err := unsupported.ResolveSecretMaterial(t.Context(), "workspace", integrationmodel.IntegrationSecret{Key: "secret", ValueRef: "secret:other"}); apperror.CodeOf(err) != "backend.integration.secret.material_unavailable" {
		t.Fatalf("invalid material reference error=%v", err)
	}
	if _, err := unsupported.ResolveSecretMaterial(t.Context(), "workspace", integrationmodel.IntegrationSecret{Key: "secret", ValueRef: "material:secret"}); err == nil || err.Error() != "backend.integration.secret.material_repository_unavailable" {
		t.Fatalf("unsupported material repository error=%v", err)
	}

	repository := &independentConfigRepository{connections: map[string]integrationmodel.IntegrationConnection{}, secrets: map[string]integrationmodel.IntegrationSecret{}, materials: map[string]string{"workspace:secret": "stored"}}
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository})
	if value, err := service.ResolveSecretMaterial(t.Context(), " workspace ", integrationmodel.IntegrationSecret{Key: "secret", ValueRef: "material:secret"}); err != nil || value != "stored" {
		t.Fatalf("stored material=%q err=%v", value, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.ResolveSecretMaterial(cancelled, "workspace", integrationmodel.IntegrationSecret{Key: "secret", ValueRef: "material:secret"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("material cancellation error=%v", err)
	}
	if err := service.PersistSecretMaterial(t.Context(), "", "secret", "value"); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("persist workspace error=%v", err)
	}
	if err := unsupported.PersistSecretMaterial(t.Context(), "workspace", "secret", ""); err != nil {
		t.Fatalf("empty material error=%v", err)
	}
	if err := unsupported.PersistSecretMaterial(t.Context(), "workspace", "secret", "value"); err == nil || err.Error() != "backend.integration.secret.material_repository_unavailable" {
		t.Fatalf("unsupported persist error=%v", err)
	}
	if err := service.PersistSecretMaterial(t.Context(), "workspace", "secret", "next"); err != nil || repository.materials["workspace:secret"] != "next" {
		t.Fatalf("persisted materials=%#v err=%v", repository.materials, err)
	}
	if err := service.PersistSecretMaterial(cancelled, "workspace", "secret", "next"); !errors.Is(err, context.Canceled) {
		t.Fatalf("persist cancellation error=%v", err)
	}
}

func TestConnectionAndCredentialTestEvidenceEdges(t *testing.T) {
	repository := &secretLifecycleRepo{secrets: []integrationmodel.IntegrationSecret{{Key: "secret", WorkspaceID: "workspace", Status: "active"}}}
	connectionRepo := &connectionResolutionRepo{}
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: connectionRepo})
	connection := integrationmodel.IntegrationConnection{Key: "connection", WorkspaceID: "workspace", ConnectorKey: "connector", Status: "configured"}
	principal := integrationManagementPrincipal()
	if saved, err := service.PersistConnectionTestStatus(t.Context(), connection, "verified", principalmodel.Principal{}); err == nil || saved.Status != "configured" {
		t.Fatalf("authorization saved=%#v err=%v", saved, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.PersistConnectionTestStatus(cancelled, connection, "verified", principal); !errors.Is(err, context.Canceled) {
		t.Fatalf("status cancellation error=%v", err)
	}
	active := connection
	active.Status = "active"
	if saved, err := service.PersistConnectionTestStatus(t.Context(), active, "verified", principal); err != nil || saved.Status != "active" {
		t.Fatalf("active status saved=%#v err=%v", saved, err)
	}
	connectionRepo.upsertErr = errIntegrationManagementTest
	if saved, err := service.PersistConnectionTestStatus(t.Context(), connection, "verified", principal); !errors.Is(err, errIntegrationManagementTest) || saved.Status != "verified" {
		t.Fatalf("upsert failure saved=%#v err=%v", saved, err)
	}
	if saved := service.RecordConnectionTestStatus(t.Context(), connection, "verified", principal); saved.Status != "configured" {
		t.Fatalf("record fallback saved=%#v", saved)
	}
	connectionRepo.upsertErr = nil
	if saved := service.RecordConnectionTestStatus(t.Context(), connection, "verified", principal); saved.Status != "verified" {
		t.Fatalf("record status saved=%#v", saved)
	}

	service = NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository})
	evidenceConnection := integrationmodel.IntegrationConnection{WorkspaceID: "workspace", SecretRefs: map[string]string{"env": "env:TOKEN", "empty": "secret:", "first": "secret:secret", "duplicate": "secret:secret", "missing": "secret:missing"}}
	invalidWorkspace := evidenceConnection
	invalidWorkspace.WorkspaceID = ""
	if err := service.RecordCredentialTestEvidence(t.Context(), invalidWorkspace, true, nil); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("evidence workspace error=%v", err)
	}
	if err := service.RecordCredentialTestEvidence(cancelled, evidenceConnection, true, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("evidence cancellation error=%v", err)
	}
	repository.listErr = errIntegrationManagementTest
	if err := service.RecordCredentialTestEvidence(t.Context(), evidenceConnection, true, nil); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("evidence list error=%v", err)
	}
	repository.listErr = nil
	if err := service.RecordCredentialTestEvidence(t.Context(), evidenceConnection, false, nil); err != nil || repository.lastSaved.LastTestStatus != "failed" || repository.lastSaved.LastTestError != "backend.integration.credential.validation_failed" {
		t.Fatalf("failed evidence=%#v err=%v", repository.lastSaved, err)
	}
	if err := service.RecordCredentialTestEvidence(t.Context(), evidenceConnection, false, &apperror.AppError{Code: "provider.invalid"}); err != nil || repository.lastSaved.LastTestError != "provider.invalid" {
		t.Fatalf("coded evidence=%#v err=%v", repository.lastSaved, err)
	}
	repository.upsertErr = errIntegrationManagementTest
	if err := service.RecordCredentialTestEvidence(t.Context(), evidenceConnection, true, nil); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("evidence upsert error=%v", err)
	}
}
