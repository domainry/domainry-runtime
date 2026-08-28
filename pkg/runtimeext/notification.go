package runtimeext

import (
	"context"
	"strings"
	"time"
)

const (
	NotificationDispatchOperationKey       = "notification.intent.dispatch"
	NotificationActionGrantDeniedErrorCode = "backend.notification.action_grant_denied"
	NotificationBatchSizeInvalidErrorCode  = "backend.notification.action_batch_size_invalid"
	NotificationBatchMaximum               = 200
)

// NotificationVariable is one typed template fact. Exactly one value pointer
// must be set; project code never receives a map or raw JSON escape hatch.
type NotificationVariable struct {
	Key            string
	StringValue    *string
	NumberValue    *float64
	BooleanValue   *bool
	TimestampValue *time.Time
}

func (v NotificationVariable) valid() bool {
	if strings.TrimSpace(v.Key) == "" {
		return false
	}
	count := 0
	if v.StringValue != nil {
		count++
	}
	if v.NumberValue != nil {
		count++
	}
	if v.BooleanValue != nil {
		count++
	}
	if v.TimestampValue != nil {
		count++
	}
	return count == 1
}

func (v NotificationVariable) Value() any {
	switch {
	case v.StringValue != nil:
		return *v.StringValue
	case v.NumberValue != nil:
		return *v.NumberValue
	case v.BooleanValue != nil:
		return *v.BooleanValue
	case v.TimestampValue != nil:
		return v.TimestampValue.UTC().Format(time.RFC3339Nano)
	default:
		return nil
	}
}

// NotificationIntent is the project Action contract for one governed,
// transactionally staged in-app event. EventType must be an exact Handler
// grant. SourceEventID and DedupeKey are stable business identities.
type NotificationIntent struct {
	EventType        string
	SourceEventID    string
	RecipientUserIDs []string
	// Surface is required and identifies the event's target product surface.
	Surface          string
	SubjectObjectKey string
	SubjectRecordID  string
	SubjectVersion   string
	DedupeKey        string
	GroupKey         string
	Alert            bool
	OccurredAt       time.Time
	Variables        []NotificationVariable
}

func (v NotificationIntent) Valid() bool {
	if strings.TrimSpace(v.EventType) == "" || strings.TrimSpace(v.SourceEventID) == "" || len(v.RecipientUserIDs) == 0 || strings.TrimSpace(v.Surface) == "" || strings.TrimSpace(v.SubjectObjectKey) == "" || strings.TrimSpace(v.SubjectRecordID) == "" || strings.TrimSpace(v.SubjectVersion) == "" || strings.TrimSpace(v.DedupeKey) == "" || v.OccurredAt.IsZero() || (v.Alert && strings.TrimSpace(v.GroupKey) == "") {
		return false
	}
	seenRecipients, seenVariables := map[string]bool{}, map[string]bool{}
	for _, raw := range v.RecipientUserIDs {
		id := strings.TrimSpace(raw)
		if id == "" || seenRecipients[id] {
			return false
		}
		seenRecipients[id] = true
	}
	for _, variable := range v.Variables {
		key := strings.TrimSpace(variable.Key)
		if !variable.valid() || seenVariables[key] {
			return false
		}
		seenVariables[key] = true
	}
	return true
}

type NotificationReceipt struct{ ID string }

type notificationActionExecution interface {
	StageNotification(context.Context, NotificationIntent) (NotificationReceipt, error)
}

type notificationBatchActionExecution interface {
	StageNotificationBatch(context.Context, []NotificationIntent) ([]NotificationReceipt, error)
}

// StageNotification is used by generated narrow notification capabilities.
// It fails closed when an execution host predates notification dispatch.
func StageNotification(ctx context.Context, execution ActionExecution, intent NotificationIntent) (NotificationReceipt, error) {
	capability, ok := execution.(notificationActionExecution)
	if !ok {
		return NotificationReceipt{}, &BusinessError{Code: "backend.notification.action_execution_unsupported", Message: "Runtime Action execution does not support notification dispatch"}
	}
	return capability.StageNotification(ctx, intent)
}

// StageNotificationBatch is used by generated narrow notification
// capabilities. The Runtime implementation validates and stages 1 through 200
// intents as one ordered, all-or-nothing addition to the Action transaction.
func StageNotificationBatch(ctx context.Context, execution ActionExecution, intents []NotificationIntent) ([]NotificationReceipt, error) {
	if len(intents) < 1 || len(intents) > NotificationBatchMaximum {
		return nil, &BusinessError{Code: NotificationBatchSizeInvalidErrorCode, Message: "Notification batch requires 1 through 200 items"}
	}
	capability, ok := execution.(notificationBatchActionExecution)
	if !ok {
		return nil, &BusinessError{Code: "backend.notification.action_batch_execution_unsupported", Message: "Runtime Action execution does not support notification batch dispatch"}
	}
	return capability.StageNotificationBatch(ctx, intents)
}
