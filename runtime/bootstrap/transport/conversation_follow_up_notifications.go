package transport

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	"github.com/domainry/domainry-foundation/requestcontext"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
)

// agentFollowUpNotificationPublisher is the only Agent-to-Notification
// mapping. Agent owns scope, observation comparison and outcome classification;
// Notification owns rendering, recipients, channels, delivery and dedupe.
type agentFollowUpNotificationPublisher struct {
	runtimeID   string
	application identitysdk.ApplicationScope
	principals  identitysdk.PrincipalResolver
	publish     func(context.Context, notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, bool, error)
}

func (p agentFollowUpNotificationPublisher) PublishConversationFollowUp(ctx context.Context, event agentsdk.ConversationFollowUpEvent) error {
	if p.principals == nil || p.publish == nil {
		return fmt.Errorf("Agent follow-up notification publisher is unavailable")
	}
	if !event.Authority.Known || event.Authority.RuntimeID != p.runtimeID || event.Authority.WorkspaceID != string(p.application.WorkspaceID) ||
		strings.TrimSpace(event.Authority.UserID) == "" || strings.TrimSpace(event.ID) == "" || strings.TrimSpace(event.PlanID) == "" ||
		strings.TrimSpace(event.TaskID) == "" || strings.TrimSpace(event.RunID) == "" || strings.TrimSpace(event.Goal) == "" || event.Occurrence < 1 || event.OccurredAt.IsZero() {
		return fmt.Errorf("Agent follow-up event is invalid")
	}
	eventType := "agent.follow_up." + event.Kind
	severity := "info"
	switch event.Kind {
	case agentsdk.ConversationFollowUpEventChanged, agentsdk.ConversationFollowUpEventCompleted:
		if strings.TrimSpace(event.Summary) == "" {
			return fmt.Errorf("Agent follow-up summary is required")
		}
	case agentsdk.ConversationFollowUpEventFailed:
		severity = "warning"
		if strings.TrimSpace(event.ErrorCode) == "" {
			return fmt.Errorf("Agent follow-up error code is required")
		}
	case agentsdk.ConversationFollowUpEventNeedsAction:
		severity = "warning"
		if strings.TrimSpace(event.Question) == "" {
			return fmt.Errorf("Agent follow-up question is required")
		}
	default:
		return fmt.Errorf("Agent follow-up event kind is invalid")
	}
	resolution, err := p.principals.Resolve(requestcontext.WithWorkspaceID(ctx, event.Authority.WorkspaceID), identitysdk.PrincipalResolutionRequest{SubjectID: identitysdk.SubjectID(event.Authority.UserID), RoleKey: event.Authority.RoleKey})
	if err != nil {
		return err
	}
	principal := resolution.Principal
	if !principal.Known || principal.WorkspaceID != event.Authority.WorkspaceID || principal.UserID != event.Authority.UserID {
		return fmt.Errorf("Agent follow-up principal is no longer available")
	}
	_, _, err = p.publish(ctx, notificationmodel.NotificationIntent{
		ID: event.ID, WorkspaceID: principal.WorkspaceID, SourceEventID: event.ID, EventType: eventType, Severity: severity,
		RecipientUserIDs: []string{principal.UserID}, SubjectType: "agent_follow_up", SubjectID: event.TaskID,
		SubjectVersion: strconv.Itoa(event.Occurrence), GroupKey: event.PlanID, DedupeKey: event.ID,
		OccurredAt: event.OccurredAt.UTC().Format(time.RFC3339Nano), Locale: strings.TrimSpace(principal.User.Locale),
		Variables: map[string]any{
			"goal": strings.TrimSpace(event.Goal), "summary": strings.TrimSpace(event.Summary), "question": strings.TrimSpace(event.Question),
			"error_code": strings.TrimSpace(event.ErrorCode), "occurrence": event.Occurrence,
		},
		CorrelationID: event.RunID,
	})
	return err
}

var _ agentsdk.ConversationFollowUpPublisher = agentFollowUpNotificationPublisher{}
