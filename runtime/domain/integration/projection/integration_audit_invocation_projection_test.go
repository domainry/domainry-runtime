package projection

import (
	"testing"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func TestIntegrationAuditShapesExposeFactsWithoutPayloads(t *testing.T) {
	event := IntegrationEventAuditShape(integrationmodel.IntegrationEvent{ID: "event-1", WorkspaceID: "workspace", Provider: "stripe", EventType: "paid", ExternalID: "ext", Status: "failed", Payload: map[string]any{"secret": true}, Error: " error ", AttemptCount: 2, NextRetryAt: "later"})
	if event["payload_set"] != true || event["error_set"] != true || event["retry_set"] != true || event["attempt_count"] != 2 {
		t.Fatalf("event audit shape = %#v", event)
	}
	if _, exists := event["payload"]; exists {
		t.Fatalf("event audit leaked payload: %#v", event)
	}
	emptyEvent := IntegrationEventAuditShape(integrationmodel.IntegrationEvent{})
	if emptyEvent["payload_set"] != false || emptyEvent["error_set"] != false || emptyEvent["retry_set"] != false {
		t.Fatalf("empty event audit shape = %#v", emptyEvent)
	}

	invocation := IntegrationInvocationAuditShape(integrationmodel.IntegrationInvocation{ID: "inv-1", WorkspaceID: "workspace", ConnectorKey: "payments", ProviderKey: "stripe", ConnectionKey: "primary", Operation: "charge", Status: "failed", DurationMS: 12, RequestRef: "request", ResponseRef: "500", Error: " failed ", EventID: "event", ObjectKey: "order", RecordID: "1", Metadata: map[string]any{"retryable": true, "retry_reason": " timeout ", "provider_status": 503}})
	if invocation["retryable"] != true || invocation["retry_reason"] != "timeout" || invocation["provider_status"] != "503" || invocation["error_set"] != true {
		t.Fatalf("invocation audit shape = %#v", invocation)
	}
	emptyInvocation := IntegrationInvocationAuditShape(integrationmodel.IntegrationInvocation{})
	if emptyInvocation["retryable"] != false || emptyInvocation["retry_reason"] != "<nil>" || emptyInvocation["provider_status"] != "<nil>" || emptyInvocation["error_set"] != false {
		t.Fatalf("empty invocation audit shape = %#v", emptyInvocation)
	}

	outbox := IntegrationOutboxAuditShape(integrationmodel.IntegrationOutboxMessage{ID: "out-1", WorkspaceID: "workspace", ConnectorKey: "email", ConnectionKey: "primary", Operation: "send", Status: "failed", Payload: map[string]any{"secret": true}, EventID: "event", RequestRef: "request", ResponseRef: "500", Error: " failed ", AttemptCount: 3, NextAttemptAt: "later"})
	if outbox["payload_set"] != true || outbox["error_set"] != true || outbox["next_attempt_at_set"] != true || outbox["attempt_count"] != 3 {
		t.Fatalf("outbox audit shape = %#v", outbox)
	}
	emptyOutbox := IntegrationOutboxAuditShape(integrationmodel.IntegrationOutboxMessage{})
	if emptyOutbox["payload_set"] != false || emptyOutbox["error_set"] != false || emptyOutbox["next_attempt_at_set"] != false {
		t.Fatalf("empty outbox audit shape = %#v", emptyOutbox)
	}
}

func TestIntegrationInvocationMetadataMatchesProviderAndExternalPrincipal(t *testing.T) {
	tests := []struct {
		name      string
		inv       integrationmodel.IntegrationInvocation
		provider  string
		principal string
		want      bool
	}{
		{name: "no filters", inv: integrationmodel.IntegrationInvocation{}, want: true},
		{name: "provider field", inv: integrationmodel.IntegrationInvocation{ProviderKey: " stripe ", Metadata: map[string]any{"external_principal": " user "}}, provider: "stripe", principal: "user", want: true},
		{name: "provider metadata fallback", inv: integrationmodel.IntegrationInvocation{Metadata: map[string]any{"provider": " stripe ", "external_principal": 42}}, provider: "stripe", principal: "42", want: true},
		{name: "provider mismatch", inv: integrationmodel.IntegrationInvocation{ProviderKey: "stripe"}, provider: "adyen", want: false},
		{name: "principal mismatch", inv: integrationmodel.IntegrationInvocation{ProviderKey: "stripe", Metadata: map[string]any{"external_principal": "user"}}, provider: "stripe", principal: "other", want: false},
		{name: "missing metadata", inv: integrationmodel.IntegrationInvocation{}, provider: "stripe", want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := IntegrationInvocationMetadataMatches(test.inv, test.provider, test.principal); got != test.want {
				t.Fatalf("match = %v, want %v", got, test.want)
			}
		})
	}
}
