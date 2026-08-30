package notificationsdkfixture

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/domainry/domainry-notification-sdk/contract"
	"github.com/domainry/domainry-notification-sdk/modulehost"
	notificationmodule "github.com/domainry/domainry-notification/module"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

// BindTransactions installs a small SDK transaction fixture for persistence
// tests that exercise Runtime-owned atomic commits without assembling a full
// Notification Module Binding.
func BindTransactions(store *database.RuntimeStore) error {
	if store == nil {
		return fmt.Errorf("Runtime store is required")
	}
	if err := EnsureModuleSchema(context.Background(), store); err != nil {
		return err
	}
	return store.BindNotificationTransactions(transactions{store: store})
}

// EnsureModuleSchema installs the source-owned Notification migration in a
// persistence-test store without assembling the full application Binding.
func EnsureModuleSchema(ctx context.Context, store *database.RuntimeStore) error {
	if store == nil {
		return fmt.Errorf("Runtime store is required")
	}
	migrations, err := notificationmodule.SchemaMigrations(store.Driver(), store.DatabaseSchema(), "")
	if err != nil {
		return err
	}
	return store.ApplyOwnedMigrations(ctx, "notification", migrations)
}

type transactions struct{ store *database.RuntimeStore }

func (transactions) CompileIntent(contract.NotificationIntent) (contract.NotificationEvent, error) {
	return contract.NotificationEvent{}, fmt.Errorf("test transaction compiler is unavailable")
}

func (t transactions) InsertEvent(ctx context.Context, executor modulehost.Executor, event contract.NotificationEvent) error {
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	columns := []string{"id", "workspace_id", "source", "source_event_id", "status", "payload_json", "attempt_count", "next_attempt_at", "last_error_code", "lease_owner", "lease_expires_at", "fencing_token", "occurred_at", "created_at", "updated_at"}
	_, err = executor.ExecContext(ctx, t.store.InsertStatement("_notification_events", columns), event.ID, event.WorkspaceID, event.Source, event.SourceEventID, event.Status, string(raw), event.AttemptCount, event.NextAttemptAt, event.LastErrorCode, event.LeaseOwner, event.LeaseExpiresAt, event.FencingToken, event.OccurredAt, event.CreatedAt, event.UpdatedAt)
	return err
}

func (t transactions) EventCommitted(ctx context.Context, identity modulehost.EventIdentity) (bool, error) {
	query := "SELECT COUNT(*) FROM " + t.store.TableIdentifier("_notification_events") + " WHERE " + t.store.Identifier("workspace_id") + " = " + t.store.Placeholder(1) + " AND " + t.store.Identifier("source") + " = " + t.store.Placeholder(2) + " AND " + t.store.Identifier("source_event_id") + " = " + t.store.Placeholder(3)
	var count int
	if err := t.store.DB().QueryRowContext(ctx, query, identity.WorkspaceID, identity.Source, identity.SourceEventID).Scan(&count); err != nil {
		return false, err
	}
	return count > 0, nil
}
