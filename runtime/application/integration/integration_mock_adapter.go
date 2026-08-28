package integration

import integrationcontract "github.com/domainry/domainry-runtime/runtime/domain/integration/contract"

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	integrationpolicy "github.com/domainry/domainry-runtime/runtime/domain/integration/policy"
	"github.com/domainry/domainry-runtime/runtime/platform/webhooksignature"
)

type MockAdapter struct{}

func (MockAdapter) VerifyWebhook(ctx context.Context, req integrationcontract.InboundWebhookRequest) (integrationcontract.VerifiedInboundWebhook, error) {
	if err := ctx.Err(); err != nil {
		return integrationcontract.VerifiedInboundWebhook{}, err
	}
	var envelope struct {
		EventType      string         `json:"event_type"`
		ExternalID     string         `json:"external_id"`
		Payload        map[string]any `json:"payload"`
		DeviceIdentity string         `json:"device_identity"`
		EventTime      string         `json:"event_time"`
	}
	if err := json.Unmarshal(req.Body, &envelope); err != nil {
		return integrationcontract.VerifiedInboundWebhook{}, fmt.Errorf("backend.integration.mock.webhook_payload_invalid")
	}
	if strings.TrimSpace(envelope.EventType) == "" {
		return integrationcontract.VerifiedInboundWebhook{}, fmt.Errorf("backend.integration.mock.webhook_identity_missing")
	}
	verified := integrationcontract.VerifiedInboundWebhook{EventType: strings.TrimSpace(envelope.EventType), ExternalID: strings.TrimSpace(envelope.ExternalID), Payload: envelope.Payload}
	if strings.TrimSpace(integrationpolicy.IntegrationConfigString(req.Connection.Config, "inbound_security_profile")) != "device" {
		return verified, nil
	}
	secret := strings.TrimSpace(req.Secrets["webhook_secret"])
	if secret == "" && strings.TrimSpace(req.Connection.SecretRefs["webhook_secret"]) != "" {
		var err error
		secret, err = ResolveSecretRef(req.Connection.SecretRefs["webhook_secret"])
		if err != nil {
			return integrationcontract.VerifiedInboundWebhook{}, err
		}
	}
	if secret == "" {
		return integrationcontract.VerifiedInboundWebhook{}, fmt.Errorf("backend.integration.mock.webhook_secret_missing")
	}
	algorithm := integrationpolicy.IntegrationConfigString(req.Connection.Config, "webhook_signature_algorithm")
	if algorithm == "" {
		algorithm = "hmac_sha256_hex"
	}
	timestamp := strings.TrimSpace(req.Headers["X-Integration-Timestamp"])
	nonce := strings.TrimSpace(req.Headers["X-Integration-Nonce"])
	signature := strings.TrimSpace(req.Headers["X-Integration-Signature"])
	maxSkew := int64(integrationpolicy.IntegrationConfigInt(req.Connection.Config, 300, "inbound_event_max_skew_seconds"))
	if err := webhooksignature.Verify(webhooksignature.Verification{Algorithm: algorithm, Secret: secret, Timestamp: timestamp, Nonce: nonce, Signature: signature, Body: req.Body, MaxSkewSeconds: maxSkew, Now: req.ReceivedAt}); err != nil {
		return integrationcontract.VerifiedInboundWebhook{}, fmt.Errorf("backend.integration.mock.webhook_signature_invalid: %w", err)
	}
	verified.Security = &integrationcontract.WebhookSecurityEvidence{SignatureVerified: true, Nonce: nonce, DeviceIdentity: strings.TrimSpace(envelope.DeviceIdentity), EventTime: strings.TrimSpace(envelope.EventTime)}
	return verified, nil
}

func (MockAdapter) Call(ctx context.Context, req integrationcontract.CallRequest) (integrationcontract.CallResult, error) {
	for _, scenario := range mockMapSlice(mockMap(req.Connection.Config["scenarios"])[req.Operation]) {
		if !mockScenarioMatches(mockMap(scenario["when"]), req.Request) {
			continue
		}
		if delayMS := integrationpolicy.IntegrationConfigInt(map[string]any{"delay_ms": scenario["delay_ms"]}, 0, "delay_ms"); delayMS > 0 {
			select {
			case <-ctx.Done():
				return integrationcontract.CallResult{}, ctx.Err()
			case <-time.After(time.Duration(delayMS) * time.Millisecond):
			}
		}
		if errorCode := integrationpolicy.IntegrationConfigString(map[string]any{"error": scenario["error"]}, "error"); errorCode != "" {
			if errorCode == "timeout" {
				return integrationcontract.CallResult{}, context.DeadlineExceeded
			}
			return integrationcontract.CallResult{}, fmt.Errorf("%s", errorCode)
		}
		response := mockMap(scenario["response"])
		if len(response) == 0 {
			return integrationcontract.CallResult{}, fmt.Errorf("backend.integration.mock.response_missing")
		}
		return integrationcontract.CallResult{Response: cloneMap(response), ResponseRef: "mock:" + req.Operation + ":scenario"}, nil
	}
	responses := mockMap(req.Connection.Config["responses"])
	response := mockMap(responses[req.Operation])
	if len(response) == 0 {
		response = mockMap(req.Connection.Config["response"])
	}
	if len(response) == 0 {
		return integrationcontract.CallResult{}, fmt.Errorf("backend.integration.mock.response_missing")
	}
	return integrationcontract.CallResult{Response: cloneMap(response), ResponseRef: "mock:" + req.Operation}, nil
}

func mockScenarioMatches(expected, request map[string]any) bool {
	if len(expected) == 0 {
		return false
	}
	for key, value := range expected {
		if fmt.Sprint(request[key]) != fmt.Sprint(value) {
			return false
		}
	}
	return true
}

func mockMap(value any) map[string]any {
	if mapped, ok := value.(map[string]any); ok {
		return mapped
	}
	return map[string]any{}
}

func mockMapSlice(value any) []map[string]any {
	switch typed := value.(type) {
	case []map[string]any:
		return append([]map[string]any(nil), typed...)
	case []any:
		out := make([]map[string]any, 0, len(typed))
		for _, item := range typed {
			if mapped := mockMap(item); len(mapped) > 0 {
				out = append(out, mapped)
			}
		}
		return out
	default:
		return nil
	}
}
