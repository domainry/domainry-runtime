package integrations_test

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-connector-sdk"
	integrationapplication "github.com/domainry/domainry-runtime/runtime/application/integration"
	runtimebootstrap "github.com/domainry/domainry-runtime/runtime/bootstrap"
	runtimetestkit "github.com/domainry/domainry-runtime/runtime/bootstrap/testkit"
	connectortest "github.com/domainry/domainry-runtime/runtime/bootstrap/testkit/connectors"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-runtime/runtime/platform/webhooksignature"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
	integrationhttp "github.com/domainry/domainry-runtime/runtime/transport/http/integrations"
)

func TestReceiveIntegrationWebhookHTTPVerifiesAndPersistsEvent(t *testing.T) {
	t.Setenv("SLACK_HTTP_SIGNING_SECRET", "signing-secret")
	store, handler := newWebhookHTTPTestRuntime(t)
	defer store.Close()
	body := `{"type":"event_callback","event_id":"Ev-http","event":{"type":"message","user":"U1"}}`
	timestamp := fmt.Sprint(time.Now().UTC().Unix())
	request := httptest.NewRequest(http.MethodPost, "/integrations/webhooks/default/slack-primary", strings.NewReader(body))
	request.Header.Set("X-Slack-Request-Timestamp", timestamp)
	request.Header.Set("X-Slack-Signature", httpSlackSignature("signing-secret", timestamp, []byte(body)))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	duplicateRequest := httptest.NewRequest(http.MethodPost, "/integrations/webhooks/default/slack-primary", strings.NewReader(body))
	duplicateRequest.Header.Set("X-Slack-Request-Timestamp", timestamp)
	duplicateRequest.Header.Set("X-Slack-Signature", httpSlackSignature("signing-secret", timestamp, []byte(body)))
	duplicateResponse := httptest.NewRecorder()
	handler.ServeHTTP(duplicateResponse, duplicateRequest)
	if duplicateResponse.Code != http.StatusOK {
		t.Fatalf("duplicate status=%d body=%s", duplicateResponse.Code, duplicateResponse.Body.String())
	}
	events, err := integrationEventRepository(store).ListEvents(t.Context(), "default", "slack", "received", 10)
	if err != nil || len(events) != 1 || events[0].ExternalID != "Ev-http" {
		t.Fatalf("events=%#v err=%v", events, err)
	}
}

func TestReceiveIntegrationWebhookHTTPRejectsSignatureAndOversizedBody(t *testing.T) {
	t.Setenv("SLACK_HTTP_SIGNING_SECRET", "signing-secret")
	store, handler := newWebhookHTTPTestRuntime(t)
	defer store.Close()
	timestamp := fmt.Sprint(time.Now().UTC().Unix())
	bad := httptest.NewRequest(http.MethodPost, "/integrations/webhooks/default/slack-primary", strings.NewReader(`{"type":"event_callback"}`))
	bad.Header.Set("X-Slack-Request-Timestamp", timestamp)
	bad.Header.Set("X-Slack-Signature", "v0=bad")
	badResponse := httptest.NewRecorder()
	handler.ServeHTTP(badResponse, bad)
	if badResponse.Code != http.StatusForbidden {
		t.Fatalf("bad signature status=%d body=%s", badResponse.Code, badResponse.Body.String())
	}
	large := httptest.NewRequest(http.MethodPost, "/integrations/webhooks/default/slack-primary", strings.NewReader(strings.Repeat("x", integrationhttp.WebhookBodyLimit+1)))
	largeResponse := httptest.NewRecorder()
	handler.ServeHTTP(largeResponse, large)
	if largeResponse.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("large body status=%d body=%s", largeResponse.Code, largeResponse.Body.String())
	}
}

