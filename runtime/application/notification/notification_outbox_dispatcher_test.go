package notification

import (
	"context"
	"testing"
	"time"

	sourcedelivery "github.com/domainry/domainry-notification/delivery"
	sourcetemplate "github.com/domainry/domainry-notification/template"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	integrationrepository "github.com/domainry/domainry-runtime/runtime/domain/integration/repository"
)

type notificationOutboxDispatcherStore struct {
	integrationrepository.IntegrationDeliveryRepository
	message integrationmodel.IntegrationOutboxMessage
}

func (s *notificationOutboxDispatcherStore) InsertOutbox(_ context.Context, _ string, value integrationmodel.IntegrationOutboxMessage) (integrationmodel.IntegrationOutboxMessage, error) {
	s.message = value
	value.ID = "outbox-1"
	return value, nil
}

func TestNotificationOutboxDispatcherPersistsPortableContentAndFallbacks(t *testing.T) {
	outbox := &notificationOutboxDispatcherStore{}
	woken := false
	dispatcher, err := NewNotificationOutboxDispatcher(outbox, "Acme", func(_ integrationmodel.IntegrationOutboxMessage) { woken = true })
	if err != nil {
		t.Fatal(err)
	}
	_, err = dispatcher.Dispatch(t.Context(), sourcedelivery.DispatchRequest{
		WorkspaceID: "workspace", PlanID: "plan-1", EventID: "event-1", Channel: "collaboration", ConnectorKey: "collaboration",
		ConnectionKey: "slack-primary", Operation: "send_message", DeduplicationKey: "plan-1", CreatedAt: time.Date(2026, 8, 24, 0, 0, 0, 0, time.UTC),
		Content:   sourcetemplate.Rendered{Channel: "collaboration", Provider: "slack", Recipients: []string{"channel-1"}, Title: "Ready", Text: "Body", Message: "Ready\n\nBody", TemplateKey: "workflow.ready", TemplateVersion: 2, TemplateLocale: "en-US", TemplateContentHash: "content", VariablesHash: "variables"},
		Fallbacks: []sourcedelivery.DispatchFallback{{ConnectorKey: "email", Operation: "send_email", Content: sourcetemplate.Rendered{Channel: "email", Recipients: []string{"owner@example.test"}, Subject: "Ready", Text: "Body", TemplateKey: "workflow.ready.email", TemplateVersion: 1, TemplateLocale: "en-US", TemplateContentHash: "fallback", VariablesHash: "variables"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	content, ok := outbox.message.Payload["notification_content"].(map[string]any)
	if !ok || content["schema_version"] != 1 || content["product_name"] != "Acme" || outbox.message.Payload["provider_payload"] != nil {
		t.Fatalf("payload=%+v", outbox.message.Payload)
	}
	if outbox.message.DedupKey != "plan-1" || outbox.message.CreatedBy != "notification" {
		t.Fatalf("message=%+v", outbox.message)
	}
	if !woken {
		t.Fatal("integration outbox worker was not notified")
	}
	fallbacks, ok := outbox.message.Payload["notification_fallback_plan"].([]any)
	if !ok || len(fallbacks) != 1 {
		t.Fatalf("fallbacks=%+v", outbox.message.Payload["notification_fallback_plan"])
	}
}
