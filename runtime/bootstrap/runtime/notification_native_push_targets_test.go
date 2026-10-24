package runtime

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/domainry/domainry-notification-sdk/contract"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

type notificationNativePushRecordReaderProbe struct {
	workspaceID string
	object      definitionmodel.ObjectSchema
	query       recordmodel.RecordListQuery
	result      recordmodel.RecordPageResult
	err         error
}

func (p *notificationNativePushRecordReaderProbe) ListRecords(_ context.Context, workspaceID string, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	p.workspaceID, p.object, p.query = workspaceID, object, query
	return p.result, p.err
}

func TestNotificationNativePushTargetResolverReadsLatestActiveProviderToken(t *testing.T) {
	probe := &notificationNativePushRecordReaderProbe{result: recordmodel.RecordPageResult{Items: []recordmodel.Record{{Data: map[string]any{"push_token": " fcm-token "}}}}}
	resolver, err := newNotificationNativePushTargetResolver(probe, []definitionmodel.ObjectSchema{notificationNativePushObject(true)}, []runtimeext.NotificationNativePushTargetDefinition{notificationNativePushDefinition()})
	if err != nil {
		t.Fatal(err)
	}
	token, found, err := resolver.FindNativePushToken(t.Context(), "workspace-a", "user-a", "fcm")
	if err != nil || !found || token != "fcm-token" {
		t.Fatalf("token=%q found=%v err=%v", token, found, err)
	}
	if probe.workspaceID != "workspace-a" || probe.object.Key != "device_registration" {
		t.Fatalf("workspace=%q object=%q", probe.workspaceID, probe.object.Key)
	}
	wantFilters := map[string]any{"recipient_user": "user-a", "platform": "android", "status": "active"}
	if !reflect.DeepEqual(probe.query.Filters, wantFilters) || probe.query.AuthorizationMode != recordmodel.RecordQueryAuthorizationUnrestricted || !probe.query.SkipTotal {
		t.Fatalf("query=%#v", probe.query)
	}
	if len(probe.query.Sort) != 2 || probe.query.Sort[0].Field != "updated_at" || probe.query.Sort[0].Direction != "desc" || probe.query.Sort[1].Field != recordmodel.IDField {
		t.Fatalf("sort=%#v", probe.query.Sort)
	}
}

func TestNotificationNativePushTargetResolverRejectsVisibleTokenField(t *testing.T) {
	_, err := newNotificationNativePushTargetResolver(&notificationNativePushRecordReaderProbe{}, []definitionmodel.ObjectSchema{notificationNativePushObject(false)}, []runtimeext.NotificationNativePushTargetDefinition{notificationNativePushDefinition()})
	if err == nil || !strings.Contains(err.Error(), "token field must be sensitive text") {
		t.Fatalf("error=%v", err)
	}
}

func TestNotificationSDKOutboxPayloadCompilesNativePushContract(t *testing.T) {
	payload, err := notificationSDKOutboxPayload(contract.RenderedNotification{
		Channel: "native_push", Provider: "fcm", Recipients: []string{"device-token"}, Title: "Safety alert", Text: "Please confirm",
		TemplateKey: "safety.alert.fcm", TemplateVersion: 1, TemplateLocale: "en-US", TemplateContentHash: "content", VariablesHash: "variables",
		Metadata: map[string]any{"notification_event_id": "event-a", "notification_channel_plan_id": "plan-a", "dedupe_key": "event-a:user-a:fcm"},
	}, "Facility Ops")
	if err != nil {
		t.Fatal(err)
	}
	if payload["device_token"] != "device-token" || payload["title"] != "Safety alert" || payload["body"] != "Please confirm" || payload["priority"] != "high" || payload["collapse_key"] != "event-a:user-a:fcm" {
		t.Fatalf("payload=%#v", payload)
	}
	data, ok := payload["data"].(map[string]string)
	if !ok || !reflect.DeepEqual(data, map[string]string{"notification_event_id": "event-a", "notification_channel_plan_id": "plan-a"}) {
		t.Fatalf("data=%#v", payload["data"])
	}
}

func notificationNativePushDefinition() runtimeext.NotificationNativePushTargetDefinition {
	return runtimeext.NotificationNativePushTargetDefinition{
		Key: "mobile_device", ObjectKey: "device_registration", RecipientUserField: "recipient_user",
		PlatformField: "platform", TokenField: "push_token", StatusField: "status", ActiveStatus: "active", UpdatedAtField: "updated_at",
	}
}

func notificationNativePushObject(sensitive bool) definitionmodel.ObjectSchema {
	return definitionmodel.ObjectSchema{Key: "device_registration", Fields: []definitionmodel.FieldSchema{
		{Key: "recipient_user", Type: "user"},
		{Key: "platform", Type: "select", Validation: definitionmodel.FieldValidation{Options: []string{"android", "ios"}}},
		{Key: "push_token", Type: "text", Sensitive: sensitive},
		{Key: "status", Type: "select", Validation: definitionmodel.FieldValidation{Options: []string{"active", "revoked"}}},
		{Key: "updated_at", Type: "datetime"},
	}}
}
