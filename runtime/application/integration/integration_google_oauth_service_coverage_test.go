package integration

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type googleOAuthFailureRepository struct {
	*independentConfigRepository
	failListConnections, failListSecrets, failResolve, failPut bool
	failUpsertSecretAt, upsertSecretCalls                      int
	failUpsertConnectionAt, upsertConnectionCalls              int
	failUpsertIdentityAt, upsertIdentityCalls                  int
}

var errGoogleOAuthRepository = errors.New("google OAuth repository failure")

func (r *googleOAuthFailureRepository) ListConnections(ctx context.Context, workspaceID string) ([]integrationmodel.IntegrationConnection, error) {
	if r.failListConnections {
		return nil, errGoogleOAuthRepository
	}
	return r.independentConfigRepository.ListConnections(ctx, workspaceID)
}
func (r *googleOAuthFailureRepository) ListSecrets(ctx context.Context, workspaceID string) ([]integrationmodel.IntegrationSecret, error) {
	if r.failListSecrets {
		return nil, errGoogleOAuthRepository
	}
	return r.independentConfigRepository.ListSecrets(ctx, workspaceID)
}
func (r *googleOAuthFailureRepository) ResolveSecretMaterial(ctx context.Context, workspaceID, secretKey string) (string, error) {
	if r.failResolve {
		return "", errGoogleOAuthRepository
	}
	return r.independentConfigRepository.ResolveSecretMaterial(ctx, workspaceID, secretKey)
}
func (r *googleOAuthFailureRepository) PutSecretMaterial(ctx context.Context, workspaceID, secretKey, value string) error {
	if r.failPut {
		return errGoogleOAuthRepository
	}
	return r.independentConfigRepository.PutSecretMaterial(ctx, workspaceID, secretKey, value)
}
func (r *googleOAuthFailureRepository) UpsertSecret(ctx context.Context, workspaceID string, value integrationmodel.IntegrationSecret) (integrationmodel.IntegrationSecret, error) {
	r.upsertSecretCalls++
	if r.failUpsertSecretAt == r.upsertSecretCalls {
		return integrationmodel.IntegrationSecret{}, errGoogleOAuthRepository
	}
	return r.independentConfigRepository.UpsertSecret(ctx, workspaceID, value)
}
func (r *googleOAuthFailureRepository) UpsertConnection(ctx context.Context, workspaceID string, value integrationmodel.IntegrationConnection) (integrationmodel.IntegrationConnection, error) {
	r.upsertConnectionCalls++
	if r.failUpsertConnectionAt == r.upsertConnectionCalls {
		return integrationmodel.IntegrationConnection{}, errGoogleOAuthRepository
	}
	return r.independentConfigRepository.UpsertConnection(ctx, workspaceID, value)
}
func (r *googleOAuthFailureRepository) UpsertExternalIdentity(ctx context.Context, workspaceID string, value integrationmodel.IntegrationExternalIdentity) (integrationmodel.IntegrationExternalIdentity, error) {
	r.upsertIdentityCalls++
	if r.failUpsertIdentityAt == r.upsertIdentityCalls {
		return integrationmodel.IntegrationExternalIdentity{}, errGoogleOAuthRepository
	}
	return r.independentConfigRepository.UpsertExternalIdentity(ctx, workspaceID, value)
}

func googleOAuthBaseRepository(connection integrationmodel.IntegrationConnection) *googleOAuthFailureRepository {
	return &googleOAuthFailureRepository{independentConfigRepository: &independentConfigRepository{
		connections: map[string]integrationmodel.IntegrationConnection{connection.Key: connection},
		secrets: map[string]integrationmodel.IntegrationSecret{
			"id":     {Key: "id", WorkspaceID: connection.WorkspaceID, Status: "active", ValueRef: "material:id"},
			"secret": {Key: "secret", WorkspaceID: connection.WorkspaceID, Status: "active", ValueRef: "material:secret"},
		},
		materials: map[string]string{connection.WorkspaceID + ":id": "client", connection.WorkspaceID + ":secret": "signer"},
	}}
}

func googleOAuthAdmin() principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin", WorkspaceID: "workspace"}}, accessfixture.Bundle{Permissions: []string{PermissionConnectionManage, PermissionSecretManage}})
}

