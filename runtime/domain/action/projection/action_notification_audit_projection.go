package projection

import (
	"fmt"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func ActionNotificationAuditMetadata(action definitionmodel.ActionSchema, sourceObject definitionmodel.ObjectSchema, record recordmodel.Record, principal principalmodel.Principal, rendered map[string]any) map[string]any {
	kind := actionFirstRenderedString(rendered, "kind", "type")
	if kind == "" {
		kind = "action_notification"
	}
	channel := actionFirstRenderedString(rendered, "channel", "transport")
	if channel == "" {
		channel = "internal"
	}
	recipient := actionFirstRenderedString(rendered, "recipient", "to", "target")
	if recipient == "" {
		recipient = actionFirstRenderedString(record.Data, "owner")
	}
	if recipient == "" {
		recipient = principal.UserID
	}
	subject := actionFirstRenderedString(rendered, "subject", "title")
	if subject == "" {
		subject = action.Label
	}
	body := actionFirstRenderedString(rendered, "body", "message")
	if body == "" {
		body = "Action " + action.Key + " prepared notification for " + sourceObject.Key + " " + record.ID
	}
	severity := actionFirstRenderedString(rendered, "severity", "priority")
	if severity == "" {
		severity = "info"
	}
	return map[string]any{
		"capability": "notify", "action_key": action.Key, "notification_kind": kind, "notification_channel": channel,
		"notification_recipient": recipient, "notification_subject": subject, "notification_body": body, "notification_severity": severity,
	}
}

func actionFirstRenderedString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(fmt.Sprint(values[key])); value != "" && value != "<nil>" {
			return value
		}
	}
	return ""
}
