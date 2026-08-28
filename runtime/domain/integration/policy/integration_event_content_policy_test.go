package policy

import (
	"testing"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func TestIntegrationEventContentFingerprintIgnoresFreshSecurityEnvelopeButDetectsBusinessConflict(t *testing.T) {
	first := integrationmodel.IntegrationEvent{Provider: "adapter", EventType: "updated", ExternalID: "event-1", Payload: map[string]any{"value": "one", "_integration_security": map[string]any{"nonce": "first"}}}
	second := first
	second.Payload = map[string]any{"_integration_security": map[string]any{"nonce": "second"}, "value": "one"}
	firstFingerprint, err := IntegrationEventContentFingerprint(first)
	if err != nil {
		t.Fatal(err)
	}
	secondFingerprint, err := IntegrationEventContentFingerprint(second)
	if err != nil || secondFingerprint != firstFingerprint {
		t.Fatalf("fresh envelope fingerprint=%q want=%q err=%v", secondFingerprint, firstFingerprint, err)
	}
	second.Payload["value"] = "different"
	changedFingerprint, err := IntegrationEventContentFingerprint(second)
	if err != nil || changedFingerprint == firstFingerprint {
		t.Fatalf("changed fingerprint=%q original=%q err=%v", changedFingerprint, firstFingerprint, err)
	}
}