func TestGoogleOAuthRepositoryFailureBranches(t *testing.T) {
	connection := integrationmodel.IntegrationConnection{Key: "gmail", WorkspaceID: "workspace", ConnectorKey: "google_workspace", ProviderKey: "google", Status: "configured", SecretRefs: map[string]string{"client_id": "secret:id", "client_secret": "secret:secret"}}
	for name, configure := range map[string]func(*googleOAuthFailureRepository){
		"connections": func(r *googleOAuthFailureRepository) { r.failListConnections = true },
		"secrets":     func(r *googleOAuthFailureRepository) { r.failListSecrets = true },
		"material":    func(r *googleOAuthFailureRepository) { r.failResolve = true },
	} {
		t.Run(name, func(t *testing.T) {
			repo := googleOAuthBaseRepository(connection)
			configure(repo)
			service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repo})
			if _, err := service.StartGoogleOAuthAuthorization(t.Context(), "gmail", integrationmodel.IntegrationGoogleOAuthStartRequest{RedirectURI: "https://app.example/cb"}, googleOAuthAdmin()); err == nil {
				t.Fatal("repository failure accepted")
			}
		})
	}
}

func TestGoogleOAuthCompleteGuardAndNonceBranches(t *testing.T) {
	connection := integrationmodel.IntegrationConnection{Key: "gmail", WorkspaceID: "workspace", ConnectorKey: "google_workspace", ProviderKey: "google", Status: "configured", Config: map[string]any{"token_url": "http://127.0.0.1:1"}, SecretRefs: map[string]string{"client_id": "secret:id", "client_secret": "secret:secret"}}
	state := googleOAuthState{WorkspaceID: "workspace", ConnectionKey: "gmail", ActorID: "admin", RedirectURI: "https://app.example/cb", Nonce: "nonce", ExpiresAt: time.Now().Add(time.Minute).Unix()}
	encoded, _ := encodeGoogleOAuthState(state, "signer")
	if _, err := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: googleOAuthBaseRepository(connection)}).CompleteGoogleOAuthAuthorization(t.Context(), "code", "bad"); err == nil {
		t.Fatal("invalid unsigned state accepted")
	}
	for name, configure := range map[string]func(*googleOAuthFailureRepository){
		"connection error":   func(r *googleOAuthFailureRepository) { r.failListConnections = true },
		"connection missing": func(r *googleOAuthFailureRepository) { delete(r.connections, "gmail") },
		"connection invalid": func(r *googleOAuthFailureRepository) {
			c := r.connections["gmail"]
			c.ProviderKey = "wrong"
			r.connections["gmail"] = c
		},
		"secrets": func(r *googleOAuthFailureRepository) { r.failListSecrets = true },
	} {
		t.Run(name, func(t *testing.T) {
			repo := googleOAuthBaseRepository(connection)
			configure(repo)
			service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repo, EventRepository: &googleOAuthNonceRepository{seen: map[string]bool{}}})
			if _, err := service.CompleteGoogleOAuthAuthorization(t.Context(), "code", encoded); err == nil {
				t.Fatal("failure accepted")
			}
		})
	}
	expired := state
	expired.ExpiresAt = time.Now().Add(-time.Minute).Unix()
	expiredState, _ := encodeGoogleOAuthState(expired, "signer")
	for name, test := range map[string]struct {
		code string
		repo integrationrepository.IntegrationEventRepository
	}{
		"expired":     {"code", &googleOAuthNonceRepository{seen: map[string]bool{}}},
		"code":        {"", &googleOAuthNonceRepository{seen: map[string]bool{}}},
		"events":      {"code", nil},
		"nonce error": {"code", &googleOAuthNonceRepository{seen: map[string]bool{}, err: errGoogleOAuthRepository}},
	} {
		t.Run(name, func(t *testing.T) {
			value := encoded
			if name == "expired" {
				value = expiredState
			}
			service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: googleOAuthBaseRepository(connection), EventRepository: test.repo})
			if _, err := service.CompleteGoogleOAuthAuthorization(t.Context(), test.code, value); err == nil {
				t.Fatal("guard accepted")
			}
		})
	}
}

