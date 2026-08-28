package integration

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

const googleOAuthAuthorizationURL = "https://accounts.google.com/o/oauth2/v2/auth"

var googleOAuthScopes = []string{
	"openid", "email", "profile",
	"https://www.googleapis.com/auth/gmail.readonly",
	"https://www.googleapis.com/auth/gmail.send",
	"https://www.googleapis.com/auth/calendar.events",
	"https://www.googleapis.com/auth/pubsub",
}

var (
	googleOAuthRandRead = rand.Read
	googleOAuthMarshal  = json.Marshal
	googleOAuthReadAll  = io.ReadAll
	googleOAuthHTTPDo   = func(request *http.Request) (*http.Response, error) {
		return (&http.Client{Timeout: 30 * time.Second}).Do(request)
	}
)

type googleOAuthState struct {
	WorkspaceID   string `json:"workspace_id"`
	ConnectionKey string `json:"connection_key"`
	ActorID       string `json:"actor_id"`
	EventActorID  string `json:"event_actor_id,omitempty"`
	EventRoleKey  string `json:"event_role_key,omitempty"`
	RedirectURI   string `json:"redirect_uri"`
	Nonce         string `json:"nonce"`
	ExpiresAt     int64  `json:"expires_at"`
}

type googleOAuthTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int64  `json:"expires_in"`
}

// StartGoogleOAuthAuthorization returns a Google authorization URL while all
// client credentials remain behind the Integration secret boundary. The state
// is restart-safe, short-lived, signed by the connection's client secret and
// consumed once through the durable nonce store.
func (s *IntegrationApplicationService) StartGoogleOAuthAuthorization(ctx context.Context, connectionKey string, req integrationmodel.IntegrationGoogleOAuthStartRequest, principal principalmodel.Principal) (integrationmodel.IntegrationGoogleOAuthStartResult, error) {
	if err := integrationAuthorizeCommand(principal); err != nil {
		return integrationmodel.IntegrationGoogleOAuthStartResult{}, err
	}
	if !HasPermission(principal, PermissionConnectionManage) || !HasPermission(principal, PermissionSecretManage) {
		return integrationmodel.IntegrationGoogleOAuthStartResult{}, forbidden("auth.permission_denied")
	}
	redirectURI, err := validateGoogleOAuthRedirectURI(req.RedirectURI)
	if err != nil {
		return integrationmodel.IntegrationGoogleOAuthStartResult{}, err
	}
	connection, found, err := s.findConnection(ctx, strings.TrimSpace(connectionKey), principalWorkspaceID(principal))
	if err != nil {
		return integrationmodel.IntegrationGoogleOAuthStartResult{}, err
	}
	if !found {
		return integrationmodel.IntegrationGoogleOAuthStartResult{}, notFound("backend.integration.connection.not_found")
	}
	if !validGoogleOAuthConnection(connection) {
		return integrationmodel.IntegrationGoogleOAuthStartResult{}, badRequest("backend.integration.google_oauth.connection_invalid")
	}
	secrets, err := s.ResolveAdapterSecrets(ctx, connection)
	if err != nil {
		return integrationmodel.IntegrationGoogleOAuthStartResult{}, err
	}
	clientID, clientSecret := strings.TrimSpace(secrets["client_id"]), strings.TrimSpace(secrets["client_secret"])
	if clientID == "" || clientSecret == "" {
		return integrationmodel.IntegrationGoogleOAuthStartResult{}, badRequest("backend.integration.google_oauth.client_credentials_required")
	}
	eventActorID, eventRoleKey := strings.TrimSpace(req.EventActorID), strings.TrimSpace(req.EventRoleKey)
	if (eventActorID == "") != (eventRoleKey == "") {
		return integrationmodel.IntegrationGoogleOAuthStartResult{}, badRequest("backend.integration.google_oauth.event_identity_incomplete")
	}
	nonceBytes := make([]byte, 32)
	if _, err := googleOAuthRandRead(nonceBytes); err != nil {
		return integrationmodel.IntegrationGoogleOAuthStartResult{}, fmt.Errorf("backend.integration.google_oauth.state_unavailable: %w", err)
	}
	expiresAt := time.Now().UTC().Add(10 * time.Minute)
	state := googleOAuthState{WorkspaceID: connection.WorkspaceID, ConnectionKey: connection.Key, ActorID: strings.TrimSpace(principal.UserID), EventActorID: eventActorID, EventRoleKey: eventRoleKey, RedirectURI: redirectURI, Nonce: base64.RawURLEncoding.EncodeToString(nonceBytes), ExpiresAt: expiresAt.Unix()}
	encodedState, err := encodeGoogleOAuthState(state, clientSecret)
	if err != nil {
		return integrationmodel.IntegrationGoogleOAuthStartResult{}, err
	}
	query := url.Values{
		"client_id": {clientID}, "redirect_uri": {redirectURI}, "response_type": {"code"},
		"scope": {strings.Join(googleOAuthScopes, " ")}, "state": {encodedState},
		"access_type": {"offline"}, "include_granted_scopes": {"true"}, "prompt": {"consent"},
	}
	return integrationmodel.IntegrationGoogleOAuthStartResult{AuthorizationURL: googleOAuthAuthorizationURL + "?" + query.Encode(), ExpiresAt: expiresAt.Format(time.RFC3339)}, nil
}

