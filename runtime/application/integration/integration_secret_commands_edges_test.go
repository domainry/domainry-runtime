package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type integrationSecretCommandRepository struct {
	*independentConfigRepository
	listErr    error
	upsertErr  error
	putErr     error
	resolveErr error
	saved      integrationmodel.IntegrationSecret
	putKey     string
	putValue   string
}

func newIntegrationSecretCommandRepository() *integrationSecretCommandRepository {
	return &integrationSecretCommandRepository{independentConfigRepository: &independentConfigRepository{
		connections: map[string]integrationmodel.IntegrationConnection{},
		secrets:     map[string]integrationmodel.IntegrationSecret{},
		materials:   map[string]string{},
		apiKeys:     map[string]integrationmodel.IntegrationAPIKey{},
	}}
}

func (r *integrationSecretCommandRepository) ListSecrets(ctx context.Context, workspaceID string) ([]integrationmodel.IntegrationSecret, error) {
	if r.listErr != nil {
		return nil, r.listErr
	}
	return r.independentConfigRepository.ListSecrets(ctx, workspaceID)
}

func (r *integrationSecretCommandRepository) UpsertSecret(ctx context.Context, workspaceID string, value integrationmodel.IntegrationSecret) (integrationmodel.IntegrationSecret, error) {
	r.saved = value
	if r.upsertErr != nil {
		return integrationmodel.IntegrationSecret{}, r.upsertErr
	}
	return r.independentConfigRepository.UpsertSecret(ctx, workspaceID, value)
}

func (r *integrationSecretCommandRepository) PutSecretMaterial(ctx context.Context, workspaceID, secretKey, value string) error {
	r.putKey, r.putValue = secretKey, value
	if r.putErr != nil {
		return r.putErr
	}
	return r.independentConfigRepository.PutSecretMaterial(ctx, workspaceID, secretKey, value)
}

func (r *integrationSecretCommandRepository) ResolveSecretMaterial(ctx context.Context, workspaceID, secretKey string) (string, error) {
	if r.resolveErr != nil {
		return "", r.resolveErr
	}
	return r.independentConfigRepository.ResolveSecretMaterial(ctx, workspaceID, secretKey)
}

