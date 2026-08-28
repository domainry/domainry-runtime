package notificationpublication

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	"github.com/domainry/domainry-notification-sdk/modulehost"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type Store struct{ runtime *database.RuntimeStore }

func NewStore(store *database.RuntimeStore) Store { return Store{runtime: store} }

func (s Store) InsertIntentTx(ctx context.Context, executor modulehost.Executor, intent notificationmodel.NotificationIntent) error {
	if s.runtime == nil || executor == nil {
		return fmt.Errorf("Notification SaaS publication store is required")
	}
	scope, ok := s.runtime.NotificationSaaSPublications()
	if !ok {
		return fmt.Errorf("Notification SaaS publication boundary is not bound")
	}
	if strings.TrimSpace(intent.ID) == "" || strings.TrimSpace(intent.WorkspaceID) == "" || strings.TrimSpace(intent.SourceEventID) == "" {
		return fmt.Errorf("Notification SaaS publication identity is required")
	}
	if intent.WorkspaceID != scope.WorkspaceID {
		return fmt.Errorf("Notification SaaS publication workspace %q does not match bound workspace %q", intent.WorkspaceID, scope.WorkspaceID)
	}
	payload, err := json.Marshal(intent)
	if err != nil {
		return fmt.Errorf("encode Notification SaaS publication intent: %w", err)
	}
	digest := sha256.Sum256(payload)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	columns := []string{"id", "tenant_id", "workspace_id", "application_key", "source_event_id", "event_type", "intent_json", "request_fingerprint", "status", "attempt_count", "next_attempt_at", "last_attempt_at", "lease_owner", "lease_expires_at", "fencing_token", "remote_event_id", "last_error_code", "last_error", "terminal_at", "created_at", "updated_at"}
	values := []any{intent.ID, scope.TenantID, scope.WorkspaceID, scope.ApplicationKey, intent.SourceEventID, intent.EventType, string(payload), hex.EncodeToString(digest[:]), "queued", 0, "", "", "", "", 0, "", "", "", "", now, now}
	query := "INSERT INTO " + s.runtime.TableIdentifier("notification_publication_outbox") + " ("
	for index, column := range columns {
		if index > 0 {
			query += ","
		}
		query += s.runtime.Identifier(column)
	}
	query += ") VALUES ("
	for index := range columns {
		if index > 0 {
			query += ","
		}
		query += s.runtime.Placeholder(index + 1)
	}
	query += ")"
	if _, err := executor.ExecContext(ctx, query, values...); err != nil {
		return fmt.Errorf("insert Notification SaaS publication outbox: %w", err)
	}
	return nil
}