// CompleteGoogleOAuthAuthorization verifies and consumes the state, exchanges
// the code, writes tokens as encrypted Runtime secret material, activates Gmail
// ingestion and thereby lets the existing polling/Watch workers self-provision.
func (s *IntegrationApplicationService) CompleteGoogleOAuthAuthorization(ctx context.Context, code, encodedState string) (integrationmodel.IntegrationGoogleOAuthCallbackResult, error) {
	unsigned, err := decodeUnsignedGoogleOAuthState(encodedState)
	if err != nil {
		return integrationmodel.IntegrationGoogleOAuthCallbackResult{}, err
	}
	connection, found, err := s.findConnection(ctx, unsigned.ConnectionKey, unsigned.WorkspaceID)
	if err != nil {
		return integrationmodel.IntegrationGoogleOAuthCallbackResult{}, err
	}
	if !found || !validGoogleOAuthConnection(connection) {
		return integrationmodel.IntegrationGoogleOAuthCallbackResult{}, badRequest("backend.integration.google_oauth.connection_invalid")
	}
	secrets, err := s.ResolveAdapterSecrets(ctx, connection)
	if err != nil {
		return integrationmodel.IntegrationGoogleOAuthCallbackResult{}, err
	}
	clientID, clientSecret := strings.TrimSpace(secrets["client_id"]), strings.TrimSpace(secrets["client_secret"])
	state, err := decodeGoogleOAuthState(encodedState, clientSecret)
	if err != nil || time.Now().UTC().Unix() > state.ExpiresAt {
		return integrationmodel.IntegrationGoogleOAuthCallbackResult{}, forbidden("backend.integration.google_oauth.state_invalid")
	}
	if strings.TrimSpace(code) == "" || s.eventRepo == nil {
		return integrationmodel.IntegrationGoogleOAuthCallbackResult{}, badRequest("backend.integration.google_oauth.callback_invalid")
	}
	now := time.Now().UTC()
	duplicate, err := s.eventRepo.RecordWebhookNonce(ctx, state.WorkspaceID, "google_oauth:"+state.ConnectionKey, state.Nonce, now.Format(time.RFC3339Nano), time.Unix(state.ExpiresAt, 0).UTC().Format(time.RFC3339))
	if err != nil {
		return integrationmodel.IntegrationGoogleOAuthCallbackResult{}, err
	}
	if duplicate {
		return integrationmodel.IntegrationGoogleOAuthCallbackResult{}, forbidden("backend.integration.google_oauth.state_replayed")
	}
	tokenURL := googleConnectionConfig(connection.Config, "token_url", "https://oauth2.googleapis.com/token")
	tokens, err := exchangeGoogleOAuthCode(ctx, tokenURL, strings.TrimSpace(code), clientID, clientSecret, state.RedirectURI)
	if err != nil {
		return integrationmodel.IntegrationGoogleOAuthCallbackResult{}, err
	}
	principal := s.resolvePrincipal(ctx, state.ActorID, "", "")
	if !principal.Known || principal.AccessBundle == nil || principal.WorkspaceID != state.WorkspaceID ||
		!principal.HasPermission(PermissionConnectionManage) || !principal.HasPermission(PermissionSecretManage) {
		return integrationmodel.IntegrationGoogleOAuthCallbackResult{}, forbidden("auth.permission_denied")
	}
	if err := s.persistGoogleOAuthTokens(ctx, &connection, tokens, state.EventActorID, state.EventRoleKey, principal); err != nil {
		return integrationmodel.IntegrationGoogleOAuthCallbackResult{}, err
	}
	return integrationmodel.IntegrationGoogleOAuthCallbackResult{ConnectionKey: connection.Key, WorkspaceID: connection.WorkspaceID, Status: connection.Status}, nil
}

