package projection

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestActionNotificationAuditMetadataOwnsDefaultsAndOverrides(t *testing.T) {
	metadata := ActionNotificationAuditMetadata(
		definitionmodel.ActionSchema{Key: "order.notify", Label: "Notify order"},
		definitionmodel.ObjectSchema{Key: "order"},
		recordmodel.Record{ID: "order-1", Data: map[string]any{"owner": "owner-1"}},
		principalmodel.Principal{Principal: identitysdk.Principal{UserID: "actor-1"}},
		map[string]any{"channel": "email", "subject": "Ready"},
	)
	if metadata["notification_channel"] != "email" || metadata["notification_recipient"] != "owner-1" || metadata["notification_subject"] != "Ready" || metadata["notification_kind"] != "action_notification" {
		t.Fatalf("unexpected notification metadata: %#v", metadata)
	}
}

func TestActionNotificationAuditMetadataCoversExplicitAndFallbackMatrix(t *testing.T) {
	action := definitionmodel.ActionSchema{Key: "order.notify", Label: "Notify order"}
	object := definitionmodel.ObjectSchema{Key: "order"}
	record := recordmodel.Record{ID: "order-1", Data: map[string]any{}}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{UserID: "actor-1"}}

	defaults := ActionNotificationAuditMetadata(action, object, record, principal, map[string]any{})
	if defaults["notification_channel"] != "internal" || defaults["notification_recipient"] != "actor-1" || defaults["notification_subject"] != action.Label || defaults["notification_severity"] != "info" {
		t.Fatalf("defaults=%#v", defaults)
	}
	explicit := ActionNotificationAuditMetadata(action, object, record, principal, map[string]any{
		"kind": "business", "transport": "sms", "target": "customer", "title": "Update", "message": "Ready", "priority": "high",
	})
	if explicit["notification_kind"] != "business" || explicit["notification_channel"] != "sms" || explicit["notification_recipient"] != "customer" || explicit["notification_body"] != "Ready" || explicit["notification_severity"] != "high" {
		t.Fatalf("explicit=%#v", explicit)
	}
	if got := actionFirstRenderedString(map[string]any{"first": "", "second": " value "}, "first", "second"); got != "value" {
		t.Fatalf("first rendered string=%q", got)
	}
}
