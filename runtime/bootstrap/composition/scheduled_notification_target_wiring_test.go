package composition

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	dispatchapplication "github.com/domainry/domainry-runtime/runtime/application/dispatch"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
)

type scheduledNotificationPublisher struct {
	intent notificationmodel.NotificationIntent
	calls  int
	create bool
	err    error
}

func (p *scheduledNotificationPublisher) Publish(_ context.Context, intent notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, bool, error) {
	p.calls++
	p.intent = intent
	if p.err != nil {
		return notificationmodel.NotificationEvent{}, false, p.err
	}
	return notificationmodel.NotificationEvent{ID: intent.ID, Status: "queued"}, p.create, nil
}

func scheduledNotificationPayload(t *testing.T, owner schedulersdk.ScheduledPlanOwner, input string, actions ...string) []byte {
	t.Helper()
	raw, err := json.Marshal(schedulersdk.ScheduledPlanDispatch{
		ContractVersion: schedulersdk.ScheduledPlanDispatchContractVersion,
		PlanID:          "plan-reminder", Owner: owner, Input: json.RawMessage(input), AllowedActions: actions,
	})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestScheduledNotificationTargetReauthorizesAndPublishesNarrowIntent(t *testing.T) {
	owner := schedulersdk.ScheduledPlanOwner{WorkspaceID: "workspace", UserID: "user", ProductKey: "product"}
	resolver := &scheduledPrincipalResolver{resolution: identitysdk.PrincipalResolution{
		Principal:    identitysdk.Principal{Known: true, WorkspaceID: owner.WorkspaceID, UserID: owner.UserID, User: identitysdk.User{ID: owner.UserID, Locale: "zh-CN"}},
		AccessBundle: identitysdk.AccessBundle{Subject: identitysdk.Subject{WorkspaceID: identitysdk.WorkspaceID(owner.WorkspaceID), SubjectID: identitysdk.SubjectID(owner.UserID)}},
	}}
	publisher := &scheduledNotificationPublisher{create: true}
	adapter := scheduledNotificationTargetRuntimeAdapter{runtime: &runtimeAssembly{templateID: owner.ProductKey}, principals: resolver, publish: publisher.Publish}
	dueAt := time.Date(2026, time.September, 15, 1, 2, 3, 456000000, time.UTC)
	receipt, err := adapter.ExecuteNotificationTarget(t.Context(), dispatchapplication.NotificationTargetRequest{
		ExecutionID: "scheduler-run-1", IdempotencyKey: "plan-reminder:2026-09-15T01:02:03Z", Operation: scheduledReminderOperation, ScheduledFor: dueAt,
		Payload: scheduledNotificationPayload(t, owner, `{"title":"交周报","message":"请在下班前提交周报。"}`, scheduledReminderAction),
	})
	if err != nil || resolver.calls != 1 || publisher.calls != 1 || receipt.ID == "" || receipt.Status != "queued" {
		t.Fatalf("receipt=%+v resolver=%d publisher=%d err=%v", receipt, resolver.calls, publisher.calls, err)
	}
	intent := publisher.intent
	if intent.ID != receipt.ID || intent.SourceEventID != receipt.ID || intent.DedupeKey != receipt.ID || intent.WorkspaceID != owner.WorkspaceID || len(intent.RecipientUserIDs) != 1 || intent.RecipientUserIDs[0] != owner.UserID || intent.EventType != scheduledReminderEventType || intent.Locale != "zh-CN" || intent.CorrelationID != "scheduler-run-1" || intent.OccurredAt != "2026-09-15T01:02:03.456Z" {
		t.Fatalf("intent=%+v", intent)
	}
	if intent.Variables["title"] != "交周报" || intent.Variables["message"] != "请在下班前提交周报。" || intent.Variables["scheduled_for"] != "2026-09-15T01:02:03Z" || intent.SubjectID != "" || intent.SubjectType != "" {
		t.Fatalf("narrow variables=%+v intent=%+v", intent.Variables, intent)
	}
	publisher.create = false
	replay, err := adapter.ExecuteNotificationTarget(t.Context(), dispatchapplication.NotificationTargetRequest{
		ExecutionID: "scheduler-run-2", IdempotencyKey: "plan-reminder:2026-09-15T01:02:03Z", Operation: scheduledReminderOperation, ScheduledFor: dueAt,
		Payload: scheduledNotificationPayload(t, owner, `{"title":"交周报","message":"请在下班前提交周报。"}`, scheduledReminderAction),
	})
	if err != nil || replay.ID != receipt.ID || replay.Status != "replayed" {
		t.Fatalf("replay=%+v err=%v", replay, err)
	}
}

func TestScheduledNotificationTargetRejectsBroadOrStalePlansBeforePublish(t *testing.T) {
	owner := schedulersdk.ScheduledPlanOwner{WorkspaceID: "workspace", UserID: "user", ProductKey: "product"}
	valid := identitysdk.PrincipalResolution{
		Principal:    identitysdk.Principal{Known: true, WorkspaceID: owner.WorkspaceID, UserID: owner.UserID, User: identitysdk.User{ID: owner.UserID}},
		AccessBundle: identitysdk.AccessBundle{Subject: identitysdk.Subject{WorkspaceID: identitysdk.WorkspaceID(owner.WorkspaceID), SubjectID: identitysdk.SubjectID(owner.UserID)}},
	}
	tests := []struct {
		name       string
		productKey string
		resolution identitysdk.PrincipalResolution
		operation  string
		input      string
		actions    []string
		want       string
	}{
		{name: "wrong operation", productKey: owner.ProductKey, resolution: valid, operation: "publish_any", input: `{"title":"A","message":"B"}`, actions: []string{scheduledReminderAction}, want: "backend.dispatch.scheduled_notification_operation_invalid"},
		{name: "broad action", productKey: owner.ProductKey, resolution: valid, operation: scheduledReminderOperation, input: `{"title":"A","message":"B"}`, actions: []string{scheduledReminderAction, "notification.any.publish"}, want: "backend.dispatch.scheduled_notification_action_denied"},
		{name: "arbitrary event type", productKey: owner.ProductKey, resolution: valid, operation: scheduledReminderOperation, input: `{"title":"A","message":"B","event_type":"integration.credential.expired"}`, actions: []string{scheduledReminderAction}, want: "backend.dispatch.scheduled_notification_input_invalid"},
		{name: "wrong product", productKey: "other", resolution: valid, operation: scheduledReminderOperation, input: `{"title":"A","message":"B"}`, actions: []string{scheduledReminderAction}, want: "backend.dispatch.scheduled_notification_product_denied"},
		{name: "disabled user", productKey: owner.ProductKey, resolution: identitysdk.PrincipalResolution{}, operation: scheduledReminderOperation, input: `{"title":"A","message":"B"}`, actions: []string{scheduledReminderAction}, want: "backend.dispatch.scheduled_notification_principal_denied"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resolver := &scheduledPrincipalResolver{resolution: test.resolution}
			publisher := &scheduledNotificationPublisher{create: true}
			adapter := scheduledNotificationTargetRuntimeAdapter{runtime: &runtimeAssembly{templateID: test.productKey}, principals: resolver, publish: publisher.Publish}
			_, err := adapter.ExecuteNotificationTarget(t.Context(), dispatchapplication.NotificationTargetRequest{ExecutionID: "run", IdempotencyKey: "window", Operation: test.operation, ScheduledFor: time.Now(), Payload: scheduledNotificationPayload(t, owner, test.input, test.actions...)})
			if apperror.CodeOf(err) != test.want || publisher.calls != 0 {
				t.Fatalf("code=%s publisher=%d err=%v", apperror.CodeOf(err), publisher.calls, err)
			}
		})
	}
}
