package recordnotification

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	notificationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/notification"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
)

// RecordBatchNotificationCommitter coordinates Record and Notification
// persistence without coupling either owner-specific adapter to the other.
type RecordBatchNotificationCommitter struct {
	store         *database.RuntimeStore
	records       recordpersistence.RecordStore
	notifications notificationpersistence.InboxEventWriter
}

func NewRecordBatchNotificationCommitter(store *database.RuntimeStore) RecordBatchNotificationCommitter {
	return RecordBatchNotificationCommitter{
		store: store, records: recordpersistence.NewRecordStore(store), notifications: notificationpersistence.NewInboxEventWriter(store),
	}
}

func (s RecordBatchNotificationCommitter) CommitRecordBatchTerminal(ctx context.Context, job recordmodel.RecordBatchJob, status string, now time.Time, event notificationmodel.NotificationEvent) error {
	tx, err := s.begin(ctx, "record batch terminal notification")
	if err != nil {
		return err
	}
	defer tx.Rollback()
	txCtx := database.WithActionExecutionTransaction(ctx, tx)
	switch strings.TrimSpace(status) {
	case "completed":
		err = s.records.CompleteRecordBatchJob(txCtx, job, now)
	case "failed":
		err = s.records.FailRecordBatchJob(txCtx, job, now)
	case "quarantined":
		err = s.records.QuarantineRecordBatchJob(txCtx, job, now)
	default:
		err = fmt.Errorf("record batch terminal status %q is invalid", status)
	}
	if err != nil {
		return err
	}
	event.WorkspaceID = job.WorkspaceID
	if err := s.notifications.InsertEventTx(txCtx, tx, event); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit record batch terminal notification: %w", err)
	}
	s.notifications.PublishCommittedEventWakeup(event)
	return nil
}

func (s RecordBatchNotificationCommitter) CancelRecordBatchJob(ctx context.Context, workspaceID, jobID string, event notificationmodel.NotificationEvent) (recordmodel.RecordBatchJob, bool, bool, error) {
	tx, err := s.begin(ctx, "record batch cancellation notification")
	if err != nil {
		return recordmodel.RecordBatchJob{}, false, false, err
	}
	defer tx.Rollback()
	txCtx := database.WithActionExecutionTransaction(ctx, tx)
	current, found, err := s.records.GetRecordBatchJob(txCtx, workspaceID, jobID)
	if err != nil || !found {
		return recordmodel.RecordBatchJob{}, found, false, err
	}
	if current.Status == "cancelled" || current.Status == "completed" {
		return current, true, false, nil
	}
	updated, found, err := s.records.CancelRecordBatchJob(txCtx, workspaceID, jobID)
	if err != nil || !found || updated.Status != "cancelled" {
		return updated, found, false, err
	}
	event.WorkspaceID = updated.WorkspaceID
	if err := s.notifications.InsertEventTx(txCtx, tx, event); err != nil {
		return recordmodel.RecordBatchJob{}, true, false, err
	}
	if err := tx.Commit(); err != nil {
		return recordmodel.RecordBatchJob{}, true, false, fmt.Errorf("commit record batch cancellation notification: %w", err)
	}
	s.notifications.PublishCommittedEventWakeup(event)
	return updated, true, true, nil
}

func (s RecordBatchNotificationCommitter) begin(ctx context.Context, operation string) (*sql.Tx, error) {
	tx, err := s.store.DB().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return nil, fmt.Errorf("begin %s: %w", operation, err)
	}
	return tx, nil
}
