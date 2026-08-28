package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestResolveSecretRefAndAdapterSecretsEdges(t *testing.T) {
	if _, err := ResolveSecretRef("literal"); apperror.CodeOf(err) != "backend.integration.secret_ref_must_be_env" {
		t.Fatalf("literal reference=%v", err)
	}
	if _, err := ResolveSecretRef("env: "); apperror.CodeOf(err) != "backend.integration.missing_env_key" {
		t.Fatalf("blank env key=%v", err)
	}
	t.Setenv("INTEGRATION_EDGE_SECRET", "")
	if _, err := ResolveSecretRef("env:INTEGRATION_EDGE_SECRET"); apperror.CodeOf(err) != "backend.integration.secret_not_configured" {
		t.Fatalf("missing env value=%v", err)
	}
	t.Setenv("INTEGRATION_EDGE_SECRET", " value ")
	if value, err := ResolveSecretRef(" env:INTEGRATION_EDGE_SECRET "); err != nil || value != "value" {
		t.Fatalf("env value=%q err=%v", value, err)
	}

	repository := newIntegrationSecretCommandRepository()
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository})
	connection := integrationmodel.IntegrationConnection{WorkspaceID: "workspace", SecretRefs: map[string]string{"env": "env:INTEGRATION_EDGE_SECRET"}}
	if _, err := service.ResolveAdapterSecrets(t.Context(), integrationmodel.IntegrationConnection{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("workspace guard=%v", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.ResolveAdapterSecrets(cancelled, connection); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
	t.Setenv("INTEGRATION_EDGE_MISSING", "")
	connection.SecretRefs["missing_env"] = "env:INTEGRATION_EDGE_MISSING"
	if _, err := service.ResolveAdapterSecrets(t.Context(), connection); apperror.CodeOf(err) != "backend.integration.secret_not_configured" {
		t.Fatalf("missing environment secret=%v", err)
	}
	delete(connection.SecretRefs, "missing_env")
	connection.SecretRefs["invalid"] = "literal"
	if _, err := service.ResolveAdapterSecrets(t.Context(), connection); apperror.CodeOf(err) != "backend.integration.secret_ref.must_be_reference" {
		t.Fatalf("invalid reference=%v", err)
	}
	delete(connection.SecretRefs, "invalid")
	connection.SecretRefs["stored"] = "secret:stored"
	repository.listErr = errors.New("secret list failed")
	if _, err := service.ResolveAdapterSecrets(t.Context(), connection); !errors.Is(err, repository.listErr) {
		t.Fatalf("list error=%v", err)
	}
	repository.listErr = nil
	if _, err := service.ResolveAdapterSecrets(t.Context(), connection); apperror.CodeOf(err) != "backend.integration.secret.unavailable" {
		t.Fatalf("missing secret=%v", err)
	}
	for name, secret := range map[string]integrationmodel.IntegrationSecret{
		"disabled": {Key: "stored", Status: "disabled", ValueRef: "material:stored"},
		"expired":  {Key: "stored", Status: "active", ValueRef: "material:stored", ExpiresAt: "2020-01-01T00:00:00Z"},
	} {
		repository.secrets["stored"] = secret
		if _, err := service.ResolveAdapterSecrets(t.Context(), connection); apperror.CodeOf(err) != "backend.integration.secret.unavailable" {
			t.Fatalf("%s secret=%v", name, err)
		}
	}
	repository.secrets["stored"] = integrationmodel.IntegrationSecret{Key: "stored", WorkspaceID: "workspace", Status: "active", ValueRef: "material:stored", ExpiresAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339)}
	repository.materials["workspace:stored"] = "stored-value"
	repository.resolveErr = errors.New("material read failed")
	if _, err := service.ResolveAdapterSecrets(t.Context(), connection); !errors.Is(err, repository.resolveErr) {
		t.Fatalf("material error=%v", err)
	}
	repository.resolveErr = nil
	resolved, err := service.ResolveAdapterSecrets(t.Context(), connection)
	if err != nil || resolved["stored"] != "stored-value" || resolved["env"] != "value" {
		t.Fatalf("resolved=%#v err=%v", resolved, err)
	}
}

func TestResolveAndPersistSecretMaterialEdges(t *testing.T) {
	repository := newIntegrationSecretCommandRepository()
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository})
	secret := integrationmodel.IntegrationSecret{Key: "secret", ValueRef: "material:secret"}
	if _, err := service.ResolveSecretMaterial(t.Context(), "", secret); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("resolve workspace=%v", err)
	}
	if _, err := service.ResolveSecretMaterial(t.Context(), "workspace", integrationmodel.IntegrationSecret{Key: "secret", ValueRef: "literal"}); apperror.CodeOf(err) != "backend.integration.secret.material_unavailable" {
		t.Fatalf("invalid material reference=%v", err)
	}
	withoutMaterial := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: &integrationManagementConfigRepo{}})
	if _, err := withoutMaterial.ResolveSecretMaterial(t.Context(), "workspace", secret); err == nil {
		t.Fatal("missing material repository accepted")
	}
	repository.resolveErr = errors.New("read failed")
	if _, err := service.ResolveSecretMaterial(t.Context(), "workspace", secret); !errors.Is(err, repository.resolveErr) {
		t.Fatalf("resolve error=%v", err)
	}
	repository.resolveErr = nil
	repository.materials["workspace:secret"] = "value"
	if value, err := service.ResolveSecretMaterial(t.Context(), "workspace", secret); err != nil || value != "value" {
		t.Fatalf("material=%q err=%v", value, err)
	}
	if err := service.PersistSecretMaterial(t.Context(), "", "secret", "value"); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("persist workspace=%v", err)
	}
	if err := withoutMaterial.PersistSecretMaterial(t.Context(), "workspace", "secret", " "); err != nil {
		t.Fatalf("blank material required repository=%v", err)
	}
	if err := withoutMaterial.PersistSecretMaterial(t.Context(), "workspace", "secret", "value"); err == nil {
		t.Fatal("missing material repository accepted write")
	}
	repository.putErr = errors.New("write failed")
	if err := service.PersistSecretMaterial(t.Context(), "workspace", "secret", "value"); !errors.Is(err, repository.putErr) {
		t.Fatalf("persist error=%v", err)
	}
	repository.putErr = nil
	if err := service.PersistSecretMaterial(t.Context(), "workspace", "secret", "new"); err != nil || repository.materials["workspace:secret"] != "new" {
		t.Fatalf("persisted=%q err=%v", repository.materials["workspace:secret"], err)
	}
}

