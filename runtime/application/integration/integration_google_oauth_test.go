package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type googleOAuthNonceRepository struct {
	integrationrepository.IntegrationEventRepository
	seen map[string]bool
	err  error
}

func (r *googleOAuthNonceRepository) RecordWebhookNonce(_ context.Context, workspaceID, connectorKey, nonce, _, _ string) (bool, error) {
	if r.err != nil {
		return false, r.err
	}
	key := workspaceID + ":" + connectorKey + ":" + nonce
	duplicate := r.seen[key]
	r.seen[key] = true
	return duplicate, nil
}

func TestGoogleOAuthAuthorizationActivatesManagedGmailIngestion(t *testing.T) {
	var received url.Values
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Body == nil {
			t.Fatalf("token request=%s", r.Method)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		received = r.Form
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"access-value","refresh_token":"refresh-value","expires_in":3600}`))
	}))
	defer tokenServer.Close()

	connection := integrationmodel.IntegrationConnection{
		Key: "gmail_primary", WorkspaceID: "default", ConnectorKey: "google_workspace", ProviderKey: "google", Status: "configured",
		Config: map[string]any{"token_url": tokenServer.URL, "oauth_linked_connection_keys": []any{"google_calendar_primary", "google_calendar_primary"}}, SecretRefs: map[string]string{"client_id": "secret:google_client_id", "client_secret": "secret:google_client_secret"},
	}
	calendarConnection := integrationmodel.IntegrationConnection{Key: "google_calendar_primary", WorkspaceID: "default", ConnectorKey: "appointment_scheduling", ProviderKey: "google_calendar", Status: "configured", Config: map[string]any{}, SecretRefs: map[string]string{}}
	repository := &independentConfigRepository{
		connections: map[string]integrationmodel.IntegrationConnection{connection.Key: connection, calendarConnection.Key: calendarConnection},
		secrets: map[string]integrationmodel.IntegrationSecret{
			"google_client_id":     {Key: "google_client_id", WorkspaceID: "default", Kind: "identifier", Status: "active", ValueRef: "material:google_client_id"},
			"google_client_secret": {Key: "google_client_secret", WorkspaceID: "default", Kind: "oauth_client_secret", Status: "active", ValueRef: "material:google_client_secret"},
		},
		materials: map[string]string{"default:google_client_id": "client-id", "default:google_client_secret": "client-secret"},
	}
	events := &googleOAuthNonceRepository{seen: map[string]bool{}}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin", WorkspaceID: "default"}}, accessfixture.Bundle{Permissions: []string{PermissionConnectionManage, PermissionSecretManage}})
	service := NewIntegrationApplicationService(ApplicationDependencies{
		ConfigRepository: repository,
		EventRepository:  events,
		PrincipalResolver: func(context.Context, string, string, string) principalmodel.Principal {
			return principal
		},
	})
	if !principal.HasPermission(PermissionConnectionManage) || !principal.HasPermission(PermissionSecretManage) {
		t.Fatalf("SDK fixture did not materialize OAuth permissions: %+v", principal.AccessBundle)
	}

	started, err := service.StartGoogleOAuthAuthorization(t.Context(), connection.Key, integrationmodel.IntegrationGoogleOAuthStartRequest{RedirectURI: "http://127.0.0.1:18287/integrations/google/oauth/callback", EventActorID: "agent_worker", EventRoleKey: "agent_service"}, principal)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(started.AuthorizationURL)
	if err != nil || parsed.Host != "accounts.google.com" || parsed.Query().Get("access_type") != "offline" || parsed.Query().Get("prompt") != "consent" || !strings.Contains(parsed.Query().Get("scope"), "gmail.send") || !strings.Contains(parsed.Query().Get("scope"), "calendar.events") {
		t.Fatalf("authorization url=%q error=%v", started.AuthorizationURL, err)
	}
	if strings.Contains(started.AuthorizationURL, "client-secret") || parsed.Query().Get("state") == "" {
		t.Fatal("authorization URL leaked secret or omitted state")
	}

	completed, err := service.CompleteGoogleOAuthAuthorization(t.Context(), "authorization-code", parsed.Query().Get("state"))
	if err != nil {
		t.Fatal(err)
	}
	if completed.ConnectionKey != connection.Key || completed.Status != "active" {
		t.Fatalf("completed=%+v", completed)
	}
	if received.Get("code") != "authorization-code" || received.Get("client_id") != "client-id" || received.Get("client_secret") != "client-secret" || received.Get("redirect_uri") == "" {
		t.Fatalf("token form=%v", received)
	}
	saved := repository.connections[connection.Key]
	if saved.Status != "active" || saved.Config["gmail_ingest_enabled"] != true || saved.Config["gmail_watch_enabled"] != false || saved.SecretRefs["access_token"] != "secret:gmail_primary_access_token" || saved.SecretRefs["refresh_token"] != "secret:gmail_primary_refresh_token" {
		t.Fatalf("saved connection=%+v", saved)
	}
	if repository.materials["default:gmail_primary_access_token"] != "access-value" || repository.materials["default:gmail_primary_refresh_token"] != "refresh-value" {
		t.Fatalf("stored token material keys=%v", repository.materials)
	}
	calendar := repository.connections[calendarConnection.Key]
	if calendar.Status != "active" || calendar.Config["calendar_id"] != "primary" || calendar.Config["meeting_enabled"] != true || calendar.Config["oauth_source_connection_key"] != connection.Key {
		t.Fatalf("linked calendar connection=%+v", calendar)
	}
	if calendar.SecretRefs["access_token"] != "secret:gmail_primary_access_token" || calendar.SecretRefs["refresh_token"] != "secret:gmail_primary_refresh_token" || calendar.SecretRefs["oauth_client_id"] != "secret:google_client_id" || calendar.SecretRefs["oauth_client_secret"] != "secret:google_client_secret" {
		t.Fatalf("linked calendar secret refs=%+v", calendar.SecretRefs)
	}
	identity := repository.identities["google_gmail_primary"]
	if identity.Provider != "google" || identity.ExternalSubject != "gmail_primary" || identity.ActorID != "agent_worker" || identity.RoleKey != "agent_service" || identity.Status != "active" {
		t.Fatalf("event identity=%+v", identity)
	}
	calendarIdentity := repository.identities["google_calendar_google_calendar_primary"]
	if calendarIdentity.Provider != "google_calendar" || calendarIdentity.ExternalSubject != "google_calendar_primary" || calendarIdentity.ActorID != "agent_worker" || calendarIdentity.RoleKey != "agent_service" || calendarIdentity.Status != "active" {
		t.Fatalf("calendar event identity=%+v", calendarIdentity)
	}
	if _, err := service.CompleteGoogleOAuthAuthorization(t.Context(), "authorization-code", parsed.Query().Get("state")); apperror.CodeOf(err) != "backend.integration.google_oauth.state_replayed" {
		t.Fatalf("replay error=%v", err)
	}
}

func TestGoogleOAuthAuthorizationRejectsInvalidLinkedConnection(t *testing.T) {
	connection := integrationmodel.IntegrationConnection{Key: "gmail", WorkspaceID: "default", ConnectorKey: "google_workspace", ProviderKey: "google", Status: "active", Config: map[string]any{"oauth_linked_connection_keys": []any{"wrong"}}, SecretRefs: map[string]string{"access_token": "secret:access", "refresh_token": "secret:refresh", "client_id": "secret:id", "client_secret": "secret:secret"}}
	wrong := integrationmodel.IntegrationConnection{Key: "wrong", WorkspaceID: "default", ConnectorKey: "appointment_scheduling", ProviderKey: "feishu_calendar", Status: "configured"}
	repository := &independentConfigRepository{connections: map[string]integrationmodel.IntegrationConnection{"gmail": connection, "wrong": wrong}}
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository})
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin", WorkspaceID: "default"}}
	if _, err := service.linkGoogleOAuthConnections(t.Context(), connection, "agent_worker", "agent_service", principal); apperror.CodeOf(err) != "backend.integration.google_oauth.linked_connection_invalid" {
		t.Fatalf("invalid linked connection error=%v", err)
	}
}

func TestGoogleOAuthAuthorizationRejectsUnsafeRedirectAndTamperedState(t *testing.T) {
	connection := integrationmodel.IntegrationConnection{Key: "gmail", WorkspaceID: "default", ConnectorKey: "google_workspace", ProviderKey: "google", Status: "configured", SecretRefs: map[string]string{"client_id": "secret:id", "client_secret": "secret:secret"}}
	repository := &independentConfigRepository{
		connections: map[string]integrationmodel.IntegrationConnection{"gmail": connection},
		secrets: map[string]integrationmodel.IntegrationSecret{
			"id":     {Key: "id", WorkspaceID: "default", Status: "active", ValueRef: "material:id"},
			"secret": {Key: "secret", WorkspaceID: "default", Status: "active", ValueRef: "material:secret"},
		},
		materials: map[string]string{"default:id": "client", "default:secret": "signer"},
	}
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repository, EventRepository: &googleOAuthNonceRepository{seen: map[string]bool{}}})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin", WorkspaceID: "default"}}, accessfixture.Bundle{Permissions: []string{PermissionConnectionManage, PermissionSecretManage}})
	if _, err := service.StartGoogleOAuthAuthorization(t.Context(), "gmail", integrationmodel.IntegrationGoogleOAuthStartRequest{RedirectURI: "http://remote.example/callback"}, principal); apperror.CodeOf(err) != "backend.integration.google_oauth.redirect_uri_invalid" {
		t.Fatalf("unsafe redirect error=%v", err)
	}
	started, err := service.StartGoogleOAuthAuthorization(t.Context(), "gmail", integrationmodel.IntegrationGoogleOAuthStartRequest{RedirectURI: "https://app.example/integrations/google/oauth/callback"}, principal)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(started.AuthorizationURL)
	state := parsed.Query().Get("state")
	state = state[:len(state)-1] + "x"
	if _, err := service.CompleteGoogleOAuthAuthorization(t.Context(), "code", state); apperror.CodeOf(err) != "backend.integration.google_oauth.state_invalid" {
		t.Fatalf("tampered state error=%v", err)
	}
}
