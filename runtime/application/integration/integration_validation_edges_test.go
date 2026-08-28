package integration

import (
	"context"
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

type cancellingIntegrationSecretRepository struct {
	*independentConfigRepository
	cancel       context.CancelFunc
	puts         int
	cancelOnList bool
}

func (r *cancellingIntegrationSecretRepository) ListSecrets(ctx context.Context, workspaceID string) ([]integrationmodel.IntegrationSecret, error) {
	values, err := r.independentConfigRepository.ListSecrets(ctx, workspaceID)
	if r.cancelOnList {
		r.cancel()
	}
	return values, err
}

func (r *cancellingIntegrationSecretRepository) PutSecretMaterial(ctx context.Context, workspaceID, secretKey, value string) error {
	if err := r.independentConfigRepository.PutSecretMaterial(ctx, workspaceID, secretKey, value); err != nil {
		return err
	}
	r.puts++
	r.cancel()
	return nil
}

func TestIntegrationConnectionAndSecretValidationEdges(t *testing.T) {
	if _, err := NewIntegrationApplicationService(ApplicationDependencies{}).NormalizeSecretRefs(t.Context(), nil, ""); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("workspace error=%v", err)
	}
	repository := &integrationManagementConfigRepo{err: errIntegrationManagementTest}
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository})
	if _, err := service.NormalizeSecretRefs(t.Context(), map[string]string{"token": "secret:key"}, "workspace"); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("secret lookup error=%v", err)
	}
	missing := &independentConfigRepository{secrets: map[string]integrationmodel.IntegrationSecret{}, connections: map[string]integrationmodel.IntegrationConnection{}, materials: map[string]string{}}
	service = NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: missing})
	if _, err := service.NormalizeSecretRefs(t.Context(), map[string]string{"token": "secret:key"}, "workspace"); apperror.CodeOf(err) != "backend.integration.secret.not_found" {
		t.Fatalf("missing secret error=%v", err)
	}
	missing.secrets["key"] = integrationmodel.IntegrationSecret{Key: "key", Status: "disabled"}
	if _, err := service.NormalizeSecretRefs(t.Context(), map[string]string{"token": "secret:key"}, "workspace"); apperror.CodeOf(err) != "backend.integration.secret.disabled" {
		t.Fatalf("disabled secret error=%v", err)
	}
	if _, ok := NewIntegrationApplicationService(ApplicationDependencies{}).connectorDefinition("connector"); ok {
		t.Fatal("nil registry returned connector")
	}
	plain := errors.New("plain validation")
	if got := integrationConnectionValidationApplicationError(plain); !errors.Is(got, plain) {
		t.Fatalf("plain validation error=%v", got)
	}
	if _, err := NormalizeSecretRefsSyntax(map[string]string{"token": " "}); apperror.CodeOf(err) != "backend.integration.secret_ref.invalid" {
		t.Fatalf("blank secret value error=%v", err)
	}
	for _, connector := range []integrationmodel.ConnectorSchema{
		{Providers: []integrationmodel.ConnectorProviderSchema{{Key: " "}}},
		{Provider: "multi"},
		{Provider: "generated"},
	} {
		if keys := connectorProviderKeys(connector); len(keys) != 0 {
			t.Fatalf("provider keys for %#v=%#v", connector, keys)
		}
	}
}

