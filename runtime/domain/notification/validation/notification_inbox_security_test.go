package validation

import (
	"testing"

	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
)

func TestNotificationInboxEventTypeAndRenderVariablesUseSafeWhitelist(t *testing.T) {
	base := notificationmodel.NotificationEventType{
		Key: "workflow.task.assigned", Source: "workflow", Category: "approval", DefaultSeverity: "warning",
		Surfaces: []string{"business_workspace"}, TemplateKey: "workflow.task.assigned.in_app", DefaultLocale: "en-US", Version: 1, Status: "published",
		Variables: []notificationmodel.NotificationTemplateVariable{{Key: "task_title", Type: "string", Required: true}, {Key: "error_code", Type: "string"}},
		Locales:   map[string]notificationmodel.NotificationInboxEventTypeContent{"en-US": {Title: "Review {{task_title}}", Body: "Use the safe error code {{error_code}}"}},
	}
	if _, err := NotificationValidateEventType(base); err != nil {
		t.Fatal(err)
	}
	for _, unsafe := range []string{"access_token", "client_secret", "provider_response_body", "error_message", "stack_trace", "raw_payload"} {
		candidate := base
		candidate.Variables = append([]notificationmodel.NotificationTemplateVariable(nil), base.Variables...)
		candidate.Variables[0].Key = unsafe
		if _, err := NotificationValidateEventType(candidate); notificationErrorCode(err) != "backend.notification.event_type_variable_unsafe" {
			t.Fatalf("unsafe variable %q err=%v", unsafe, err)
		}
	}
	if _, err := NotificationValidateRenderVariables(notificationmodel.NotificationTemplate{Key: base.TemplateKey, Variables: base.Variables}, map[string]any{
		"task_title": "Expense approval", "error_code": "backend.workflow.timeout", "provider_response": "token=must-not-leak",
	}); notificationErrorCode(err) != "backend.notification.template_variable_unknown" {
		t.Fatalf("unknown variable err=%v", err)
	}
	undeclared := base
	content := undeclared.Locales["en-US"]
	content.Body = "Raw provider response: {{provider_response}}"
	undeclared.Locales = map[string]notificationmodel.NotificationInboxEventTypeContent{"en-US": content}
	if _, err := NotificationValidateEventType(undeclared); notificationErrorCode(err) != "backend.notification.event_type_variable_unknown" {
		t.Fatalf("undeclared content variable err=%v", err)
	}
	event := notificationmodel.NotificationEvent{
		ID: "event", WorkspaceID: "workspace", Source: "workflow", SourceEventID: "source-event", EventType: "workflow.task.assigned", Category: "approval", Severity: "warning", Surface: "business_workspace",
		RecipientUserIDs: []string{"user"}, OccurredAt: "2026-07-28T00:00:00Z", Snapshot: notificationmodel.NotificationInboxSnapshot{Title: "Review task", Body: "Review safely", Actions: []notificationmodel.NotificationInboxActionRef{{Key: "workflow.task.open", Kind: "route", Label: "Review", ResourceType: "workflow_task", ResourceID: "https://evil.example/task"}}},
	}
	if _, err := NotificationValidateInboxEvent(event); notificationErrorCode(err) != "backend.notification.inbox_action_resource_invalid" {
		t.Fatalf("URL action resource err=%v", err)
	}
}
