package integration

import (
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestGoogleOAuthPureHelperBranches(t *testing.T) {
	if googleOAuthLinkedConnectionKeys(nil) != nil {
		t.Fatal("missing linked connections changed")
	}
	for _, config := range []map[string]any{
		{"oauth_linked_connection_keys": []string{" one ", "", "one", "two"}},
		{"oauth_linked_connection_keys": []any{"one", 2}},
		{"oauth_linked_connection_keys": "one, two"},
		{"oauth_linked_connection_keys": 1},
	} {
		_ = googleOAuthLinkedConnectionKeys(config)
	}
	for _, value := range []string{"%", "https://app.example/cb?q=1", "https://app.example/cb#x", "https://u@app.example/cb", "/cb", "ftp://app.example/cb", "http://remote.example/cb"} {
		if _, err := validateGoogleOAuthRedirectURI(value); err == nil {
			t.Fatalf("redirect %q accepted", value)
		}
	}
	for _, value := range []string{"https://app.example/cb", "http://localhost/cb", "http://127.0.0.1:18287/cb"} {
		if got, err := validateGoogleOAuthRedirectURI(value); err != nil || got != value {
			t.Fatalf("redirect=%q got=%q err=%v", value, got, err)
		}
	}
	state := googleOAuthState{WorkspaceID: "workspace", ConnectionKey: "connection", Nonce: "nonce", ExpiresAt: time.Now().Add(time.Minute).Unix()}
	encoded, err := encodeGoogleOAuthState(state, "secret")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := decodeGoogleOAuthState(encoded, "secret"); err != nil || got.Nonce != "nonce" {
		t.Fatalf("state=%+v err=%v", got, err)
	}
	invalidPayloads := []string{
		"missing-dot", "%.signature",
		base64.RawURLEncoding.EncodeToString([]byte(strings.Repeat("x", 4097))) + ".signature",
		base64.RawURLEncoding.EncodeToString([]byte("{")) + ".signature",
	}
	for _, value := range invalidPayloads {
		if _, err := decodeUnsignedGoogleOAuthState(value); err == nil {
			t.Fatalf("unsigned state %q accepted", value)
		}
	}
	for _, incomplete := range []googleOAuthState{{ConnectionKey: "c", Nonce: "n"}, {WorkspaceID: "w", Nonce: "n"}, {WorkspaceID: "w", ConnectionKey: "c"}} {
		payload, _ := googleOAuthMarshal(incomplete)
		if _, err := decodeUnsignedGoogleOAuthState(base64.RawURLEncoding.EncodeToString(payload) + ".x"); err == nil {
			t.Fatalf("incomplete state %+v accepted", incomplete)
		}
	}
	for _, pair := range [][2]string{{encoded, ""}, {"x.%", "secret"}, {encoded, "wrong"}} {
		if _, err := decodeGoogleOAuthState(pair[0], pair[1]); err == nil {
			t.Fatalf("signed state accepted: %#v", pair)
		}
	}
	if _, err := decodeGoogleOAuthState("bad", "secret"); err == nil {
		t.Fatal("state without signature accepted")
	}
	if googleConnectionConfig(nil, "key", "fallback") != "fallback" || googleConnectionConfig(map[string]any{"key": ""}, "key", "fallback") != "fallback" || googleConnectionConfig(map[string]any{"key": " value "}, "key", "fallback") != "value" || googleConnectionConfig(map[string]any{"key": nil}, "key", "fallback") != "fallback" {
		t.Fatal("connection config helper changed")
	}
	if !validGoogleOAuthConnection(integrationmodel.IntegrationConnection{ConnectorKey: "google_workspace", ProviderKey: "google", Status: "active"}) || validGoogleOAuthConnection(integrationmodel.IntegrationConnection{ConnectorKey: "google_workspace", ProviderKey: "google", Status: "disabled"}) || googleOAuthFingerprint("x") == "" {
		t.Fatal("connection identity helper changed")
	}
}

func TestGoogleOAuthExchangeBranches(t *testing.T) {
	if _, err := exchangeGoogleOAuthCode(t.Context(), "%", "code", "id", "secret", "https://app/cb"); err == nil {
		t.Fatal("invalid token endpoint accepted")
	}
	if _, err := exchangeGoogleOAuthCode(t.Context(), "http://127.0.0.1:1", "code", "id", "secret", "https://app/cb"); err == nil {
		t.Fatal("unreachable token endpoint accepted")
	}
	for name, response := range map[string]struct {
		status int
		body   string
	}{
		"status":  {http.StatusBadRequest, `{}`},
		"json":    {http.StatusOK, `{`},
		"token":   {http.StatusOK, `{}`},
		"success": {http.StatusOK, `{"access_token":"access"}`},
	} {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(response.status)
				_, _ = w.Write([]byte(response.body))
			}))
			defer server.Close()
			tokens, err := exchangeGoogleOAuthCode(t.Context(), server.URL, "code", "id", "secret", "https://app/cb")
			if name == "success" {
				if err != nil || tokens.AccessToken != "access" {
					t.Fatalf("tokens=%+v err=%v", tokens, err)
				}
			} else if err == nil {
				t.Fatal("invalid token response accepted")
			}
		})
	}
	originalReadAll := googleOAuthReadAll
	googleOAuthReadAll = func(io.Reader) ([]byte, error) { return nil, errors.New("read") }
	t.Cleanup(func() { googleOAuthReadAll = originalReadAll })
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	defer server.Close()
	if _, err := exchangeGoogleOAuthCode(t.Context(), server.URL, "code", "id", "secret", "https://app/cb"); err == nil {
		t.Fatal("token response read failure accepted")
	}
	googleOAuthReadAll = originalReadAll
	originalHTTPDo := googleOAuthHTTPDo
	googleOAuthHTTPDo = func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 199, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
	}
	t.Cleanup(func() { googleOAuthHTTPDo = originalHTTPDo })
	if _, err := exchangeGoogleOAuthCode(t.Context(), "https://token.example", "code", "id", "secret", "https://app/cb"); err == nil {
		t.Fatal("informational token response accepted")
	}
}

