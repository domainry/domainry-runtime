package composition

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	dispatchapplication "github.com/domainry/domainry-runtime/runtime/application/dispatch"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
)

const (
	scheduledReminderOperation = "publish_reminder"
	scheduledReminderAction    = "notification.reminder.publish"
	scheduledReminderEventType = "scheduler.reminder.due"
	scheduledReminderTitleMax  = 240
	scheduledReminderBodyMax   = 4000
)

// scheduledNotificationTargetRuntimeAdapter is the only Scheduler-to-
// Notification mapping. It narrows product-authored input to reminder facts;
// Notification owns rendering, inbox materialization, preferences, channel
// plans, delivery state, retries, and source-identity deduplication.
type scheduledNotificationTargetRuntimeAdapter struct {
	runtime    *runtimeAssembly
	principals identitysdk.PrincipalResolver
	publish    func(context.Context, notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, bool, error)
}

type scheduledReminderInput struct {
	Title   string `json:"title"`
	Message string `json:"message"`
}

func (a scheduledNotificationTargetRuntimeAdapter) ExecuteNotificationTarget(ctx context.Context, request dispatchapplication.NotificationTargetRequest) (dispatchapplication.NotificationTargetReceipt, error) {
	if a.runtime == nil || a.principals == nil || a.publish == nil {
		return dispatchapplication.NotificationTargetReceipt{}, scheduledNotificationTargetError(apperror.KindUnavailable, "backend.dispatch.scheduled_notification_unavailable")
	}
	if strings.TrimSpace(request.Operation) != scheduledReminderOperation {
		return dispatchapplication.NotificationTargetReceipt{}, scheduledNotificationTargetError(apperror.KindBadRequest, "backend.dispatch.scheduled_notification_operation_invalid")
	}
	var dispatch schedulersdk.ScheduledPlanDispatch
	if len(request.Payload) == 0 || json.Unmarshal(request.Payload, &dispatch) != nil ||
		dispatch.ContractVersion != schedulersdk.ScheduledPlanDispatchContractVersion || dispatch.Owner.Validate() != nil ||
		strings.TrimSpace(dispatch.PlanID) == "" || strings.TrimSpace(request.ExecutionID) == "" || strings.TrimSpace(request.IdempotencyKey) == "" || request.ScheduledFor.IsZero() {
		return dispatchapplication.NotificationTargetReceipt{}, scheduledNotificationTargetError(apperror.KindBadRequest, "backend.dispatch.scheduled_notification_payload_invalid")
	}
	if len(dispatch.AllowedActions) != 1 || strings.TrimSpace(dispatch.AllowedActions[0]) != scheduledReminderAction {
		return dispatchapplication.NotificationTargetReceipt{}, scheduledNotificationTargetError(apperror.KindForbidden, "backend.dispatch.scheduled_notification_action_denied")
	}
	principal, err := resolveCurrentScheduledPlanOwner(ctx, a.productKey(), a.principals, dispatch.Owner, "backend.dispatch.scheduled_notification_product_denied", "backend.dispatch.scheduled_notification_principal_denied")
	if err != nil {
		return dispatchapplication.NotificationTargetReceipt{}, err
	}
	input, err := parseScheduledReminderInput(dispatch.Input)
	if err != nil {
		return dispatchapplication.NotificationTargetReceipt{}, err
	}
	identity := scheduledReminderIdentity(dispatch.Owner.WorkspaceID, dispatch.PlanID, request.IdempotencyKey)
	event, created, err := a.publish(ctx, notificationmodel.NotificationIntent{
		ID: identity, WorkspaceID: principal.WorkspaceID, SourceEventID: identity, EventType: scheduledReminderEventType,
		RecipientUserIDs: []string{principal.UserID}, DedupeKey: identity, OccurredAt: request.ScheduledFor.UTC().Format("2006-01-02T15:04:05.999999999Z07:00"), Locale: strings.TrimSpace(principal.User.Locale),
		Variables:     map[string]any{"title": input.Title, "message": input.Message, "scheduled_for": request.ScheduledFor.UTC().Format("2006-01-02T15:04:05Z07:00")},
		CorrelationID: strings.TrimSpace(request.ExecutionID),
	})
	if err != nil {
		return dispatchapplication.NotificationTargetReceipt{}, err
	}
	status := strings.TrimSpace(event.Status)
	if status == "" {
		status = "queued"
	}
	if !created {
		status = "replayed"
	}
	return dispatchapplication.NotificationTargetReceipt{ID: identity, Status: status}, nil
}

func (a scheduledNotificationTargetRuntimeAdapter) productKey() string {
	a.runtime.mu.RLock()
	defer a.runtime.mu.RUnlock()
	return a.runtime.templateID
}

func parseScheduledReminderInput(raw json.RawMessage) (scheduledReminderInput, error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var input scheduledReminderInput
	if len(raw) == 0 || decoder.Decode(&input) != nil {
		return scheduledReminderInput{}, scheduledNotificationTargetError(apperror.KindBadRequest, "backend.dispatch.scheduled_notification_input_invalid")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return scheduledReminderInput{}, scheduledNotificationTargetError(apperror.KindBadRequest, "backend.dispatch.scheduled_notification_input_invalid")
	}
	input.Title, input.Message = strings.TrimSpace(input.Title), strings.TrimSpace(input.Message)
	if input.Title == "" || input.Message == "" || utf8.RuneCountInString(input.Title) > scheduledReminderTitleMax || utf8.RuneCountInString(input.Message) > scheduledReminderBodyMax {
		return scheduledReminderInput{}, scheduledNotificationTargetError(apperror.KindBadRequest, "backend.dispatch.scheduled_notification_input_invalid")
	}
	return input, nil
}

func scheduledReminderIdentity(workspaceID, planID, idempotencyKey string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(workspaceID) + "\x00" + strings.TrimSpace(planID) + "\x00" + strings.TrimSpace(idempotencyKey)))
	return "scheduled-reminder-" + hex.EncodeToString(digest[:])
}

func scheduledNotificationTargetError(kind apperror.ErrorKind, code string) error {
	return apperror.New(kind, code, nil, nil)
}

var _ dispatchapplication.NotificationTargetRuntime = scheduledNotificationTargetRuntimeAdapter{}
