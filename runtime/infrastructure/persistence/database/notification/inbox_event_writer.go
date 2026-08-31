package notification

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/domainry/domainry-foundation/mutation"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	sdkcontract "github.com/domainry/domainry-notification-sdk/contract"
	"github.com/domainry/domainry-notification-sdk/modulehost"
	"github.com/domainry/domainry-orm/query"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	transactioncontract "github.com/domainry/domainry-runtime/runtime/domain/transaction/contract"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	notificationpublication "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/notificationpublication"
)

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
		if err := notificationpublication.NewPublicationOutboxStore(w.runtimeStore).InsertIntentTx(ctx, executor, *value.PublicationIntent); err != nil {
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
		queryValue, args, err := query.NewWorkspaceSelectBuilder(w.runtimeStore.SQLRenderer, "_publication_outbox", event.WorkspaceID).
			Projections(query.Project(query.CountAll())).Where(query.And(
			query.Equal("publication_type", "notification.saas"),
			query.Equal("tenant_id", scope.TenantID),
			query.Equal("application_key", scope.ApplicationKey), query.Equal("source_event_id", event.SourceEventID),
		)).Build()
		if err != nil {
			return 0, fmt.Errorf("build Notification publication commit inspection: %w", err)
		}
		err = w.runtimeStore.DB().QueryRowContext(ctx, queryValue, args...).Scan(&count)
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