func TestProviderSecretReferenceLookupEdges(t *testing.T) {
	connector := integrationmodel.ConnectorSchema{Key: "connector", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "provider", SecretFields: []definitionmodel.FieldSchema{{Key: "token", Config: map[string]any{"credential_kind": "api_key"}}}}}}
	repository := &integrationManagementConfigRepo{err: errIntegrationManagementTest}
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository})
	if err := service.ValidateProviderSecretRefs(t.Context(), connector, "provider", "configured", map[string]string{"token": "env:TOKEN"}, "workspace"); err != nil {
		t.Fatalf("environment provider secret error=%v", err)
	}
	if err := service.ValidateProviderSecretRefs(t.Context(), connector, "provider", "configured", map[string]string{"token": "secret:key"}, "workspace"); !errors.Is(err, errIntegrationManagementTest) {
		t.Fatalf("provider secret lookup error=%v", err)
	}
	missing := &independentConfigRepository{secrets: map[string]integrationmodel.IntegrationSecret{}, connections: map[string]integrationmodel.IntegrationConnection{}, materials: map[string]string{}}
	service = NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: missing})
	if err := service.ValidateProviderSecretRefs(t.Context(), connector, "provider", "configured", map[string]string{"token": "secret:key"}, "workspace"); apperror.CodeOf(err) != "backend.integration.secret.not_found" {
		t.Fatalf("missing provider secret error=%v", err)
	}
	withoutFields := integrationmodel.ConnectorSchema{Key: "connector", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "provider"}}}
	if err := service.ValidateProviderSecretRefs(t.Context(), withoutFields, "provider", "configured", map[string]string{"token": "env:TOKEN"}, "workspace"); apperror.CodeOf(err) != "backend.integration.connection.provider_secret_unknown" {
		t.Fatalf("provider without secret fields accepted arbitrary reference: %v", err)
	}
}

type integrationCodedEdgeError struct{}

func (integrationCodedEdgeError) Error() string     { return "coded" }
func (integrationCodedEdgeError) ErrorCode() string { return " provider.coded " }

func TestIntegrationErrorCodeAndProjectionPathEdges(t *testing.T) {
	if integrationErrorCode(integrationCodedEdgeError{}) != "provider.coded" {
		t.Fatal("coded provider error was not normalized")
	}
	if integrationPayloadPathString(map[string]any{"value": "x"}, " ") != "" {
		t.Fatal("empty payload path returned a value")
	}
}

func TestIntegrationSecretLoopsPreserveMidOperationCancellation(t *testing.T) {
	base := &independentConfigRepository{secrets: map[string]integrationmodel.IntegrationSecret{
		"one": {Key: "one", WorkspaceID: "workspace", Status: "active", ValueRef: "material:one"},
		"two": {Key: "two", WorkspaceID: "workspace", Status: "active", ValueRef: "material:two"},
	}, connections: map[string]integrationmodel.IntegrationConnection{}, materials: map[string]string{"workspace:one": "one", "workspace:two": "two"}}
	ctx, cancel := context.WithCancel(t.Context())
	repository := &cancellingIntegrationSecretRepository{independentConfigRepository: base, cancel: cancel, cancelOnList: true}
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository})
	connection := integrationmodel.IntegrationConnection{WorkspaceID: "workspace", SecretRefs: map[string]string{"one": "secret:one", "two": "secret:two"}}
	if _, err := service.ResolveAdapterSecrets(ctx, connection); !errors.Is(err, context.Canceled) {
		t.Fatalf("resolve cancellation error=%v", err)
	}

	ctx, cancel = context.WithCancel(t.Context())
	repository.cancel, repository.cancelOnList = cancel, false
	if err := service.PersistAdapterSecretUpdates(ctx, connection, nil, map[string]string{"one": "new-one", "two": "new-two"}); !errors.Is(err, context.Canceled) || repository.puts != 1 {
		t.Fatalf("persist cancellation puts=%d err=%v", repository.puts, err)
	}

	ctx, cancel = context.WithCancel(t.Context())
	repository.cancel, repository.cancelOnList = cancel, true
	if err := service.RecordCredentialTestEvidence(ctx, connection, true, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("evidence cancellation error=%v", err)
	}
}

func TestResolveAdapterSecretsRejectsLiteralReference(t *testing.T) {
	service := NewIntegrationApplicationService(ApplicationDependencies{})
	if _, err := service.ResolveAdapterSecrets(t.Context(), integrationmodel.IntegrationConnection{WorkspaceID: "workspace", SecretRefs: map[string]string{"token": "literal"}}); apperror.CodeOf(err) != "backend.integration.secret_ref.must_be_reference" {
		t.Fatalf("literal reference error=%v", err)
	}
}
