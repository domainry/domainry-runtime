package notificationsdkfixture

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/domainry/domainry-notification-sdk/contract"
	"github.com/domainry/domainry-notification-sdk/modulehost"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

// BindTransactions installs a small SDK transaction fixture for persistence
// tests that exercise Runtime-owned atomic commits without assembling a full
// Notification Module Binding.
func BindTransactions(store *database.RuntimeStore) error {
	if store == nil {
		return fmt.Errorf("Runtime store is required")
	}
	return store.BindNotificationTransactions(transactions{store: store})
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
	_, err = executor.ExecContext(ctx, t.store.InsertStatement("notification_events", columns), event.ID, event.WorkspaceID, event.Source, event.SourceEventID, event.Status, string(raw), event.AttemptCount, event.NextAttemptAt, event.LastErrorCode, event.LeaseOwner, event.LeaseExpiresAt, event.FencingToken, event.OccurredAt, event.CreatedAt, event.UpdatedAt)
	return err
}