func TestReceiveIntegrationWebhookHTTPReturnsGraphValidationTokenAsPlainText(t *testing.T) {
	store, handler := newWebhookHTTPTestRuntime(t)
	defer store.Close()
	request := httptest.NewRequest(http.MethodPost, "/integrations/webhooks/default/teams-primary?validationToken=graph%20token", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Body.String() != "graph token" || !strings.HasPrefix(response.Header().Get("Content-Type"), "text/plain") {
		t.Fatalf("status=%d content-type=%q body=%q", response.Code, response.Header().Get("Content-Type"), response.Body.String())
	}
}

func TestReceiveIntegrationWebhookHTTPReturnsSlackChallengeAsJSON(t *testing.T) {
	t.Setenv("SLACK_HTTP_SIGNING_SECRET", "signing-secret")
	store, handler := newWebhookHTTPTestRuntime(t)
	defer store.Close()
	body := `{"type":"url_verification","challenge":"slack-token"}`
	timestamp := fmt.Sprint(time.Now().UTC().Unix())
	request := httptest.NewRequest(http.MethodPost, "/integrations/webhooks/default/slack-primary", strings.NewReader(body))
	request.Header.Set("X-Slack-Request-Timestamp", timestamp)
	request.Header.Set("X-Slack-Signature", httpSlackSignature("signing-secret", timestamp, []byte(body)))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"challenge":"slack-token"`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestReceiveDeviceWebhookHTTPPersistsOneEventAcrossOneHundredIdenticalDeliveries(t *testing.T) {
	t.Setenv("MOCK_DEVICE_WEBHOOK_SECRET", "device-secret")
	store, handler := newWebhookHTTPTestRuntime(t)
	defer store.Close()
	now := time.Now().UTC()
	bodyBytes, err := json.Marshal(map[string]any{
		"event_type": "credential.presented", "external_id": "device-event-1", "device_identity": "device-1",
		"event_time": now.Format(time.RFC3339Nano), "payload": map[string]any{"credential_ref": "opaque-card-ref"},
	})
	if err != nil {
		t.Fatal(err)
	}
	timestamp, nonce := fmt.Sprint(now.Unix()), "device-nonce-1"
	signature := webhooksignature.Compute("hmac_sha256_hex", "device-secret", timestamp, nonce, bodyBytes)
	for delivery := 0; delivery < 100; delivery++ {
		request := httptest.NewRequest(http.MethodPost, "/integrations/webhooks/default/device-primary", strings.NewReader(string(bodyBytes)))
		request.Header.Set("X-Integration-Timestamp", timestamp)
		request.Header.Set("X-Integration-Nonce", nonce)
		request.Header.Set("X-Integration-Signature", signature)
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if delivery == 0 && response.Code != http.StatusCreated {
			t.Fatalf("first delivery status=%d body=%s", response.Code, response.Body.String())
		}
		if delivery > 0 && (response.Code != http.StatusForbidden || !strings.Contains(response.Body.String(), "backend.integration.webhook.replay_detected")) {
			t.Fatalf("replay %d status=%d body=%s", delivery, response.Code, response.Body.String())
		}
	}
	events, err := integrationEventRepository(store).ListEvents(t.Context(), "default", "sandbox", "received", 10)
	if err != nil || len(events) != 1 || events[0].ExternalID != "device-event-1" {
		t.Fatalf("events=%#v err=%v", events, err)
	}
	security, _ := events[0].Payload["_integration_security"].(map[string]any)
	if security["profile"] != "device" || security["device_identity"] != "device-1" || security["signature_verified"] != true {
		t.Fatalf("persisted security=%#v payload=%#v", security, events[0].Payload)
	}
}

func newWebhookHTTPTestRuntime(t *testing.T) (*persistence.RuntimeStore, http.Handler) {
	t.Helper()
	store, err := persistence.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "webhook-http.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if _, err := integrationConfigRepository(store).UpsertConnection(t.Context(), "default", integrationmodel.IntegrationConnection{Key: "slack-primary", WorkspaceID: "default", ConnectorKey: "collaboration", ProviderKey: "slack", Status: "active", SecretRefs: map[string]string{"signing_secret": "env:SLACK_HTTP_SIGNING_SECRET"}}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if _, err := integrationConfigRepository(store).UpsertConnection(t.Context(), "default", integrationmodel.IntegrationConnection{Key: "teams-primary", WorkspaceID: "default", ConnectorKey: "collaboration", ProviderKey: "teams", Status: "active"}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if _, err := integrationConfigRepository(store).UpsertConnection(t.Context(), "default", integrationmodel.IntegrationConnection{
		Key: "device-primary", WorkspaceID: "default", ConnectorKey: "mock", ProviderKey: "sandbox", Status: "active",
		SecretRefs: map[string]string{"webhook_secret": "env:MOCK_DEVICE_WEBHOOK_SECRET"},
		Config:     map[string]any{"inbound_security_profile": "device", "inbound_event_max_skew_seconds": 300, "webhook_signature_algorithm": "hmac_sha256_hex"},
	}); err != nil {
		store.Close()
		t.Fatal(err)
	}
	slackProvider := integrationmodel.ConnectorProviderSchema{Key: "slack", SecretFields: []definitionmodel.FieldSchema{{Key: "signing_secret", Name: "Signing secret", Type: "text", Required: true}}}
	builtins := connector.ProviderSet{Providers: []connector.Adapter{
		connectortest.Provider("collaboration", "slack", webhookHTTPFixtureAdapter{provider: "slack"}, nil, slackProvider),
		connectortest.Provider("collaboration", "teams", webhookHTTPFixtureAdapter{provider: "teams"}, nil),
	}}
	mockSchema := integrationmodel.ConnectorSchema{Key: "mock", Type: "mock", Provider: "sandbox", Providers: []integrationmodel.ConnectorProviderSchema{{
		Key: "sandbox", SecretFields: []definitionmodel.FieldSchema{{
			Key: "webhook_secret", Name: "Webhook secret", Type: "text", Required: true,
			Config: map[string]any{"credential_kind": "signing_secret"},
		}},
	}}}
	providers := connector.NewRegistry()
	builtins.Providers = append(builtins.Providers, connectortest.Provider("mock", "sandbox", integrationapplication.MockAdapter{}, mockSchema.Operations, mockSchema.Providers[0]))
	if err := providers.RegisterProviderSet(builtins); err != nil {
		store.Close()
		t.Fatal(err)
	}
	providers.Freeze()
	records := runtimetestkit.NewRuntimeServices(t.Context(), runtimetestkit.RuntimeServicesConfig{TemplateID: "webhook", TemplateVersion: "1", Name: "Webhook", Objects: nil, Views: nil, Actions: nil, Workflows: nil, AutomationRules: nil, Dictionaries: nil, Integrations: integrationmodel.IntegrationSchema{Connectors: []integrationmodel.ConnectorSchema{{Key: "collaboration", Providers: []integrationmodel.ConnectorProviderSchema{{Key: "slack"}, {Key: "teams"}}}, mockSchema}}, Reports: nil, Entrypoints: nil, Skills: nil, Agents: nil, Store: store, ConnectorProviders: providers})
	return store, runtimebootstrap.AssembleHTTPServer(t.Context(), records, runtimetestkit.IdentityBindingStub{}, t.TempDir(), nil, true, runtimehttp.AgentHTTPConfig{}).Routes()
}

type webhookHTTPFixtureAdapter struct {
	integrationapplication.MockAdapter
	provider string
}

func (adapter webhookHTTPFixtureAdapter) VerifyWebhook(_ context.Context, request integrationcontract.InboundWebhookRequest) (integrationcontract.VerifiedInboundWebhook, error) {
	if adapter.provider == "teams" {
		if challenge := strings.TrimSpace(request.Query["validationToken"]); challenge != "" {
			return integrationcontract.VerifiedInboundWebhook{Challenge: challenge, ChallengeFormat: "text/plain"}, nil
		}
		return integrationcontract.VerifiedInboundWebhook{}, fmt.Errorf("teams fixture validation token is missing")
	}
	timestamp := strings.TrimSpace(request.Headers["X-Slack-Request-Timestamp"])
	signature := strings.TrimSpace(request.Headers["X-Slack-Signature"])
	expected := httpSlackSignature(request.Secrets["signing_secret"], timestamp, request.Body)
	if timestamp == "" || !hmac.Equal([]byte(signature), []byte(expected)) {
		return integrationcontract.VerifiedInboundWebhook{}, fmt.Errorf("slack fixture signature is invalid")
	}
	var envelope struct {
		Type      string         `json:"type"`
		EventID   string         `json:"event_id"`
		Challenge string         `json:"challenge"`
		Event     map[string]any `json:"event"`
	}
	if err := json.Unmarshal(request.Body, &envelope); err != nil {
		return integrationcontract.VerifiedInboundWebhook{}, err
	}
	if envelope.Type == "url_verification" {
		return integrationcontract.VerifiedInboundWebhook{Challenge: envelope.Challenge, ChallengeFormat: "json"}, nil
	}
	return integrationcontract.VerifiedInboundWebhook{EventType: envelope.Type, ExternalID: envelope.EventID, Payload: envelope.Event}, nil
}

func httpSlackSignature(secret, timestamp string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte("v0:" + timestamp + ":"))
	_, _ = mac.Write(body)
	return "v0=" + hex.EncodeToString(mac.Sum(nil))
}
