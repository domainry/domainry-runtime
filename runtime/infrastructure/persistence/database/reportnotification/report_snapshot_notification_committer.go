package reportnotification

import (
	"context"
	"database/sql"
	"fmt"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	reportsdkpersistence "github.com/domainry/domainry-report-sdk/persistence"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	notificationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/notification"
)

type ReportSnapshotNotificationCommitter struct {
	store         *database.RuntimeStore
	snapshots     reportsdkpersistence.SnapshotRepository
	notifications notificationpersistence.InboxEventWriter
	commitTx      func(*sql.Tx) error
}

func NewReportSnapshotNotificationCommitter(store *database.RuntimeStore, snapshots reportsdkpersistence.SnapshotRepository) ReportSnapshotNotificationCommitter {
	return ReportSnapshotNotificationCommitter{
		store: store, snapshots: snapshots, notifications: notificationpersistence.NewInboxEventWriter(store),
	}
}

func (s ReportSnapshotNotificationCommitter) CompleteReportSnapshotWithNotification(ctx context.Context, request reportsdkpersistence.SnapshotCompleteRequest, event notificationmodel.NotificationEvent) error {
	return s.commit(ctx, event, func(txCtx context.Context) error {
		return s.snapshots.Complete(txCtx, request)
	})
}

func (s ReportSnapshotNotificationCommitter) FailReportSnapshotWithNotification(ctx context.Context, request reportsdkpersistence.SnapshotFailRequest, event notificationmodel.NotificationEvent) error {
	return s.commit(ctx, event, func(txCtx context.Context) error {
		return s.snapshots.Fail(txCtx, request)
	})
}

func (s ReportSnapshotNotificationCommitter) commit(ctx context.Context, event notificationmodel.NotificationEvent, update func(context.Context) error) error {
	tx, err := s.store.DB().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin report snapshot notification: %w", err)
	}
	defer tx.Rollback()
	txCtx := database.WithActionExecutionTransaction(ctx, tx)
	if err := update(txCtx); err != nil {
		return err
	}
	if err := s.notifications.InsertEventTx(txCtx, tx, event); err != nil {
		return err
	}
	if err := s.commitTransaction(tx); err != nil {
		return fmt.Errorf("commit report snapshot notification: %w", err)
	}
	s.notifications.PublishCommittedEventWakeup(event)
	return nil
}

func (s ReportSnapshotNotificationCommitter) commitTransaction(tx *sql.Tx) error {
	if s.commitTx != nil {
		return s.commitTx(tx)
	}
	return tx.Commit()
}
