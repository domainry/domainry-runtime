package integrations_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	integrationapplication "github.com/domainry/domainry-runtime/runtime/application/integration"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	integrationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/integration"
)

func TestGoogleOAuthHTTPStartAndCallbackContracts(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"access","refresh_token":"refresh","expires_in":3600}`))
	}))
	defer tokenServer.Close()
	store, _ := newIntegrationEventHTTPApplication(t)
	defer store.Close()
	repository := integrationpersistence.NewIntegrationConfigStore(store)
	for _, secret := range []integrationmodel.IntegrationSecret{
		{Key: "google_client_id", WorkspaceID: "workspace-1", Kind: "identifier", Status: "active", ValueRef: "material:google_client_id"},
		{Key: "google_client_secret", WorkspaceID: "workspace-1", Kind: "oauth_client_secret", Status: "active", ValueRef: "material:google_client_secret"},
	} {
		if _, err := repository.UpsertSecret(t.Context(), "workspace-1", secret); err != nil {
			t.Fatal(err)
		}
	}
	if err := repository.PutSecretMaterial(t.Context(), "workspace-1", "google_client_id", "client-id"); err != nil {
		t.Fatal(err)
	}
	if err := repository.PutSecretMaterial(t.Context(), "workspace-1", "google_client_secret", "client-secret"); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.UpsertConnection(t.Context(), "workspace-1", integrationmodel.IntegrationConnection{
		Key: "gmail", WorkspaceID: "workspace-1", ConnectorKey: "google_workspace", ProviderKey: "google", Status: "configured",
		Config: map[string]any{"token_url": tokenServer.URL}, SecretRefs: map[string]string{"client_id": "secret:google_client_id", "client_secret": "secret:google_client_secret"},
	}); err != nil {
		t.Fatal(err)
	}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1", UserID: "admin"}}, accessfixture.Bundle{Permissions: []string{integrationapplication.PermissionConnectionManage, integrationapplication.PermissionSecretManage}})
	application := integrationapplication.NewIntegrationApplicationService(integrationapplication.ApplicationDependencies{
		ConfigRepository:  repository,
		EventRepository:   integrationpersistence.NewIntegrationEventStore(store),
		PrincipalResolver: func(context.Context, string, string, string) principalmodel.Principal { return principal },
	})
	call := integrationManagementHTTPCall(application, &principal)
	if response := call(http.MethodPost, "/tenant-admin/integrations/connections/gmail/oauth/google/start", `{`, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid start status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodGet, "/integrations/google/oauth/callback?error=access_denied", "", nil); response.Code != http.StatusBadRequest {
		t.Fatalf("provider rejection status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodGet, "/integrations/google/oauth/callback?state=bad&code=bad", "", nil); response.Code != http.StatusForbidden {
		t.Fatalf("invalid callback status=%d body=%s", response.Code, response.Body.String())
	}

	start := call(http.MethodPost, "/tenant-admin/integrations/connections/gmail/oauth/google/start", `{"redirect_uri":"https://app.example/integrations/google/oauth/callback"}`, nil)
	var started integrationmodel.IntegrationGoogleOAuthStartResult
	if start.Code != http.StatusOK || json.Unmarshal(start.Body.Bytes(), &started) != nil {
		t.Fatalf("start status=%d body=%s", start.Code, start.Body.String())
	}
	state := oauthState(t, started.AuthorizationURL)
	completed := call(http.MethodGet, "/integrations/google/oauth/callback?code=code&state="+url.QueryEscape(state), "", nil)
	if completed.Code != http.StatusOK {
		t.Fatalf("JSON callback status=%d body=%s", completed.Code, completed.Body.String())
	}

	start = call(http.MethodPost, "/tenant-admin/integrations/connections/gmail/oauth/google/start", `{"redirect_uri":"https://app.example/integrations/google/oauth/callback"}`, nil)
	if start.Code != http.StatusOK || json.Unmarshal(start.Body.Bytes(), &started) != nil {
		t.Fatalf("second start status=%d body=%s", start.Code, start.Body.String())
	}
	state = oauthState(t, started.AuthorizationURL)
	html := call(http.MethodGet, "/integrations/google/oauth/callback?code=code&state="+url.QueryEscape(state), "", map[string]string{"Accept": "text/html,application/xhtml+xml"})
	if html.Code != http.StatusOK || html.Header().Get("Content-Security-Policy") == "" {
		t.Fatalf("HTML callback status=%d headers=%v body=%s", html.Code, html.Header(), html.Body.String())
	}
	accessfixture.Set(&principal, accessfixture.Bundle{})
	denied := call(http.MethodPost, "/tenant-admin/integrations/connections/gmail/oauth/google/start", `{"redirect_uri":"https://app.example/integrations/google/oauth/callback"}`, nil)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("denied start status=%d body=%s", denied.Code, denied.Body.String())
	}
}

func oauthState(t *testing.T, authorizationURL string) string {
	t.Helper()
	parsed, err := url.Parse(authorizationURL)
	if err != nil || parsed.Query().Get("state") == "" {
		t.Fatalf("authorization URL=%q err=%v", authorizationURL, err)
	}
	return parsed.Query().Get("state")
}
