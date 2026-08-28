package policy

import (
	"fmt"
	"strings"

	"github.com/domainry/domainry-runtime/runtime/platform/idempotency"
)

// WebhookExternalEventID preserves a provider event ID when present. Providers
// without one use a reviewed canonical projection instead of collapsing all
// missing IDs into the same database uniqueness scope.
func WebhookExternalEventID(provider, connectionKey, eventType, externalID string, payload map[string]any) (string, bool, error) {
	if externalID = strings.TrimSpace(externalID); externalID != "" {
		return externalID, false, nil
	}
	fingerprint, err := idempotency.Fingerprint(idempotency.FingerprintInput{
		UseCase: "integration.webhook.receive", ResourceType: "provider_event", TargetID: strings.TrimSpace(provider) + ":" + strings.TrimSpace(connectionKey),
		Payload: map[string]any{"event_type": strings.TrimSpace(eventType), "payload": payload},
	})
	if err != nil {
		return "", false, err
	}
	return "fallback:" + fingerprint, true, nil
}

func ProviderRetryIsProtected(idempotencySupported bool, sideEffect string, attempt int, config map[string]any) bool {
	if attempt <= 0 || idempotencySupported || strings.TrimSpace(sideEffect) != "write" {
		return true
	}
	strategy := strings.ToLower(strings.TrimSpace(fmt.Sprint(config["non_idempotent_retry_strategy"])))
	switch strategy {
	case "query", "reconcile", "compensate":
		return true
	default:
		return false
	}
}