func (s *IntegrationApplicationService) persistGoogleOAuthTokens(ctx context.Context, connection *integrationmodel.IntegrationConnection, tokens googleOAuthTokenResponse, eventActorID, eventRoleKey string, principal principalmodel.Principal) error {
	accessKey, refreshKey := connection.Key+"_access_token", connection.Key+"_refresh_token"
	eventActorID, eventRoleKey = strings.TrimSpace(eventActorID), strings.TrimSpace(eventRoleKey)
	if (eventActorID == "") != (eventRoleKey == "") {
		return badRequest("backend.integration.google_oauth.event_identity_incomplete")
	}
	refreshToken := strings.TrimSpace(tokens.RefreshToken)
	var existingRefresh integrationmodel.IntegrationSecret
	if refreshToken == "" {
		var ok bool
		var err error
		existingRefresh, ok, err = s.findSecret(ctx, refreshKey, connection.WorkspaceID)
		if err != nil {
			return err
		}
		if !ok {
			return badRequest("backend.integration.google_oauth.refresh_token_required")
		}
	}
	if _, err := s.UpsertIntegrationSecret(ctx, accessKey, integrationmodel.IntegrationSecretUpsertRequest{Kind: "bearer_token", Status: "active", Description: "Google OAuth access token for " + connection.Key, Value: tokens.AccessToken}, principal); err != nil {
		return err
	}
	if connection.SecretRefs == nil {
		connection.SecretRefs = map[string]string{}
	}
	if refreshToken == "" {
		connection.SecretRefs["refresh_token"] = "secret:" + existingRefresh.Key
	} else if _, err := s.UpsertIntegrationSecret(ctx, refreshKey, integrationmodel.IntegrationSecretUpsertRequest{Kind: "refresh_token", Status: "active", Description: "Google OAuth refresh token for " + connection.Key, Value: refreshToken}, principal); err != nil {
		return err
	}
	connection.SecretRefs["access_token"] = "secret:" + accessKey
	if refreshToken != "" {
		connection.SecretRefs["refresh_token"] = "secret:" + refreshKey
	}
	if connection.Config == nil {
		connection.Config = map[string]any{}
	}
	connection.Config["gmail_ingest_enabled"] = true
	// OAuth authorization makes durable polling usable immediately. Push Watch is
	// optional and must not be enabled merely because mailbox access was granted.
	connection.Config["gmail_watch_enabled"] = false
	connection.Status = "active"
	saved, err := s.upsertConnectionAndSyncBackground(ctx, *connection)
	if err != nil {
		return err
	}
	*connection = saved
	linkedConnectionKeys, err := s.linkGoogleOAuthConnections(ctx, saved, eventActorID, eventRoleKey, principal)
	if err != nil {
		return err
	}
	if eventActorID != "" {
		_, err = s.configRepo.UpsertExternalIdentity(ctx, saved.WorkspaceID, integrationmodel.IntegrationExternalIdentity{
			Key: "google_" + saved.Key, WorkspaceID: saved.WorkspaceID, Provider: "google",
			ExternalSubject: saved.Key, ExternalSubjectType: "service_account", ExternalName: saved.Name,
			ActorID: eventActorID, RoleKey: eventRoleKey, Status: "active", CreatedBy: principal.UserID,
		})
		if err != nil {
			return err
		}
	}
	s.audit(ctx, "integration_google_oauth_completed", "integration_connection", saved.Key, principal, "Authorized Google Workspace connection "+saved.Key, nil, connectionAuditShape(saved), map[string]any{"workspace_id": saved.WorkspaceID, "connection_key": saved.Key, "gmail_ingest_enabled": true, "gmail_watch_enabled": gmailConfigBool(saved.Config, "gmail_watch_enabled"), "linked_connection_keys": linkedConnectionKeys})
	return nil
}

