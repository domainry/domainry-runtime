package integration

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

// LookupConnection preserves the legacy best-effort lookup contract used by
// cross-domain adapters. Business commands use findConnection directly so
// repository and normalization failures remain visible.
func (s *IntegrationApplicationService) LookupConnection(ctx context.Context, connectionKey, workspaceID string) (integrationmodel.IntegrationConnection, bool) {
	workspaceID = strings.TrimSpace(workspaceID)
	if integrationAuthorizeWorkspaceQuery(workspaceID) != nil {
		return integrationmodel.IntegrationConnection{}, false
	}
	connection, ok, _ := s.findConnection(ctx, connectionKey, workspaceID)
	return connection, ok
}

func (s *IntegrationApplicationService) LookupSecret(ctx context.Context, secretKey, workspaceID string) (integrationmodel.IntegrationSecret, bool) {
	workspaceID = strings.TrimSpace(workspaceID)
	if integrationAuthorizeWorkspaceQuery(workspaceID) != nil {
		return integrationmodel.IntegrationSecret{}, false
	}
	secret, ok, _ := s.findSecret(ctx, secretKey, workspaceID)
	return secret, ok
}

func validateGoogleOAuthRedirectURI(value string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.User != nil || parsed.Host == "" {
		return "", badRequest("backend.integration.google_oauth.redirect_uri_invalid")
	}
	hostname := strings.ToLower(parsed.Hostname())
	if parsed.Scheme != "https" && !(parsed.Scheme == "http" && (hostname == "127.0.0.1" || hostname == "localhost")) {
		return "", badRequest("backend.integration.google_oauth.redirect_uri_invalid")
	}
	return parsed.String(), nil
}

func encodeGoogleOAuthState(state googleOAuthState, secret string) (string, error) {
	payload, err := googleOAuthMarshal(state)
	if err != nil {
		return "", fmt.Errorf("backend.integration.google_oauth.state_unavailable: %w", err)
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func decodeUnsignedGoogleOAuthState(value string) (googleOAuthState, error) {
	parts := strings.Split(strings.TrimSpace(value), ".")
	if len(parts) != 2 {
		return googleOAuthState{}, forbidden("backend.integration.google_oauth.state_invalid")
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil || len(payload) > 4096 {
		return googleOAuthState{}, forbidden("backend.integration.google_oauth.state_invalid")
	}
	var state googleOAuthState
	if json.Unmarshal(payload, &state) != nil {
		return googleOAuthState{}, forbidden("backend.integration.google_oauth.state_invalid")
	}
	if len(strings.TrimSpace(state.WorkspaceID)) == 0 {
		return googleOAuthState{}, forbidden("backend.integration.google_oauth.state_invalid")
	}
	if strings.TrimSpace(state.ConnectionKey) == "" || strings.TrimSpace(state.Nonce) == "" {
		return googleOAuthState{}, forbidden("backend.integration.google_oauth.state_invalid")
	}
	return state, nil
}

func decodeGoogleOAuthState(value, secret string) (googleOAuthState, error) {
	parts := strings.Split(strings.TrimSpace(value), ".")
	if len(parts) != 2 || strings.TrimSpace(secret) == "" {
		return googleOAuthState{}, forbidden("backend.integration.google_oauth.state_invalid")
	}
	signature, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil || base64.RawURLEncoding.EncodeToString(signature) != parts[1] {
		return googleOAuthState{}, forbidden("backend.integration.google_oauth.state_invalid")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(parts[0]))
	if !hmac.Equal(signature, mac.Sum(nil)) {
		return googleOAuthState{}, forbidden("backend.integration.google_oauth.state_invalid")
	}
	return decodeUnsignedGoogleOAuthState(value)
}

func exchangeGoogleOAuthCode(ctx context.Context, endpoint, code, clientID, clientSecret, redirectURI string) (googleOAuthTokenResponse, error) {
	values := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "client_id": {clientID}, "client_secret": {clientSecret}, "redirect_uri": {redirectURI}}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(values.Encode()))
	if err != nil {
		return googleOAuthTokenResponse{}, badRequest("backend.integration.google_oauth.exchange_failed")
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := googleOAuthHTTPDo(request)
	if err != nil {
		return googleOAuthTokenResponse{}, fmt.Errorf("backend.integration.google_oauth.exchange_failed: %w", err)
	}
	defer response.Body.Close()
	body, err := googleOAuthReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil || response.StatusCode < 200 || response.StatusCode >= 300 {
		return googleOAuthTokenResponse{}, badRequest("backend.integration.google_oauth.exchange_failed")
	}
	var tokens googleOAuthTokenResponse
	if json.Unmarshal(body, &tokens) != nil || strings.TrimSpace(tokens.AccessToken) == "" {
		return googleOAuthTokenResponse{}, badRequest("backend.integration.google_oauth.exchange_failed")
	}
	return tokens, nil
}

func googleConnectionConfig(config map[string]any, key, otherwise string) string {
	if value := strings.TrimSpace(fmt.Sprint(config[key])); value != "" && value != "<nil>" {
		return value
	}
	return otherwise
}

func validGoogleOAuthConnection(connection integrationmodel.IntegrationConnection) bool {
	return connection.ConnectorKey == "google_workspace" && connection.ProviderKey == "google" && connection.Status != "disabled"
}

func googleOAuthFingerprint(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