func TestGoogleOAuthStartAuthorizationGuardBranches(t *testing.T) {
	baseConnection := integrationmodel.IntegrationConnection{Key: "gmail", WorkspaceID: "workspace-primary", ConnectorKey: "google_workspace", ProviderKey: "google", Status: "configured", SecretRefs: map[string]string{"client_id": "secret:id", "client_secret": "secret:secret"}}
	baseRepo := func(connection integrationmodel.IntegrationConnection) *independentConfigRepository {
		return &independentConfigRepository{connections: map[string]integrationmodel.IntegrationConnection{connection.Key: connection}, secrets: map[string]integrationmodel.IntegrationSecret{"id": {Key: "id", WorkspaceID: "workspace-primary", Status: "active", ValueRef: "material:id"}, "secret": {Key: "secret", WorkspaceID: "workspace-primary", Status: "active", ValueRef: "material:secret"}}, materials: map[string]string{"workspace-primary:id": "client", "workspace-primary:secret": "signer"}}
	}
	admin := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "admin", WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Permissions: []string{PermissionConnectionManage, PermissionSecretManage}})
	for name, principal := range map[string]principalmodel.Principal{
		"unknown":               principalmodel.Principal{},
		"connection permission": accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Permissions: []string{PermissionSecretManage}}),
		"secret permission":     accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-primary"}}, accessfixture.Bundle{Permissions: []string{PermissionConnectionManage}}),
	} {
		t.Run(name, func(t *testing.T) {
			service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: baseRepo(baseConnection)})
			if _, err := service.StartGoogleOAuthAuthorization(t.Context(), "gmail", integrationmodel.IntegrationGoogleOAuthStartRequest{RedirectURI: "https://app.example/cb"}, principal); err == nil {
				t.Fatal("unauthorized OAuth start accepted")
			}
		})
	}
	service := NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: baseRepo(baseConnection)})
	for name, key := range map[string]string{"missing": "missing", "invalid": "wrong"} {
		if name == "invalid" {
			repo := baseRepo(baseConnection)
			wrong := baseConnection
			wrong.Key, wrong.ProviderKey = key, "wrong"
			repo.connections[key] = wrong
			service = NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repo})
		}
		if _, err := service.StartGoogleOAuthAuthorization(t.Context(), key, integrationmodel.IntegrationGoogleOAuthStartRequest{RedirectURI: "https://app.example/cb"}, admin); err == nil {
			t.Fatalf("%s connection accepted", name)
		}
	}
	repo := baseRepo(baseConnection)
	repo.materials["workspace-primary:id"] = ""
	service = NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repo})
	if _, err := service.StartGoogleOAuthAuthorization(t.Context(), "gmail", integrationmodel.IntegrationGoogleOAuthStartRequest{RedirectURI: "https://app.example/cb"}, admin); err == nil {
		t.Fatal("empty client credentials accepted")
	}
	repo = baseRepo(baseConnection)
	repo.materials["workspace-primary:secret"] = ""
	service = NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: repo})
	if _, err := service.StartGoogleOAuthAuthorization(t.Context(), "gmail", integrationmodel.IntegrationGoogleOAuthStartRequest{RedirectURI: "https://app.example/cb"}, admin); err == nil {
		t.Fatal("empty client secret accepted")
	}
	service = NewIntegrationApplicationService(ApplicationDependencies{ConfigRepository: baseRepo(baseConnection)})
	for _, request := range []integrationmodel.IntegrationGoogleOAuthStartRequest{
		{RedirectURI: "https://app.example/cb", EventActorID: "actor"},
		{RedirectURI: "https://app.example/cb", EventRoleKey: "role"},
	} {
		if _, err := service.StartGoogleOAuthAuthorization(t.Context(), "gmail", request, admin); err == nil {
			t.Fatal("incomplete event identity accepted")
		}
	}
	originalRead := googleOAuthRandRead
	googleOAuthRandRead = func([]byte) (int, error) { return 0, errors.New("random") }
	t.Cleanup(func() { googleOAuthRandRead = originalRead })
	if _, err := service.StartGoogleOAuthAuthorization(t.Context(), "gmail", integrationmodel.IntegrationGoogleOAuthStartRequest{RedirectURI: "https://app.example/cb"}, admin); err == nil {
		t.Fatal("random failure accepted")
	}
	googleOAuthRandRead = originalRead
	originalMarshal := googleOAuthMarshal
	googleOAuthMarshal = func(any) ([]byte, error) { return nil, errors.New("marshal") }
	t.Cleanup(func() { googleOAuthMarshal = originalMarshal })
	if _, err := service.StartGoogleOAuthAuthorization(t.Context(), "gmail", integrationmodel.IntegrationGoogleOAuthStartRequest{RedirectURI: "https://app.example/cb"}, admin); err == nil {
		t.Fatal("state marshal failure accepted")
	}
}
