package validation

import (
	"strings"
	"testing"

	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
)

func validInboxBoundaryEvent() notificationmodel.NotificationEvent {
	return notificationmodel.NotificationEvent{
		ID: " event ", WorkspaceID: " workspace ", Source: " workflow ", SourceEventID: " source ", EventType: " workflow.task.assigned ", Category: " approval ", Severity: " warning ", Surface: " business_workspace ",
		RecipientUserIDs: []string{" user ", "user", ""}, SubjectType: " task ", SubjectID: " task-1 ", SubjectVersion: " 1 ", GroupKey: " group ", DedupeKey: " dedupe ", OccurredAt: "2026-07-28T00:00:00Z", ExpiresAt: "2026-07-29T00:00:00Z",
		Snapshot:           notificationmodel.NotificationInboxSnapshot{Title: " Review ", Body: " Body ", Facts: []notificationmodel.NotificationTemplateFact{{Key: "Fact", Value: "Value"}}, Actions: []notificationmodel.NotificationInboxActionRef{{Key: " task.open ", Kind: "route", Label: " Open ", ResourceType: " task ", ResourceID: " task-1 ", Style: " primary "}}},
		LocalizedSnapshots: map[string]notificationmodel.NotificationInboxSnapshot{"en-US": {Title: "Review", Body: "Body"}}, Status: "processing", AttemptCount: 3, NextAttemptAt: "later", LastErrorCode: "error", LeaseOwner: "worker", LeaseExpiresAt: "later", FencingToken: 2,
	}
}

