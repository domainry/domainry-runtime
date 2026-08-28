package validation

import (
	"testing"

	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
)

func TestNotificationEventTypeRemainingInvalidFields(t *testing.T) {
	for name, mutate := range map[string]func(*notificationmodel.NotificationEventType){
		"source":         func(value *notificationmodel.NotificationEventType) { value.Source = "bad source" },
		"category":       func(value *notificationmodel.NotificationEventType) { value.Category = "bad category" },
		"template":       func(value *notificationmodel.NotificationEventType) { value.TemplateKey = "bad template" },
		"default locale": func(value *notificationmodel.NotificationEventType) { value.DefaultLocale = "" },
		"locales":        func(value *notificationmodel.NotificationEventType) { value.Locales = nil },
		"version":        func(value *notificationmodel.NotificationEventType) { value.Version = 0 },
	} {
		t.Run(name, func(t *testing.T) {
			value := validEventTypeBoundary()
			mutate(&value)
			if _, err := NotificationValidateEventType(value); err == nil {
				t.Fatal("invalid event type accepted")
			}
		})
	}

	base := validEventTypeBoundary()
	content := base.Locales["en-US"]
	content.ActionLabels = map[string]string{"test.open": "Open"}
	base.Locales = map[string]notificationmodel.NotificationInboxEventTypeContent{"en-US": content}
	for name, descriptor := range map[string]notificationmodel.NotificationInboxActionDescriptor{
		"kind":          {Key: "test.open", Kind: "other", ResourceType: "test", SurfaceRoutes: map[string]string{"business_workspace": "test.open"}},
		"resource type": {Key: "test.open", Kind: "route", ResourceType: "bad type", SurfaceRoutes: map[string]string{"business_workspace": "test.open"}},
		"routes":        {Key: "test.open", Kind: "route", ResourceType: "test"},
		"route key":     {Key: "test.open", Kind: "route", ResourceType: "test", SurfaceRoutes: map[string]string{"business_workspace": "bad route"}},
	} {
		t.Run("action "+name, func(t *testing.T) {
			value := base
			value.Actions = []notificationmodel.NotificationInboxActionDescriptor{descriptor}
			if _, err := NotificationValidateEventType(value); err == nil {
				t.Fatal("invalid action accepted")
			}
		})
	}
	sensitive := validEventTypeBoundary()
	sensitive.Variables = []notificationmodel.NotificationTemplateVariable{{Key: "safe_token_value", Type: "string"}}
	if _, err := NotificationValidateEventType(sensitive); err == nil {
		t.Fatal("embedded sensitive variable accepted")
	}
}

func TestNotificationRuleRemainingConditions(t *testing.T) {
	base := validEventTypeBoundary()
	for name, rule := range map[string]notificationmodel.NotificationRule{
		"aggregation": {EventTypeKey: base.Key, AggregationWindowSeconds: -1},
		"reminder":    {EventTypeKey: base.Key, ReminderIntervalSeconds: -1},
		"maximum":     {EventTypeKey: base.Key, MaximumReminders: -1},
	} {
		t.Run(name, func(t *testing.T) {
			if err := NotificationValidateEventTypes([]notificationmodel.NotificationEventType{base}, []notificationmodel.NotificationRule{rule}); err == nil {
				t.Fatal("negative rule value accepted")
			}
		})
	}
	if err := NotificationValidateEventTypes([]notificationmodel.NotificationEventType{base}, []notificationmodel.NotificationRule{{EventTypeKey: base.Key, RecoveryEventTypeKey: base.Key}}); err != nil {
		t.Fatalf("valid recovery rejected: %v", err)
	}

	channels := []notificationmodel.NotificationRuleChannel{
		{Channel: "in_app", DelaySeconds: -1},
		{Channel: "in_app", EscalationStep: -1},
		{Channel: "in_app", DigestWindowSeconds: -1},
		{Channel: "in_app", DigestMaximumItems: -1},
		{Channel: "email", DeliveryMode: "digest", DigestWindowSeconds: 1, DelaySeconds: 1},
		{Channel: "email", DeliveryMode: "digest", DigestWindowSeconds: 1, EscalationStep: 1},
		{Channel: "in_app", ConnectorKey: "connector"},
		{Channel: "in_app", Operation: "send"},
		{Channel: "in_app", DelaySeconds: 1},
		{Channel: "in_app", EscalationStep: 1},
		{Channel: "in_app", DeliveryMode: "digest", DigestWindowSeconds: 1},
		{Channel: "in_app", DigestWindowSeconds: 1},
		{Channel: "email", TemplateKey: "test.email", Operation: "send"},
		{Channel: "email", TemplateKey: "test.email", ConnectorKey: "email"},
	}
	for index, channel := range channels {
		rule := notificationmodel.NotificationRule{EventTypeKey: base.Key, Channels: []notificationmodel.NotificationRuleChannel{channel}}
		if err := NotificationValidateEventTypes([]notificationmodel.NotificationEventType{base}, []notificationmodel.NotificationRule{rule}); err == nil {
			t.Fatalf("invalid channel %d accepted", index)
		}
	}
}

