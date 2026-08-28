package notification

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	sdkcontract "github.com/domainry/domainry-notification-sdk/contract"
	"github.com/domainry/domainry-notification-sdk/modulehost"
	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	transactioncontract "github.com/domainry/domainry-runtime/runtime/domain/transaction/contract"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	notificationpublication "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/notificationpublication"
	"github.com/domainry/domainry-runtime/runtime/platform/mutation"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

// InboxEventWriter writes an already compiled module event through a
// producer-owned transaction. It does not compile intents or own commits.
type InboxEventWriter struct{ runtimeStore *database.RuntimeStore }

func NewInboxEventWriter(store *database.RuntimeStore) InboxEventWriter {
	return InboxEventWriter{runtimeStore: store}
}

func (w InboxEventWriter) InsertEventTx(ctx context.Context, executor modulehost.Executor, value notificationmodel.NotificationEvent) error {
	if w.runtimeStore == nil {
		return fmt.Errorf("Notification transaction binding is unavailable")
	}
	if _, saas := w.runtimeStore.NotificationSaaSPublications(); saas {
		if value.PublicationIntent == nil {
			return fmt.Errorf("Notification SaaS publication intent is unavailable for event %q", value.ID)
		}
		if err := notificationpublication.NewStore(w.runtimeStore).InsertIntentTx(ctx, executor, *value.PublicationIntent); err != nil {
			return err
		}
		if transactioncontract.ActiveTransaction(ctx) {
			return w.registerSaaSAfterCommit(ctx, *value.PublicationIntent)
		}
		return nil
	}
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
	return fmt.Errorf("Notification transaction topology is not bound")
}

func (w InboxEventWriter) registerSaaSAfterCommit(ctx context.Context, intent notificationmodel.NotificationIntent) error {
	return transactioncontract.RegisterAfterCommit(ctx, transactioncontract.AfterCommitHook{Name: "wake Notification SaaS publication " + intent.ID, Purpose: transactioncontract.AfterCommitDurableWorkWakeup, DurableRecovery: true, Run: func(context.Context) error {
		workspace, err := principalmodel.NewWorkspaceID(intent.WorkspaceID)
		if err == nil && w.runtimeStore != nil {
			w.runtimeStore.WorkerWakeups().Publish(workerplatform.DurableTaskLocator{QueueKind: "notification_publication", WorkspaceID: workspace.String(), TaskID: strings.TrimSpace(intent.ID)})
		}
		return nil
	}})
}

func (w InboxEventWriter) registerAfterCommit(ctx context.Context, event notificationmodel.NotificationEvent) error {
	return transactioncontract.RegisterAfterCommit(ctx, transactioncontract.AfterCommitHook{Name: "wake notification inbox " + event.ID, Purpose: transactioncontract.AfterCommitDurableWorkWakeup, DurableRecovery: true, Run: func(context.Context) error { w.PublishCommittedEventWakeup(event); return nil }})
}

func (w InboxEventWriter) PublishCommittedEventWakeup(event notificationmodel.NotificationEvent) {
	workspace, err := principalmodel.NewWorkspaceID(event.WorkspaceID)
	if w.runtimeStore == nil || err != nil || strings.TrimSpace(event.ID) == "" {
		return
	}
	queue := "notification_inbox"
	if _, saas := w.runtimeStore.NotificationSaaSPublications(); saas {
		queue = "notification_publication"
	}
	w.runtimeStore.WorkerWakeups().Publish(workerplatform.DurableTaskLocator{QueueKind: queue, WorkspaceID: workspace.String(), TaskID: strings.TrimSpace(event.ID)})
}

func (w InboxEventWriter) CommittedCount(ctx context.Context, event notificationmodel.NotificationEvent) (int, error) {
	if w.runtimeStore == nil {
		return 0, fmt.Errorf("Notification publication store is required")
	}
	var count int
	if scope, saas := w.runtimeStore.NotificationSaaSPublications(); saas {
		query := "SELECT COUNT(*) FROM " + w.runtimeStore.TableIdentifier("notification_publication_outbox") + " WHERE " + w.runtimeStore.Identifier("tenant_id") + " = " + w.runtimeStore.Placeholder(1) + " AND " + w.runtimeStore.Identifier("workspace_id") + " = " + w.runtimeStore.Placeholder(2) + " AND " + w.runtimeStore.Identifier("application_key") + " = " + w.runtimeStore.Placeholder(3) + " AND " + w.runtimeStore.Identifier("source_event_id") + " = " + w.runtimeStore.Placeholder(4)
		err := w.runtimeStore.DB().QueryRowContext(ctx, query, scope.TenantID, event.WorkspaceID, scope.ApplicationKey, event.SourceEventID).Scan(&count)
		return count, err
	}
	transactions := w.runtimeStore.NotificationTransactions()
	if transactions == nil {
		return 0, fmt.Errorf("Notification transaction topology is not bound")
	}
	committed, err := transactions.EventCommitted(ctx, modulehost.EventIdentity{WorkspaceID: event.WorkspaceID, Source: event.Source, SourceEventID: event.SourceEventID})
	if err != nil || !committed {
		return 0, err
	}
	return 1, nil
}
