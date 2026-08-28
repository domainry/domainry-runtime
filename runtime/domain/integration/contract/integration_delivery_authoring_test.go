package integrationcontract

import (
	"testing"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func TestIntegrationDeliveryAuthoringUsesOneCapabilityPerHTTPInterface(t *testing.T) {
	wantRoutes := map[string]string{
		"integration.invocation.list":       "GET /integrations/invocations",
		"integration.event.list":            "GET /integrations/events",
		"integration.event.recover_offline": "POST /integrations/events/recover-offline",
		"integration.outbox.list":           "GET /integrations/outbox",
		"integration.outbox.enqueue":        "POST /integrations/outbox",
		"integration.outbox.status":         "POST /integrations/outbox/{messageID}/status",
		"integration.outbox.retry":          "POST /integrations/outbox/{messageID}/retry",
	}
	definitions := IntegrationDeliveryAuthoringCapabilities()
	if len(definitions) != len(wantRoutes) {
		t.Fatalf("delivery capabilities=%d want=%d", len(definitions), len(wantRoutes))
	}
	seenRoutes := map[string]string{}
	for _, definition := range definitions {
		want, exists := wantRoutes[definition.Key]
		if !exists {
			t.Fatalf("unexpected capability %s", definition.Key)
		}
		if len(definition.ConfigurationRoutes) != 1 || definition.ConfigurationRoutes[0] != want {
			t.Fatalf("capability %s routes=%v want=%s", definition.Key, definition.ConfigurationRoutes, want)
		}
		if previous := seenRoutes[want]; previous != "" {
			t.Fatalf("route %s owned by both %s and %s", want, previous, definition.Key)
		}
		seenRoutes[want] = definition.Key
		if definition.InputSchema == nil || definition.OutputSchema == nil || definition.InputSchema.AdditionalProperties == nil || *definition.InputSchema.AdditionalProperties {
			t.Fatalf("capability %s does not publish a closed request and response contract", definition.Key)
		}
	}
}

func TestIntegrationOwnerOutboxStatusEnumMatchesRuntimeNormalizerValues(t *testing.T) {
	want := map[string]bool{"delivered": true, "read": true}
	for _, status := range integrationmodel.RuntimeIntegrationOutboxStatuses() {
		delete(want, status)
	}
	if len(want) != 0 {
		t.Fatalf("owner outbox statuses omit runtime values: %v", want)
	}
}