func TestUpsertIntegrationSecretGuardsValidationAndPersistence(t *testing.T) {
	principal := integrationRotationPrincipal(PermissionSecretManage)
	repository := newIntegrationSecretCommandRepository()
	var auditEvent string
	var auditBefore map[string]any
	service := NewIntegrationApplicationService(ApplicationDependencies{
		ConfigRepository: repository,
		Audit: func(_ context.Context, event, _, _ string, _ principalmodel.Principal, _ string, before, _ map[string]any, _ map[string]any) {
			auditEvent, auditBefore = event, before
		},
	})
	valid := integrationmodel.IntegrationSecretUpsertRequest{Key: "request-key", Kind: "bearer", ValueRef: "env:TOKEN"}
	if _, err := service.UpsertIntegrationSecret(t.Context(), "key", valid, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("workspace guard=%v", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.UpsertIntegrationSecret(cancelled, "key", valid, principal); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
	if _, err := service.UpsertIntegrationSecret(t.Context(), "key", valid, integrationRotationPrincipal()); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("permission guard=%v", err)
	}
	if _, err := service.UpsertIntegrationSecret(t.Context(), "", integrationmodel.IntegrationSecretUpsertRequest{ValueRef: "env:TOKEN"}, principal); apperror.CodeOf(err) != "backend.integration.secret.missing_key" {
		t.Fatalf("missing key=%v", err)
	}
	for name, request := range map[string]integrationmodel.IntegrationSecretUpsertRequest{
		"kind":     {Kind: "invalid", ValueRef: "env:TOKEN"},
		"status":   {Status: "invalid", ValueRef: "env:TOKEN"},
		"expiry":   {ExpiresAt: "invalid", ValueRef: "env:TOKEN"},
		"material": {},
	} {
		if _, err := service.UpsertIntegrationSecret(t.Context(), "key", request, principal); err == nil {
			t.Fatalf("invalid %s accepted", name)
		}
	}
	repository.putErr = errors.New("material write failed")
	if _, err := service.UpsertIntegrationSecret(t.Context(), "key", integrationmodel.IntegrationSecretUpsertRequest{Value: "raw"}, principal); !errors.Is(err, repository.putErr) {
		t.Fatalf("material error=%v", err)
	}
	repository.putErr = nil
	repository.listErr = errors.New("secret list failed")
	if _, err := service.UpsertIntegrationSecret(t.Context(), "key", valid, principal); !errors.Is(err, repository.listErr) {
		t.Fatalf("list error=%v", err)
	}
	repository.listErr = nil
	repository.upsertErr = errors.New("secret write failed")
	if _, err := service.UpsertIntegrationSecret(t.Context(), "key", valid, principal); !errors.Is(err, repository.upsertErr) {
		t.Fatalf("upsert error=%v", err)
	}
	repository.upsertErr = nil

	expired := "2020-01-01T00:00:00Z"
	saved, err := service.UpsertIntegrationSecret(t.Context(), "", integrationmodel.IntegrationSecretUpsertRequest{Key: " raw ", Kind: "bearer", Status: "active", Description: " description ", Value: " secret value ", ExpiresAt: expired}, principal)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Key != "raw" || saved.Kind != "bearer_token" || saved.Status != "expired" || saved.Description != "description" || saved.ValueRef != "material:raw" || saved.Fingerprint == "" || repository.putKey != "raw" || repository.putValue != "secret value" {
		t.Fatalf("saved=%#v put=%q:%q", saved, repository.putKey, repository.putValue)
	}
	if auditEvent != "integration_secret_upserted" || auditBefore != nil {
		t.Fatalf("audit event=%q before=%#v", auditEvent, auditBefore)
	}

	repository.secrets["raw"] = saved
	saved, err = service.UpsertIntegrationSecret(t.Context(), "raw", integrationmodel.IntegrationSecretUpsertRequest{Kind: "api_key", Status: "disabled", ValueRef: "env:NEW"}, principal)
	if err != nil || saved.DisabledAt == "" || auditBefore["key"] != "raw" {
		t.Fatalf("disabled=%#v before=%#v err=%v", saved, auditBefore, err)
	}
	saved, err = service.UpsertIntegrationSecret(t.Context(), "raw", integrationmodel.IntegrationSecretUpsertRequest{Kind: "api_key", Status: "revoked", ValueRef: "env:NEW"}, principal)
	if err != nil || saved.RevokedAt == "" {
		t.Fatalf("revoked=%#v err=%v", saved, err)
	}
}

func TestRotateIntegrationSecretGuardsFailuresAndSuccess(t *testing.T) {
	principal := integrationRotationPrincipal(PermissionSecretManage)
	repository := newIntegrationSecretCommandRepository()
	repository.secrets["secret"] = integrationmodel.IntegrationSecret{Key: "secret", WorkspaceID: "workspace", Kind: "api_key", Status: "disabled", Description: "Old", ValueRef: "env:OLD", DisabledAt: "old", RevokedAt: "old"}
	var auditEvent string
	service := NewIntegrationApplicationService(ApplicationDependencies{
		ConfigRepository: repository,
		Audit: func(_ context.Context, event, _, _ string, _ principalmodel.Principal, _ string, _, _ map[string]any, _ map[string]any) {
			auditEvent = event
		},
	})
	valid := integrationmodel.IntegrationSecretUpsertRequest{ValueRef: "env:NEW"}
	if _, err := service.RotateIntegrationSecret(t.Context(), "secret", valid, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("workspace guard=%v", err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := service.RotateIntegrationSecret(cancelled, "secret", valid, principal); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
	if _, err := service.RotateIntegrationSecret(t.Context(), "secret", valid, integrationRotationPrincipal()); apperror.CodeOf(err) != "auth.permission_denied" {
		t.Fatalf("permission guard=%v", err)
	}
	repository.listErr = errors.New("secret list failed")
	if _, err := service.RotateIntegrationSecret(t.Context(), "secret", valid, principal); !errors.Is(err, repository.listErr) {
		t.Fatalf("list error=%v", err)
	}
	repository.listErr = nil
	if _, err := service.RotateIntegrationSecret(t.Context(), "missing", valid, principal); apperror.KindOf(err) != apperror.KindNotFound {
		t.Fatalf("missing secret=%v", err)
	}
	for name, request := range map[string]integrationmodel.IntegrationSecretUpsertRequest{
		"material": {},
		"status":   {ValueRef: "env:NEW", Status: "invalid"},
		"expiry":   {ValueRef: "env:NEW", ExpiresAt: "invalid"},
		"kind":     {ValueRef: "env:NEW", Kind: "invalid"},
	} {
		if _, err := service.RotateIntegrationSecret(t.Context(), "secret", request, principal); err == nil {
			t.Fatalf("invalid %s accepted", name)
		}
	}
	repository.putErr = errors.New("material write failed")
	if _, err := service.RotateIntegrationSecret(t.Context(), "secret", integrationmodel.IntegrationSecretUpsertRequest{Value: "raw"}, principal); !errors.Is(err, repository.putErr) {
		t.Fatalf("material error=%v", err)
	}
	repository.putErr = nil
	repository.upsertErr = errors.New("secret write failed")
	if _, err := service.RotateIntegrationSecret(t.Context(), "secret", valid, principal); !errors.Is(err, repository.upsertErr) {
		t.Fatalf("upsert error=%v", err)
	}
	repository.upsertErr = nil

	saved, err := service.RotateIntegrationSecret(t.Context(), " secret ", integrationmodel.IntegrationSecretUpsertRequest{Kind: "webhook", Status: "rotating", Description: " New ", Value: " raw ", ExpiresAt: "2020-01-01T00:00:00Z"}, principal)
	if err != nil {
		t.Fatal(err)
	}
	if saved.Status != "expired" || saved.Kind != "webhook_secret" || saved.Description != "New" || saved.ValueRef != "material:secret" || saved.RotatedAt == "" || saved.DisabledAt != "" || saved.RevokedAt != "" || auditEvent != "integration_secret_rotated" {
		t.Fatalf("rotated=%#v audit=%q", saved, auditEvent)
	}
}
