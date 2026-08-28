package integration

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	"github.com/domainry/domainry-runtime/runtime/platform/webhooksignature"
)

func TestMockAdapterVerifyWebhookFailureMatrix(t *testing.T) {
	adapter := MockAdapter{}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := adapter.VerifyWebhook(cancelled, integrationcontract.InboundWebhookRequest{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled verification error=%v", err)
	}
	if _, err := adapter.VerifyWebhook(t.Context(), integrationcontract.InboundWebhookRequest{Body: []byte("{")}); err == nil || err.Error() != "backend.integration.mock.webhook_payload_invalid" {
		t.Fatalf("invalid payload error=%v", err)
	}
	if _, err := adapter.VerifyWebhook(t.Context(), integrationcontract.InboundWebhookRequest{Body: []byte(`{"payload":{"ok":true}}`)}); err == nil || err.Error() != "backend.integration.mock.webhook_identity_missing" {
		t.Fatalf("missing identity error=%v", err)
	}
	body := []byte(`{"event_type":" device.updated ","external_id":" event-1 ","payload":{"ok":true},"device_identity":" device-1 ","event_time":" 2026-07-27T00:00:00Z "}`)
	plain, err := adapter.VerifyWebhook(t.Context(), integrationcontract.InboundWebhookRequest{Body: body})
	if err != nil || plain.EventType != "device.updated" || plain.ExternalID != "event-1" || plain.Security != nil {
		t.Fatalf("plain webhook=%#v error=%v", plain, err)
	}
	deviceConnection := integrationmodel.IntegrationConnection{Config: map[string]any{"inbound_security_profile": "device"}}
	if _, err := adapter.VerifyWebhook(t.Context(), integrationcontract.InboundWebhookRequest{Body: body, Connection: deviceConnection}); err == nil || err.Error() != "backend.integration.mock.webhook_secret_missing" {
		t.Fatalf("missing secret error=%v", err)
	}
	deviceConnection.SecretRefs = map[string]string{"webhook_secret": "secret:unsupported"}
	if _, err := adapter.VerifyWebhook(t.Context(), integrationcontract.InboundWebhookRequest{Body: body, Connection: deviceConnection}); testErrorCode(err) != "backend.integration.secret_ref_must_be_env" {
		t.Fatalf("invalid secret ref error=%v", err)
	}
}

func TestMockAdapterVerifyWebhookSignatureMatrix(t *testing.T) {
	adapter := MockAdapter{}
	now := time.Date(2026, 7, 27, 0, 0, 0, 0, time.UTC)
	body := []byte(`{"event_type":"device.updated","external_id":"event-1","payload":{"ok":true},"device_identity":"device-1","event_time":"2026-07-27T00:00:00Z"}`)
	timestamp, nonce := fmt.Sprint(now.Unix()), "nonce-1"
	connection := integrationmodel.IntegrationConnection{
		Config: map[string]any{
			"inbound_security_profile":       "device",
			"inbound_event_max_skew_seconds": 300,
		},
	}
	request := integrationcontract.InboundWebhookRequest{
		Body: body, Connection: connection, ReceivedAt: now,
		Headers: map[string]string{
			"X-Integration-Timestamp": timestamp,
			"X-Integration-Nonce":     nonce,
			"X-Integration-Signature": "invalid",
		},
		Secrets: map[string]string{"webhook_secret": "device-secret"},
	}
	if _, err := adapter.VerifyWebhook(t.Context(), request); err == nil || !errors.Is(err, webhooksignature.ErrorInvalidSignature) {
		t.Fatalf("invalid signature error=%v", err)
	}

	t.Setenv("MOCK_ADAPTER_WEBHOOK_SECRET", "device-secret")
	request.Secrets = nil
	request.Connection.SecretRefs = map[string]string{"webhook_secret": "env:MOCK_ADAPTER_WEBHOOK_SECRET"}
	request.Connection.Config["webhook_signature_algorithm"] = "hmac_sha256_hex"
	request.Headers["X-Integration-Signature"] = webhooksignature.Compute("hmac_sha256_hex", "device-secret", timestamp, nonce, body)
	verified, err := adapter.VerifyWebhook(t.Context(), request)
	if err != nil {
		t.Fatalf("valid signature error=%v", err)
	}
	if verified.Security == nil || !verified.Security.SignatureVerified || verified.Security.Nonce != nonce ||
		verified.Security.DeviceIdentity != "device-1" || verified.Security.EventTime != "2026-07-27T00:00:00Z" {
		t.Fatalf("verified webhook=%#v", verified)
	}
}
