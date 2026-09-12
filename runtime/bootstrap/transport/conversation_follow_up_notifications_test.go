package transport

import (
	"context"
	"testing"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-foundation/requestcontext"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
)

type followUpPrincipalResolver struct {
	request    identitysdk.PrincipalResolutionRequest
	workspace  string
	resolution identitysdk.PrincipalResolution
}

func (r *followUpPrincipalResolver) Resolve(ctx context.Context, request identitysdk.PrincipalResolutionRequest) (identitysdk.PrincipalResolution, error) {
	r.request = request
	r.workspace = requestcontext.WorkspaceID(ctx)
	return r.resolution, nil
}

func TestAgentFollowUpNotificationPublisherRevalidatesAndNarrowsIntent(t *testing.T) {
	application := identitysdk.ApplicationScope{WorkspaceID: "workspace", ApplicationKey: "product"}
	resolver := &followUpPrincipalResolver{resolution: identitysdk.PrincipalResolution{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "user", User: identitysdk.User{ID: "user", Locale: "zh-CN"}}}}
	var intent notificationmodel.NotificationIntent
	publisher := agentFollowUpNotificationPublisher{runtimeID: "runtime", application: application, principals: resolver, publish: func(_ context.Context, value notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, bool, error) {
		intent = value
		return notificationmodel.NotificationEvent{ID: value.ID}, true, nil
	}}
	now := time.Date(2026, 9, 12, 9, 0, 0, 0, time.UTC)
	event := agentsdk.ConversationFollowUpEvent{ID: "followup-event", Kind: agentsdk.ConversationFollowUpEventNeedsAction, Authority: agentsdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}, PlanID: "plan", TaskID: "task", RunID: "run", Goal: "检查发布", Question: "是否批准？", Occurrence: 2, OccurredAt: now}
	if err := publisher.PublishConversationFollowUp(t.Context(), event); err != nil {
		t.Fatal(err)
	}
	if resolver.workspace != string(application.WorkspaceID) || resolver.request.SubjectID != "user" || intent.EventType != "agent.follow_up.needs_action" || intent.WorkspaceID != "workspace" || len(intent.RecipientUserIDs) != 1 || intent.RecipientUserIDs[0] != "user" || intent.SubjectID != "task" || intent.GroupKey != "plan" || intent.DedupeKey != event.ID || intent.Locale != "zh-CN" || intent.Variables["question"] != "是否批准？" {
		t.Fatalf("resolution=%+v intent=%+v", resolver.request, intent)
	}
	if _, found := intent.Variables["observation"]; found {
		t.Fatal("private observation leaked into Notification intent")
	}
}

func TestAgentFollowUpNotificationPublisherRejectsBroadOrStaleEvents(t *testing.T) {
	application := identitysdk.ApplicationScope{WorkspaceID: "workspace", ApplicationKey: "product"}
	valid := agentsdk.ConversationFollowUpEvent{ID: "event", Kind: agentsdk.ConversationFollowUpEventChanged, Authority: agentsdk.ConversationAuthority{Known: true, RuntimeID: "runtime", WorkspaceID: "workspace", UserID: "user"}, PlanID: "plan", TaskID: "task", RunID: "run", Goal: "goal", Summary: "changed", Occurrence: 1, OccurredAt: time.Now()}
	for _, test := range []struct {
		name      string
		event     agentsdk.ConversationFollowUpEvent
		principal identitysdk.Principal
	}{
		{name: "wrong runtime", event: func() agentsdk.ConversationFollowUpEvent {
			value := valid
			value.Authority.RuntimeID = "other"
			return value
		}(), principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "user"}},
		{name: "moved workspace", event: valid, principal: identitysdk.Principal{Known: true, WorkspaceID: "other", UserID: "user"}},
		{name: "missing summary", event: func() agentsdk.ConversationFollowUpEvent { value := valid; value.Summary = ""; return value }(), principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "user"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			publisher := agentFollowUpNotificationPublisher{runtimeID: "runtime", application: application, principals: &followUpPrincipalResolver{resolution: identitysdk.PrincipalResolution{Principal: test.principal}}, publish: func(context.Context, notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, bool, error) {
				calls++
				return notificationmodel.NotificationEvent{}, true, nil
			}}
			if err := publisher.PublishConversationFollowUp(t.Context(), test.event); err == nil || calls != 0 {
				t.Fatalf("err=%v calls=%d", err, calls)
			}
		})
	}
}