func TestPersistGoogleOAuthTokenAndLinkFailureBranches(t *testing.T) {
	base := integrationmodel.IntegrationConnection{Key: "gmail", WorkspaceID: "workspace", ConnectorKey: "google_workspace", ProviderKey: "google", Status: "configured"}
	principal := googleOAuthAdmin()
	if err := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: googleOAuthBaseRepository(base)}).persistGoogleOAuthTokens(t.Context(), &base, googleOAuthTokenResponse{AccessToken: "a", RefreshToken: "r"}, "actor", "", principal); err == nil {
		t.Fatal("incomplete identity accepted")
	}
	for _, test := range []struct {
		name      string
		configure func(*googleOAuthFailureRepository)
		tokens    googleOAuthTokenResponse
	}{
		{"refresh lookup", func(r *googleOAuthFailureRepository) { r.failListSecrets = true }, googleOAuthTokenResponse{AccessToken: "a"}},
		{"refresh missing", func(*googleOAuthFailureRepository) {}, googleOAuthTokenResponse{AccessToken: "a"}},
		{"access material", func(r *googleOAuthFailureRepository) { r.failPut = true }, googleOAuthTokenResponse{AccessToken: "a", RefreshToken: "r"}},
		{"access secret", func(r *googleOAuthFailureRepository) { r.failUpsertSecretAt = 1 }, googleOAuthTokenResponse{AccessToken: "a", RefreshToken: "r"}},
		{"refresh secret", func(r *googleOAuthFailureRepository) { r.failUpsertSecretAt = 2 }, googleOAuthTokenResponse{AccessToken: "a", RefreshToken: "r"}},
		{"connection", func(r *googleOAuthFailureRepository) { r.failUpsertConnectionAt = 1 }, googleOAuthTokenResponse{AccessToken: "a", RefreshToken: "r"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			connection := base
			repo := googleOAuthBaseRepository(connection)
			test.configure(repo)
			service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repo})
			if err := service.persistGoogleOAuthTokens(t.Context(), &connection, test.tokens, "", "", principal); err == nil {
				t.Fatal("failure accepted")
			}
		})
	}
	connection := base
	repo := googleOAuthBaseRepository(connection)
	repo.secrets["gmail_refresh_token"] = integrationmodel.IntegrationSecret{Key: "gmail_refresh_token", WorkspaceID: "workspace", Status: "active"}
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repo})
	if err := service.persistGoogleOAuthTokens(t.Context(), &connection, googleOAuthTokenResponse{AccessToken: "a"}, "", "", principal); err != nil || connection.SecretRefs["refresh_token"] != "secret:gmail_refresh_token" {
		t.Fatalf("connection=%+v err=%v", connection, err)
	}

	missing := base
	missing.Config = map[string]any{"oauth_linked_connection_keys": []string{"missing"}}
	if _, err := service.linkGoogleOAuthConnections(t.Context(), missing, "", "", principal); err == nil {
		t.Fatal("missing linked connection accepted")
	}
	target := integrationmodel.IntegrationConnection{Key: "calendar", WorkspaceID: "workspace", ConnectorKey: "appointment_scheduling", ProviderKey: "google_calendar", Status: "configured"}
	for name, configure := range map[string]func(*googleOAuthFailureRepository){
		"target save":     func(r *googleOAuthFailureRepository) { r.failUpsertConnectionAt = 1 },
		"target identity": func(r *googleOAuthFailureRepository) { r.failUpsertIdentityAt = 1 },
	} {
		t.Run(name, func(t *testing.T) {
			source := base
			source.Config = map[string]any{"oauth_linked_connection_keys": "calendar"}
			source.SecretRefs = map[string]string{"access_token": "secret:a"}
			r := googleOAuthBaseRepository(source)
			r.connections["calendar"] = target
			configure(r)
			s := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: r})
			if _, err := s.linkGoogleOAuthConnections(t.Context(), source, "actor", "role", principal); err == nil {
				t.Fatal("linked persistence failure accepted")
			}
		})
	}
}

