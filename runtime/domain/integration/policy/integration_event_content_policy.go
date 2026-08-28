package policy

import (
	"github.com/domainry/domainry-foundation/idempotency"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

// IntegrationEventContentFingerprint identifies the immutable business content
// behind one provider external event ID. Transport security evidence is omitted
// because a legitimate replay can be delivered in a fresh signed envelope.
func IntegrationEventContentFingerprint(event integrationmodel.IntegrationEvent) (string, error) {
	payload := map[string]any{}
	for key, value := range event.Payload {
		if key == "_integration_security" {
			continue
		}
		payload[key] = value
	}
	return idempotency.Fingerprint(idempotency.FingerprintInput{
		UseCase: "integration.event.content", ResourceType: event.Provider, TargetID: event.ExternalID,
		Payload: map[string]any{"event_type": event.EventType, "payload": payload},
	})
}
