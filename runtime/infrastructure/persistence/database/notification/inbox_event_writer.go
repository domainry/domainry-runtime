package notification

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	sdkcontract "github.com/domainry/domainry-notification-sdk/contract"
	notificationsql "github.com/domainry/domainry-notification/sqlstore"
	notificationcontract "github.com/domainry/domainry-runtime/runtime/domain/notification/contract"
	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	transactioncontract "github.com/domainry/domainry-runtime/runtime/domain/transaction/contract"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/mutation"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

// InboxEventWriter writes an already compiled module event through a
// producer-owned transaction. It does not compile intents or own commits.
type InboxEventWriter struct{ runtimeStore *database.RuntimeStore }

func NewInboxEventWriter(store *database.RuntimeStore) InboxEventWriter {
	return InboxEventWriter{runtimeStore: store}
}

func (w InboxEventWriter) InsertEventTx(ctx context.Context, executor notificationsql.Executor, value notificationmodel.NotificationEvent) error {
	if transactions := w.runtimeStore.NotificationTransactions(); transactions != nil {
		encoded, err := json.Marshal(value)
		if err != nil {
			return fmt.Errorf("encode notification event transaction boundary: %w", err)
		}
		var event sdkcontract.NotificationEvent
		if err := json.Unmarshal(encoded, &event); err != nil {
			return fmt.Errorf("decode notification event transaction boundary: %w", err)
		}
		if err := transactions.InsertEvent(ctx, executor, event); err != nil {
			return fmt.Errorf("insert notification inbox event: %w", database.MutationConstraintError(err, "notification_event", value.Source+"/"+value.SourceEventID, mutation.MutationConflictUnique))
		}
		if transactioncontract.ActiveTransaction(ctx) {
			return w.registerAfterCommit(ctx, value)
		}
		return nil
	}
	moduleStore, err := NewSQLStoreAdapter(w.runtimeStore, inboxEventWriterClock{})
	if err != nil {
		return err
	}
	if err = moduleStore.InsertEvent(ctx, executor, notificationcontract.ModuleInboxEvent(value)); err != nil {
		return fmt.Errorf("insert notification inbox event: %w", database.MutationConstraintError(err, "notification_event", value.Source+"/"+value.SourceEventID, mutation.MutationConflictUnique))
	}
	if transactioncontract.ActiveTransaction(ctx) {
		return w.registerAfterCommit(ctx, value)
	}
	return nil
}

func (w InboxEventWriter) registerAfterCommit(ctx context.Context, event notificationmodel.NotificationEvent) error {
	return transactioncontract.RegisterAfterCommit(ctx, transactioncontract.AfterCommitHook{Name: "wake notification inbox " + event.ID, Purpose: transactioncontract.AfterCommitDurableWorkWakeup, DurableRecovery: true, Run: func(context.Context) error { w.PublishCommittedEventWakeup(event); return nil }})
}

func (w InboxEventWriter) PublishCommittedEventWakeup(event notificationmodel.NotificationEvent) {
	workspace, err := principalmodel.NewWorkspaceID(event.WorkspaceID)
	if w.runtimeStore == nil || err != nil || strings.TrimSpace(event.ID) == "" {
		return
	}
	w.runtimeStore.WorkerWakeups().Publish(workerplatform.DurableTaskLocator{QueueKind: "notification_inbox", WorkspaceID: workspace.String(), TaskID: strings.TrimSpace(event.ID)})
}

type inboxEventWriterClock struct{}

func (inboxEventWriterClock) Now() time.Time { return time.Now().UTC() }