func TestGoogleOAuthCompletePersistsFailureFromCallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"access_token":"a","refresh_token":"r"}`))
	}))
	defer server.Close()
	connection := integrationmodel.IntegrationConnection{Key: "gmail", WorkspaceID: "workspace", ConnectorKey: "google_workspace", ProviderKey: "google", Status: "configured", Config: map[string]any{"token_url": server.URL}, SecretRefs: map[string]string{"client_id": "secret:id", "client_secret": "secret:secret"}}
	state := googleOAuthState{WorkspaceID: "workspace", ConnectionKey: "gmail", ActorID: "admin", RedirectURI: "https://app.example/cb", Nonce: "nonce", ExpiresAt: time.Now().Add(time.Minute).Unix()}
	encoded, _ := encodeGoogleOAuthState(state, "signer")
	repo := googleOAuthBaseRepository(connection)
	repo.failPut = true
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repo, EventRepository: &googleOAuthNonceRepository{seen: map[string]bool{}}})
	if _, err := service.CompleteGoogleOAuthAuthorization(t.Context(), "code", encoded); err == nil {
		t.Fatal("token persistence failure accepted")
	}
	repo = googleOAuthBaseRepository(connection)
	service = NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repo, EventRepository: &googleOAuthNonceRepository{seen: map[string]bool{}}})
	connection.Config["token_url"] = "http://127.0.0.1:1"
	repo.connections["gmail"] = connection
	if _, err := service.CompleteGoogleOAuthAuthorization(t.Context(), "code", encoded); err == nil {
		t.Fatal("token exchange failure accepted")
	}
}

func TestPersistGoogleOAuthLinkedAndIdentityFailures(t *testing.T) {
	principal := googleOAuthAdmin()
	base := integrationmodel.IntegrationConnection{Key: "gmail", WorkspaceID: "workspace", ConnectorKey: "google_workspace", ProviderKey: "google", Status: "configured", Config: map[string]any{"oauth_linked_connection_keys": "missing"}}
	repo := googleOAuthBaseRepository(base)
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repo})
	connection := base
	if err := service.persistGoogleOAuthTokens(t.Context(), &connection, googleOAuthTokenResponse{AccessToken: "a", RefreshToken: "r"}, "", "", principal); err == nil {
		t.Fatal("linked connection failure accepted")
	}
	base.Config = nil
	repo = googleOAuthBaseRepository(base)
	repo.failUpsertIdentityAt = 1
	service = NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repo})
	connection = base
	if err := service.persistGoogleOAuthTokens(t.Context(), &connection, googleOAuthTokenResponse{AccessToken: "a", RefreshToken: "r"}, "actor", "role", principal); err == nil {
		t.Fatal("source identity failure accepted")
	}
	base.Config = map[string]any{"oauth_linked_connection_keys": "calendar"}
	repo = googleOAuthBaseRepository(base)
	repo.failListConnections = true
	service = NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repo})
	if _, err := service.linkGoogleOAuthConnections(t.Context(), base, "", "", principal); err == nil {
		t.Fatal("linked connection lookup failure accepted")
	}
	for name, target := range map[string]integrationmodel.IntegrationConnection{
		"connector": {Key: "calendar", WorkspaceID: "workspace", ConnectorKey: "wrong", ProviderKey: "google_calendar", Status: "configured"},
		"disabled":  {Key: "calendar", WorkspaceID: "workspace", ConnectorKey: "appointment_scheduling", ProviderKey: "google_calendar", Status: "disabled"},
	} {
		t.Run(name, func(t *testing.T) {
			source := base
			source.Config = map[string]any{"oauth_linked_connection_keys": "calendar"}
			r := googleOAuthBaseRepository(source)
			r.connections["calendar"] = target
			if _, err := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: r}).linkGoogleOAuthConnections(t.Context(), source, "", "", principal); err == nil {
				t.Fatal("invalid target accepted")
			}
		})
	}
	for name, calendarID := range map[string]any{"empty": "", "custom": "custom"} {
		t.Run(name, func(t *testing.T) {
			source := base
			source.Config = map[string]any{"oauth_linked_connection_keys": "calendar"}
			target := integrationmodel.IntegrationConnection{Key: "calendar", WorkspaceID: "workspace", ConnectorKey: "appointment_scheduling", ProviderKey: "google_calendar", Status: "configured", Config: map[string]any{"calendar_id": calendarID}}
			r := googleOAuthBaseRepository(source)
			r.connections["calendar"] = target
			linked, err := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: r}).linkGoogleOAuthConnections(t.Context(), source, "", "", principal)
			if err != nil || len(linked) != 1 {
				t.Fatalf("linked=%v err=%v", linked, err)
			}
		})
	}
}