func TestNotificationInboxRemainingCompoundConditions(t *testing.T) {
	base := validInboxBoundaryEvent()
	for name, mutate := range map[string]func(*notificationmodel.NotificationEvent){
		"workspace":    func(value *notificationmodel.NotificationEvent) { value.WorkspaceID = "" },
		"source id":    func(value *notificationmodel.NotificationEvent) { value.SourceEventID = "" },
		"action key":   func(value *notificationmodel.NotificationEvent) { value.Snapshot.Actions[0].Key = "bad key" },
		"action label": func(value *notificationmodel.NotificationEvent) { value.Snapshot.Actions[0].Label = "" },
	} {
		t.Run(name, func(t *testing.T) {
			value := base
			value.Snapshot.Actions = append([]notificationmodel.NotificationInboxActionRef(nil), base.Snapshot.Actions...)
			mutate(&value)
			if _, err := NotificationValidateInboxEvent(value); err == nil {
				t.Fatal("invalid event accepted")
			}
		})
	}
	for _, action := range []notificationmodel.NotificationInboxActionRef{
		{Key: "test.open", Kind: "route", Label: "Open", ResourceType: "test", ResourceID: "record"},
		{Key: "test.open", Kind: "business_action", Label: "Open", ResourceType: "test", ResourceID: "record", Style: "secondary"},
		{Key: "test.open", Kind: "route", Label: "Open", ResourceType: "test", ResourceID: "record", Style: "danger"},
	} {
		if _, err := notificationValidateInboxAction(action); err != nil {
			t.Fatalf("valid action rejected: %v", err)
		}
	}
	alert := base
	alert.AlertState = notificationmodel.NotificationAlertFiring
	if _, err := NotificationValidateInboxEvent(alert); err != nil {
		t.Fatalf("grouped alert rejected: %v", err)
	}
	for _, query := range []notificationmodel.NotificationInboxQuery{
		{WorkspaceID: "workspace", Surface: "business_workspace"},
		{WorkspaceID: "workspace", ViewerUserID: "user", Surface: "unknown"},
	} {
		if _, err := NotificationValidateInboxQuery(query); err == nil {
			t.Fatal("invalid query accepted")
		}
	}
	for _, query := range []notificationmodel.NotificationInboxQuery{
		{WorkspaceID: "workspace", ViewerUserID: "user", Surface: "business_workspace", Scope: "team", ReportingUserIDs: []string{"member"}},
		{WorkspaceID: "workspace", ViewerUserID: "user", Surface: "business_workspace", Scope: "delegated", DelegatedUserIDs: []string{"member"}},
		{WorkspaceID: "workspace", ViewerUserID: "user", Surface: "business_workspace", Scope: "delegated", DelegatedUserIDs: []string{"member"}, RecipientUserID: "member"},
		{WorkspaceID: "workspace", ViewerUserID: "user", Surface: "business_workspace", Scope: "mine"},
		{WorkspaceID: "workspace", ViewerUserID: "user", Surface: "business_workspace", Scope: "mine", RecipientUserID: "user"},
	} {
		if _, err := NotificationValidateInboxQuery(query); err != nil {
			t.Fatalf("valid query rejected: %v", err)
		}
	}
	for _, saved := range []notificationmodel.NotificationInboxSavedView{{Key: "saved"}, {Key: "bad key", Name: "Saved"}} {
		if _, err := NotificationValidateInboxSavedView(saved); err == nil {
			t.Fatal("invalid saved view accepted")
		}
	}
}
