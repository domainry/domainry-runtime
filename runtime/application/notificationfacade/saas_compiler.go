package notificationfacade

import (
	"fmt"
	"strings"

	runtimemodel "github.com/domainry/domainry-notification-sdk/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// SaaSCompiler preserves the exact producer intent until the business-owned
// transaction inserts it into Runtime's durable publication outbox. Rendering
// and Notification-domain event compilation remain owned by Notification SaaS.
type SaaSCompiler struct{}

func (SaaSCompiler) CompileInboxIntent(value runtimemodel.NotificationIntent, scope principalmodel.SystemScope) (runtimemodel.NotificationEvent, error) {
	if _, err := principalmodel.NewSystemCommandScope(scope); err != nil {
		return runtimemodel.NotificationEvent{}, err
	}
	if strings.TrimSpace(value.ID) == "" || strings.TrimSpace(value.WorkspaceID) == "" || strings.TrimSpace(value.SourceEventID) == "" || strings.TrimSpace(value.EventType) == "" || strings.TrimSpace(value.Surface) == "" || strings.TrimSpace(value.OccurredAt) == "" {
		return runtimemodel.NotificationEvent{}, fmt.Errorf("Notification SaaS publication intent is missing a required identity field")
	}
	intent := value
	return runtimemodel.NotificationEvent{
		ID: value.ID, WorkspaceID: value.WorkspaceID, SourceEventID: value.SourceEventID,
		EventType: value.EventType, Severity: value.Severity, Surface: value.Surface,
		RecipientUserIDs:     append([]string(nil), value.RecipientUserIDs...),
		AudienceResolverKeys: append([]string(nil), value.AudienceResolverKeys...),
		SubjectType:          value.SubjectType, SubjectID: value.SubjectID, SubjectVersion: value.SubjectVersion,
		GroupKey: value.GroupKey, DedupeKey: value.DedupeKey, ActionState: value.ActionState,
		AlertState: value.AlertState, ExpiresAt: value.ExpiresAt, OccurredAt: value.OccurredAt,
		CorrelationID: value.CorrelationID, TraceID: value.TraceID, PublicationIntent: &intent,
	}, nil
}
