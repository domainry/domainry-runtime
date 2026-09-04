package action

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
)

func (e *businessActionExecution) StageNotification(ctx context.Context, intent runtimeext.NotificationIntent) (runtimeext.NotificationReceipt, error) {
	receipts, events, err := e.prepareNotificationBatch(ctx, []runtimeext.NotificationIntent{intent})
	if err != nil {
		return runtimeext.NotificationReceipt{}, err
	}
	e.notifications = append(e.notifications, events...)
	return receipts[0], nil
}

func (e *businessActionExecution) StageNotificationBatch(ctx context.Context, intents []runtimeext.NotificationIntent) ([]runtimeext.NotificationReceipt, error) {
	if len(intents) < 1 || len(intents) > runtimeext.NotificationBatchMaximum {
		return nil, apperror.New(apperror.KindBadRequest, runtimeext.NotificationBatchSizeInvalidErrorCode, nil, map[string]string{"maximum": strconv.Itoa(runtimeext.NotificationBatchMaximum)})
	}
	receipts, events, err := e.prepareNotificationBatch(ctx, intents)
	if err != nil {
		return nil, err
	}
	e.notifications = append(e.notifications, events...)
	return receipts, nil
}

func (e *businessActionExecution) prepareNotificationBatch(ctx context.Context, intents []runtimeext.NotificationIntent) ([]runtimeext.NotificationReceipt, []notificationmodel.NotificationEvent, error) {
	seen := map[string]bool{}
	for _, event := range e.notifications {
		seen[strings.TrimSpace(event.EventType)+"\x00"+strings.TrimSpace(event.SourceEventID)] = true
	}
	for index, intent := range intents {
		if !intent.Valid() {
			return nil, nil, apperror.New(apperror.KindBadRequest, "backend.notification.action_intent_invalid", nil, map[string]string{"index": strconv.Itoa(index)})
		}
		if !e.notificationEventGranted(intent.EventType) {
			return nil, nil, apperror.New(apperror.KindForbidden, runtimeext.NotificationActionGrantDeniedErrorCode, nil, map[string]string{"event_type": intent.EventType, "index": strconv.Itoa(index)})
		}
		identity := strings.TrimSpace(intent.EventType) + "\x00" + strings.TrimSpace(intent.SourceEventID)
		if seen[identity] {
			return nil, nil, apperror.New(apperror.KindBadRequest, "backend.notification.action_batch_source_duplicate", nil, map[string]string{"event_type": intent.EventType, "index": strconv.Itoa(index)})
		}
		seen[identity] = true
	}
	if e.dependencies.CompileNotification == nil {
		return nil, nil, apperror.New(apperror.KindInternal, "backend.notification.action_compiler_required", nil, nil)
	}
	receipts := make([]runtimeext.NotificationReceipt, len(intents))
	events := make([]notificationmodel.NotificationEvent, len(intents))
	for index, intent := range intents {
		identityHash := sha256.Sum256([]byte(e.workspace.ID + "\x00" + strings.TrimSpace(intent.EventType) + "\x00" + strings.TrimSpace(intent.SourceEventID)))
		receipt := runtimeext.NotificationReceipt{ID: "project_notification:" + hex.EncodeToString(identityHash[:])}
		event, err := e.dependencies.CompileNotification(e.unitOfWork.executionContext(ctx), receipt.ID, intent, e.invocation.Principal)
		if err != nil {
			return nil, nil, err
		}
		event.ID = receipt.ID
		event.WorkspaceID = e.workspace.ID
		event.SourceEventID = strings.TrimSpace(intent.SourceEventID)
		event.EventType = strings.TrimSpace(intent.EventType)
		event.RecipientUserIDs = append([]string(nil), intent.RecipientUserIDs...)
		event.SubjectType = strings.TrimSpace(intent.SubjectObjectKey)
		event.SubjectID = strings.TrimSpace(intent.SubjectRecordID)
		event.SubjectVersion = strings.TrimSpace(intent.SubjectVersion)
		event.DedupeKey = strings.TrimSpace(intent.DedupeKey)
		event.OccurredAt = intent.OccurredAt.UTC().Format(time.RFC3339Nano)
		event.CorrelationID = e.identity.ExecutionID
		receipts[index], events[index] = receipt, event
	}
	return receipts, events, nil
}

func (e *businessActionExecution) notificationEventGranted(eventType string) bool {
	for _, granted := range e.notificationGrants {
		if strings.TrimSpace(granted) == strings.TrimSpace(eventType) {
			return true
		}
	}
	return false
}
