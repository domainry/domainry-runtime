package runtime

import "testing"

func TestRecordExportCompletionEventPublishesDownloadAction(t *testing.T) {
	types, err := NotificationRuntimeEventTypes(nil, []string{"en-US"}, "en-US", func(_, key string) (string, bool) { return key, true })
	if err != nil {
		t.Fatal(err)
	}
	for _, eventType := range types {
		if eventType.Key != "record.export.completed" {
			continue
		}
		if eventType.Source != "records" || eventType.Category != "long_task" || !eventType.MandatoryInApp || len(eventType.Actions) != 1 || eventType.Actions[0].Key != "record.export.download" || eventType.Actions[0].ResourceType != "record_export" || eventType.Actions[0].RouteKey != "record.export.download" {
			t.Fatalf("event type=%+v", eventType)
		}
		return
	}
	t.Fatal("record export completion event type missing")
}