func TestPersistAdapterSecretUpdatesFailureEdges(t *testing.T) {
	repository := newIntegrationSecretCommandRepository()
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository})
	connection := integrationmodel.IntegrationConnection{WorkspaceID: "workspace", SecretRefs: map[string]string{"token": "secret:token"}}
	if err := service.PersistAdapterSecretUpdates(t.Context(), integrationmodel.IntegrationConnection{}, nil, map[string]string{"token": "new"}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("workspace guard=%v", err)
	}
	if err := service.PersistAdapterSecretUpdates(t.Context(), connection, nil, nil); err != nil {
		t.Fatalf("empty updates=%v", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := service.PersistAdapterSecretUpdates(cancelled, connection, nil, map[string]string{"token": "new"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
	withoutMaterial := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: &integrationManagementConfigRepo{}})
	if err := withoutMaterial.PersistAdapterSecretUpdates(t.Context(), connection, nil, map[string]string{"token": "new"}); err == nil {
		t.Fatal("missing material repository accepted updates")
	}
	if err := service.PersistAdapterSecretUpdates(t.Context(), connection, nil, map[string]string{"token": " "}); err != nil {
		t.Fatalf("blank update=%v", err)
	}
	environment := connection
	environment.SecretRefs = map[string]string{"token": "env:TOKEN"}
	if err := service.PersistAdapterSecretUpdates(t.Context(), environment, nil, map[string]string{"token": "new"}); apperror.CodeOf(err) != "backend.integration.secret.rotation_requires_runtime_secret" {
		t.Fatalf("environment update=%v", err)
	}
	repository.listErr = errors.New("secret list failed")
	if err := service.PersistAdapterSecretUpdates(t.Context(), connection, nil, map[string]string{"token": "new"}); !errors.Is(err, repository.listErr) {
		t.Fatalf("list error=%v", err)
	}
	repository.listErr = nil
	if err := service.PersistAdapterSecretUpdates(t.Context(), connection, nil, map[string]string{"token": "new"}); apperror.CodeOf(err) != "backend.integration.secret.unavailable" {
		t.Fatalf("missing secret=%v", err)
	}
	repository.secrets["token"] = integrationmodel.IntegrationSecret{Key: "token", WorkspaceID: "workspace", Status: "disabled", ValueRef: "material:token"}
	if err := service.PersistAdapterSecretUpdates(t.Context(), connection, nil, map[string]string{"token": "new"}); apperror.CodeOf(err) != "backend.integration.secret.unavailable" {
		t.Fatalf("disabled secret=%v", err)
	}
	repository.secrets["token"] = integrationmodel.IntegrationSecret{Key: "token", WorkspaceID: "workspace", Status: "active", ValueRef: "material:token"}
	repository.materials["workspace:token"] = "old"
	repository.resolveErr = errors.New("material read failed")
	if err := service.PersistAdapterSecretUpdates(t.Context(), connection, map[string]string{"token": "old"}, map[string]string{"token": "new"}); !errors.Is(err, repository.resolveErr) {
		t.Fatalf("resolve error=%v", err)
	}
	repository.resolveErr = nil
	repository.putErr = errors.New("material write failed")
	if err := service.PersistAdapterSecretUpdates(t.Context(), connection, nil, map[string]string{"token": "new"}); !errors.Is(err, repository.putErr) {
		t.Fatalf("put error=%v", err)
	}
	repository.putErr = nil
	repository.upsertErr = errors.New("secret write failed")
	if err := service.PersistAdapterSecretUpdates(t.Context(), connection, nil, map[string]string{"token": "new"}); !errors.Is(err, repository.upsertErr) {
		t.Fatalf("metadata error=%v", err)
	}
}