func TestNotificationValidateInboxEventBoundaryFailures(t *testing.T) {
	base := validInboxBoundaryEvent()
	valid, err := NotificationValidateInboxEvent(base)
	if err != nil || valid.ID != "event" || len(valid.RecipientUserIDs) != 1 || valid.Status != notificationmodel.NotificationEventQueued || valid.AttemptCount != 0 || valid.FencingToken != 0 || valid.ActionState != notificationmodel.NotificationActionNone {
		t.Fatalf("valid=%+v err=%v", valid, err)
	}
	tests := []struct {
		name, code string
		mutate     func(*notificationmodel.NotificationEvent)
	}{
		{"resolver count", "backend.notification.inbox_audience_resolvers_invalid", func(v *notificationmodel.NotificationEvent) {
			for i := 0; i < 9; i++ {
				v.AudienceResolverKeys = append(v.AudienceResolverKeys, "resolver."+string(rune('a'+i)))
			}
		}},
		{"resolver key", "backend.notification.inbox_audience_resolvers_invalid", func(v *notificationmodel.NotificationEvent) { v.AudienceResolverKeys = []string{"bad key"} }},
		{"identity", "backend.notification.inbox_event_identity_required", func(v *notificationmodel.NotificationEvent) { v.ID = "" }},
		{"source key", "backend.notification.inbox_event_key_invalid", func(v *notificationmodel.NotificationEvent) { v.Source = "bad key" }},
		{"event key", "backend.notification.inbox_event_key_invalid", func(v *notificationmodel.NotificationEvent) { v.EventType = "bad key" }},
		{"category key", "backend.notification.inbox_event_key_invalid", func(v *notificationmodel.NotificationEvent) { v.Category = "bad key" }},
		{"severity", "backend.notification.inbox_event_severity_invalid", func(v *notificationmodel.NotificationEvent) { v.Severity = "high" }},
		{"surface", "backend.notification.inbox_event_surface_invalid", func(v *notificationmodel.NotificationEvent) { v.Surface = "admin_console" }},
		{"action state", "backend.notification.inbox_action_state_invalid", func(v *notificationmodel.NotificationEvent) { v.ActionState = "done" }},
		{"alert state", "backend.notification.inbox_alert_state_invalid", func(v *notificationmodel.NotificationEvent) { v.AlertState = "bad" }},
		{"alert group", "backend.notification.inbox_alert_state_invalid", func(v *notificationmodel.NotificationEvent) {
			v.AlertState, v.GroupKey = notificationmodel.NotificationAlertFiring, ""
		}},
		{"occurred", "backend.notification.inbox_event_time_invalid", func(v *notificationmodel.NotificationEvent) { v.OccurredAt = "bad" }},
		{"expires", "backend.notification.inbox_event_time_invalid", func(v *notificationmodel.NotificationEvent) { v.ExpiresAt = "bad" }},
		{"empty title", "backend.notification.inbox_title_invalid", func(v *notificationmodel.NotificationEvent) { v.Snapshot.Title = "" }},
		{"long title", "backend.notification.inbox_title_invalid", func(v *notificationmodel.NotificationEvent) { v.Snapshot.Title = strings.Repeat("界", 241) }},
		{"link title scheme", "backend.notification.inbox_title_link_forbidden", func(v *notificationmodel.NotificationEvent) { v.Snapshot.Title = "https://example.test" }},
		{"link title path", "backend.notification.inbox_title_link_forbidden", func(v *notificationmodel.NotificationEvent) { v.Snapshot.Title = "/internal/path" }},
		{"link title www", "backend.notification.inbox_title_link_forbidden", func(v *notificationmodel.NotificationEvent) { v.Snapshot.Title = "www.example.test" }},
		{"empty body", "backend.notification.inbox_body_invalid", func(v *notificationmodel.NotificationEvent) { v.Snapshot.Body = "" }},
		{"long body", "backend.notification.inbox_body_invalid", func(v *notificationmodel.NotificationEvent) { v.Snapshot.Body = strings.Repeat("x", 4001) }},
		{"fact count", "backend.notification.inbox_components_limit", func(v *notificationmodel.NotificationEvent) {
			v.Snapshot.Facts = make([]notificationmodel.NotificationTemplateFact, 21)
		}},
		{"action count", "backend.notification.inbox_components_limit", func(v *notificationmodel.NotificationEvent) {
			v.Snapshot.Actions = make([]notificationmodel.NotificationInboxActionRef, 6)
		}},
		{"fact key", "backend.notification.inbox_fact_invalid", func(v *notificationmodel.NotificationEvent) { v.Snapshot.Facts[0].Key = "" }},
		{"fact value", "backend.notification.inbox_fact_invalid", func(v *notificationmodel.NotificationEvent) { v.Snapshot.Facts[0].Value = "" }},
		{"action", "backend.notification.inbox_action_invalid", func(v *notificationmodel.NotificationEvent) { v.Snapshot.Actions[0].Kind = "http" }},
		{"action label link", "backend.notification.inbox_action_invalid", func(v *notificationmodel.NotificationEvent) { v.Snapshot.Actions[0].Label = "/api/task" }},
		{"action resource type", "backend.notification.inbox_action_resource_invalid", func(v *notificationmodel.NotificationEvent) { v.Snapshot.Actions[0].ResourceType = "bad type" }},
		{"action resource id", "backend.notification.inbox_action_resource_invalid", func(v *notificationmodel.NotificationEvent) { v.Snapshot.Actions[0].ResourceID = "" }},
		{"action style", "backend.notification.inbox_action_style_invalid", func(v *notificationmodel.NotificationEvent) { v.Snapshot.Actions[0].Style = "link" }},
		{"locale", "backend.notification.inbox_snapshot_locale_invalid", func(v *notificationmodel.NotificationEvent) {
			v.LocalizedSnapshots = map[string]notificationmodel.NotificationInboxSnapshot{" ": {Title: "Title", Body: "Body"}}
		}},
		{"localized invalid", "backend.notification.inbox_title_invalid", func(v *notificationmodel.NotificationEvent) {
			v.LocalizedSnapshots = map[string]notificationmodel.NotificationInboxSnapshot{"en-US": {Body: "Body"}}
		}},
		{"recipients", "backend.notification.inbox_recipients_invalid", func(v *notificationmodel.NotificationEvent) { v.RecipientUserIDs, v.AudienceResolverKeys = nil, nil }},
		{"recipient maximum", "backend.notification.inbox_recipients_invalid", func(v *notificationmodel.NotificationEvent) {
			v.RecipientUserIDs = make([]string, 501)
			for i := range v.RecipientUserIDs {
				v.RecipientUserIDs[i] = "user-" + string(rune(1000+i))
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := base
			candidate.RecipientUserIDs = append([]string(nil), base.RecipientUserIDs...)
			candidate.AudienceResolverKeys = append([]string(nil), base.AudienceResolverKeys...)
			candidate.Snapshot.Facts = append([]notificationmodel.NotificationTemplateFact(nil), base.Snapshot.Facts...)
			candidate.Snapshot.Actions = append([]notificationmodel.NotificationInboxActionRef(nil), base.Snapshot.Actions...)
			candidate.LocalizedSnapshots = map[string]notificationmodel.NotificationInboxSnapshot{"en-US": base.LocalizedSnapshots["en-US"]}
			test.mutate(&candidate)
			if _, err := NotificationValidateInboxEvent(candidate); notificationErrorCode(err) != test.code {
				t.Fatalf("code=%q err=%v", notificationErrorCode(err), err)
			}
		})
	}
	resolverOnly := base
	resolverOnly.RecipientUserIDs, resolverOnly.AudienceResolverKeys = nil, []string{"workflow_task_assignee", "workflow_task_assignee", ""}
	if value, err := NotificationValidateInboxEvent(resolverOnly); err != nil || len(value.AudienceResolverKeys) != 1 {
		t.Fatalf("resolver only=%+v err=%v", value, err)
	}
}

func TestNotificationValidateInboxQueryAndSavedViewBoundaries(t *testing.T) {
	base := notificationmodel.NotificationInboxQuery{WorkspaceID: " workspace ", ViewerUserID: " viewer ", Surface: " business_workspace ", From: "2026-07-28T00:00:00Z", To: "2026-07-29T00:00:00Z"}
	value, err := NotificationValidateInboxQuery(base)
	if err != nil || value.Scope != "mine" || value.Mailbox != "inbox" || value.Limit != 50 || value.RecipientUserID != "viewer" {
		t.Fatalf("value=%+v err=%v", value, err)
	}
	for _, test := range []struct {
		name, code string
		value      notificationmodel.NotificationInboxQuery
	}{
		{"scope identity", "backend.notification.inbox_scope_invalid", notificationmodel.NotificationInboxQuery{}},
		{"scope kind", "backend.notification.inbox_audience_scope_invalid", notificationmodel.NotificationInboxQuery{WorkspaceID: "w", ViewerUserID: "u", Surface: "business_workspace", Scope: "all"}},
		{"team empty", "backend.notification.inbox_team_scope_denied", notificationmodel.NotificationInboxQuery{WorkspaceID: "w", ViewerUserID: "u", Surface: "business_workspace", Scope: "team"}},
		{"team outside", "backend.notification.inbox_team_scope_denied", notificationmodel.NotificationInboxQuery{WorkspaceID: "w", ViewerUserID: "u", Surface: "business_workspace", Scope: "team", ReportingUserIDs: []string{"a"}, RecipientUserID: "b"}},
		{"delegated empty", "backend.notification.inbox_delegated_scope_denied", notificationmodel.NotificationInboxQuery{WorkspaceID: "w", ViewerUserID: "u", Surface: "business_workspace", Scope: "delegated"}},
		{"delegated outside", "backend.notification.inbox_delegated_scope_denied", notificationmodel.NotificationInboxQuery{WorkspaceID: "w", ViewerUserID: "u", Surface: "business_workspace", Scope: "delegated", DelegatedUserIDs: []string{"a"}, RecipientUserID: "b"}},
		{"mine spoof", "backend.notification.inbox_team_filter_invalid", notificationmodel.NotificationInboxQuery{WorkspaceID: "w", ViewerUserID: "u", Surface: "business_workspace", Scope: "mine", RecipientUserID: "b"}},
		{"mailbox", "backend.notification.inbox_mailbox_invalid", notificationmodel.NotificationInboxQuery{WorkspaceID: "w", ViewerUserID: "u", Surface: "business_workspace", Mailbox: "other"}},
		{"query", "backend.notification.inbox_query_invalid", notificationmodel.NotificationInboxQuery{WorkspaceID: "w", ViewerUserID: "u", Surface: "business_workspace", Query: strings.Repeat("x", 201)}},
		{"from", "backend.notification.inbox_query_time_invalid", notificationmodel.NotificationInboxQuery{WorkspaceID: "w", ViewerUserID: "u", Surface: "business_workspace", From: "bad"}},
		{"to", "backend.notification.inbox_query_time_invalid", notificationmodel.NotificationInboxQuery{WorkspaceID: "w", ViewerUserID: "u", Surface: "business_workspace", To: "bad"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := NotificationValidateInboxQuery(test.value); notificationErrorCode(err) != test.code {
				t.Fatalf("err=%v", err)
			}
		})
	}
	team, err := NotificationValidateInboxQuery(notificationmodel.NotificationInboxQuery{WorkspaceID: "w", ViewerUserID: "u", Surface: "business_workspace", Scope: "team", ReportingUserIDs: []string{"a", "b"}, RecipientUserID: "b", Limit: 101})
	if err != nil || len(team.RecipientUserIDs) != 1 || team.RecipientUserIDs[0] != "b" || team.Limit != 100 {
		t.Fatalf("team=%+v err=%v", team, err)
	}
	delegated, err := NotificationValidateInboxQuery(notificationmodel.NotificationInboxQuery{WorkspaceID: "w", ViewerUserID: "u", Surface: "business_workspace", Scope: "delegated", DelegatedUserIDs: []string{"a", "b"}})
	if err != nil || len(delegated.RecipientUserIDs) != 2 {
		t.Fatalf("delegated=%+v err=%v", delegated, err)
	}
	for _, saved := range []notificationmodel.NotificationInboxSavedView{{}, {Key: "saved", Name: strings.Repeat("x", 81)}, {Key: "saved", Name: "Saved", Mailbox: "bad"}} {
		if _, err := NotificationValidateInboxSavedView(saved); err == nil {
			t.Fatalf("saved view accepted=%+v", saved)
		}
	}
	saved, err := NotificationValidateInboxSavedView(notificationmodel.NotificationInboxSavedView{Key: " saved ", Name: " Saved ", Scope: "team", TeamMemberID: "report", Mailbox: "unread"})
	if err != nil || saved.Key != "saved" || saved.Scope != "team" {
		t.Fatalf("saved=%+v err=%v", saved, err)
	}
}

func validEventTypeBoundary() notificationmodel.NotificationEventType {
	return notificationmodel.NotificationEventType{Key: "test.event", Source: "test", Category: "system", DefaultSeverity: "info", Surfaces: []string{"business_workspace"}, TemplateKey: "test.event.in_app", DefaultLocale: "en-US", Locales: map[string]notificationmodel.NotificationInboxEventTypeContent{"en-US": {Title: "Hello {{name}}", Body: "Body", Facts: []notificationmodel.NotificationTemplateFact{{Key: "Name", Value: "{{name}}"}}}}, Variables: []notificationmodel.NotificationTemplateVariable{{Key: "name", Type: "string"}}, Version: 1, Status: "published"}
}

func TestNotificationEventTypeAndRuleBoundaryMatrix(t *testing.T) {
	base := validEventTypeBoundary()
	if _, err := NotificationValidateEventType(base); err != nil {
		t.Fatal(err)
	}
	mutations := []func(*notificationmodel.NotificationEventType){
		func(v *notificationmodel.NotificationEventType) { v.Key = "bad key" }, func(v *notificationmodel.NotificationEventType) { v.DefaultLocale = "fr" }, func(v *notificationmodel.NotificationEventType) { v.DefaultSeverity = "high" }, func(v *notificationmodel.NotificationEventType) { v.Surfaces = nil }, func(v *notificationmodel.NotificationEventType) { v.Surfaces = []string{"admin_console"} },
		func(v *notificationmodel.NotificationEventType) {
			v.Variables = []notificationmodel.NotificationTemplateVariable{{Key: "bad key", Type: "string"}}
		}, func(v *notificationmodel.NotificationEventType) {
			v.Variables = []notificationmodel.NotificationTemplateVariable{{Key: "name", Type: "string"}, {Key: "name", Type: "string"}}
		}, func(v *notificationmodel.NotificationEventType) {
			v.Variables = []notificationmodel.NotificationTemplateVariable{{Key: "name", Type: "object"}}
		},
		func(v *notificationmodel.NotificationEventType) {
			v.Actions = []notificationmodel.NotificationInboxActionDescriptor{{Key: "bad key"}}
		}, func(v *notificationmodel.NotificationEventType) {
			v.Actions = []notificationmodel.NotificationInboxActionDescriptor{{Key: "test.open", Kind: "route", ResourceType: "test", SurfaceRoutes: map[string]string{"admin_console": "test.open"}}}
		}, func(v *notificationmodel.NotificationEventType) {
			v.Actions = []notificationmodel.NotificationInboxActionDescriptor{{Key: "test.open", Kind: "route", ResourceType: "test", SurfaceRoutes: map[string]string{"business_workspace": "test.open"}}}
		},
		func(v *notificationmodel.NotificationEventType) {
			v.Locales = map[string]notificationmodel.NotificationInboxEventTypeContent{"": {Title: "Title", Body: "Body"}}
		}, func(v *notificationmodel.NotificationEventType) {
			v.Locales = map[string]notificationmodel.NotificationInboxEventTypeContent{"en-US": v.Locales["en-US"], "": {Title: "Title", Body: "Body"}}
		}, func(v *notificationmodel.NotificationEventType) {
			v.Locales = map[string]notificationmodel.NotificationInboxEventTypeContent{"en-US": {Title: "", Body: "Body"}}
		}, func(v *notificationmodel.NotificationEventType) {
			v.Locales = map[string]notificationmodel.NotificationInboxEventTypeContent{"en-US": {Title: "Title", Body: ""}}
		}, func(v *notificationmodel.NotificationEventType) {
			c := v.Locales["en-US"]
			c.Body = "{{unknown}}"
			v.Locales = map[string]notificationmodel.NotificationInboxEventTypeContent{"en-US": c}
		},
	}
	for i, mutate := range mutations {
		candidate := base
		candidate.Variables = append([]notificationmodel.NotificationTemplateVariable(nil), base.Variables...)
		candidate.Surfaces = append([]string(nil), base.Surfaces...)
		candidate.Locales = map[string]notificationmodel.NotificationInboxEventTypeContent{"en-US": base.Locales["en-US"]}
		mutate(&candidate)
		if _, err := NotificationValidateEventType(candidate); err == nil {
			t.Fatalf("mutation %d accepted", i)
		}
	}
	action := base
	content := action.Locales["en-US"]
	content.ActionLabels = map[string]string{"test.open": "Open"}
	action.Locales = map[string]notificationmodel.NotificationInboxEventTypeContent{"en-US": content}
	action.Actions = []notificationmodel.NotificationInboxActionDescriptor{{Key: "test.open", Kind: "business_action", ResourceType: "test", SurfaceRoutes: map[string]string{"business_workspace": "test.open"}}}
	if _, err := NotificationValidateEventType(action); err != nil {
		t.Fatal(err)
	}
	validRule := notificationmodel.NotificationRule{EventTypeKey: "test.event", Enabled: true, Channels: []notificationmodel.NotificationRuleChannel{{Channel: "in_app"}, {Channel: "email", TemplateKey: "test.email", ConnectorKey: "email", Operation: "send_email"}}}
	if err := NotificationValidateEventTypes([]notificationmodel.NotificationEventType{base}, []notificationmodel.NotificationRule{validRule}); err != nil {
		t.Fatal(err)
	}
	badRules := []notificationmodel.NotificationRule{
		{}, {EventTypeKey: "missing"}, {EventTypeKey: "test.event", DedupeWindowSeconds: -1}, {EventTypeKey: "test.event", RecoveryEventTypeKey: "missing"},
		{EventTypeKey: "test.event", Channels: []notificationmodel.NotificationRuleChannel{{Channel: "in_app"}, {Channel: "in_app"}}},
		{EventTypeKey: "test.event", Channels: []notificationmodel.NotificationRuleChannel{{Channel: "email", DeliveryMode: "later"}}},
		{EventTypeKey: "test.event", Channels: []notificationmodel.NotificationRuleChannel{{Channel: "email", DeliveryMode: "digest"}}},
		{EventTypeKey: "test.event", Channels: []notificationmodel.NotificationRuleChannel{{Channel: "in_app", TemplateKey: "bad"}}},
		{EventTypeKey: "test.event", Channels: []notificationmodel.NotificationRuleChannel{{Channel: "email"}}},
		{EventTypeKey: "test.event", Channels: []notificationmodel.NotificationRuleChannel{{Channel: "sms"}}},
	}
	for i, rule := range badRules {
		if err := NotificationValidateEventTypes([]notificationmodel.NotificationEventType{base}, []notificationmodel.NotificationRule{rule}); err == nil {
			t.Fatalf("bad rule %d accepted", i)
		}
	}
	if err := NotificationValidateEventTypes([]notificationmodel.NotificationEventType{base, base}, nil); err == nil {
		t.Fatal("duplicate event type accepted")
	}
	invalidType := base
	invalidType.Status = "draft"
	if err := NotificationValidateEventTypes([]notificationmodel.NotificationEventType{invalidType}, nil); err == nil {
		t.Fatal("invalid event type accepted")
	}
}