// linkGoogleOAuthConnections explicitly projects one Google Workspace consent
// onto tenant-declared Google Calendar connections. The source connection owns
// the encrypted OAuth material; linked providers receive only secret references.
func (s *IntegrationApplicationService) linkGoogleOAuthConnections(ctx context.Context, source integrationmodel.IntegrationConnection, eventActorID, eventRoleKey string, principal principalmodel.Principal) ([]string, error) {
	keys := googleOAuthLinkedConnectionKeys(source.Config)
	linked := make([]string, 0, len(keys))
	for _, key := range keys {
		target, found, err := s.findConnection(ctx, key, source.WorkspaceID)
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, badRequest("backend.integration.google_oauth.linked_connection_not_found", "connection_key", key)
		}
		if target.ConnectorKey != "appointment_scheduling" || target.ProviderKey != "google_calendar" || target.Status == "disabled" {
			return nil, badRequest("backend.integration.google_oauth.linked_connection_invalid", "connection_key", key)
		}
		if target.SecretRefs == nil {
			target.SecretRefs = map[string]string{}
		}
		for targetName, sourceName := range map[string]string{
			"access_token":        "access_token",
			"refresh_token":       "refresh_token",
			"oauth_client_id":     "client_id",
			"oauth_client_secret": "client_secret",
		} {
			if ref := strings.TrimSpace(source.SecretRefs[sourceName]); ref != "" {
				target.SecretRefs[targetName] = ref
			}
		}
		if target.Config == nil {
			target.Config = map[string]any{}
		}
		if strings.TrimSpace(fmt.Sprint(target.Config["calendar_id"])) == "" || fmt.Sprint(target.Config["calendar_id"]) == "<nil>" {
			target.Config["calendar_id"] = "primary"
		}
		target.Config["meeting_enabled"] = true
		target.Config["oauth_source_connection_key"] = source.Key
		target.Status = "active"
		savedTarget, err := s.upsertConnectionAndSyncBackground(ctx, target)
		if err != nil {
			return nil, err
		}
		if eventActorID != "" {
			_, err = s.configRepo.UpsertExternalIdentity(ctx, savedTarget.WorkspaceID, integrationmodel.IntegrationExternalIdentity{
				Key: "google_calendar_" + savedTarget.Key, WorkspaceID: savedTarget.WorkspaceID, Provider: savedTarget.ProviderKey,
				ExternalSubject: savedTarget.Key, ExternalSubjectType: "service_account", ExternalName: savedTarget.Name,
				ActorID: eventActorID, RoleKey: eventRoleKey, Status: "active", CreatedBy: principal.UserID,
			})
			if err != nil {
				return nil, err
			}
		}
		linked = append(linked, savedTarget.Key)
	}
	return linked, nil
}

func googleOAuthLinkedConnectionKeys(config map[string]any) []string {
	raw, ok := config["oauth_linked_connection_keys"]
	if !ok {
		return nil
	}
	values := []string{}
	switch typed := raw.(type) {
	case []string:
		values = append(values, typed...)
	case []any:
		for _, value := range typed {
			values = append(values, fmt.Sprint(value))
		}
	case string:
		values = strings.Split(typed, ",")
	}
	seen := map[string]struct{}{}
	keys := make([]string, 0, len(values))
	for _, value := range values {
		key := strings.TrimSpace(value)
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		keys = append(keys, key)
	}
	return keys
}
