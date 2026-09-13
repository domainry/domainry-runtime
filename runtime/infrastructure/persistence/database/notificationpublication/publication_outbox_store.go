package notificationpublication

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	"github.com/domainry/domainry-notification-sdk/modulehost"
	"github.com/domainry/domainry-orm/query"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type PublicationOutboxStore struct{ runtime *database.RuntimeStore }

func NewPublicationOutboxStore(store *database.RuntimeStore) PublicationOutboxStore {
	return PublicationOutboxStore{runtime: store}
}

func (s PublicationOutboxStore) InsertIntentTx(ctx context.Context, executor modulehost.Executor, intent notificationmodel.NotificationIntent) error {
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
	queryer, ok := executor.(interface {
		QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	})
	if !ok {
		return fmt.Errorf("Notification publication executor cannot check account erasure fences")
	}
	for _, subject := range intent.RecipientUserIDs {
		if err := s.runtime.GuardSubjectEvidenceWrite(ctx, queryer, scope.WorkspaceID, "_publication_outbox", []string{"id", "user_id"}, []any{intent.ID, subject}); err != nil {
			return err
		}
	}
	if err := s.runtime.GuardSubjectEvidenceWrite(ctx, queryer, scope.WorkspaceID, "_publication_outbox", []string{"id", "object_key", "record_id"}, []any{intent.ID, intent.SubjectType, intent.SubjectID}); err != nil {
		return err
	}
	payload, err := json.Marshal(intent)
	if err != nil {
		return fmt.Errorf("encode Notification SaaS publication intent: %w", err)
	}
	digest := sha256.Sum256(payload)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	queryValue, args, buildErr := query.NewWorkspaceInsertBuilder(s.runtime.SQLRenderer, "_publication_outbox", scope.WorkspaceID).
		Columns("id", "publication_type", "application_key", "source_event_id", "event_type", "intent_json", "connector_key", "operation", "payload_json", "dedup_key", "request_fingerprint", "status", "attempt_count", "next_attempt_at", "last_attempt_at", "lease_owner", "lease_expires_at", "fencing_token", "remote_event_id", "last_error_code", "last_error", "terminal_at", "created_at", "updated_at").
		Values(intent.ID, "notification.saas", scope.ApplicationKey, intent.SourceEventID, intent.EventType, string(payload), "notification", "publish_intent", string(payload), intent.SourceEventID, hex.EncodeToString(digest[:]), "queued", 0, "", "", "", "", 0, "", "", "", "", now, now).Build()
	if buildErr != nil {
		return fmt.Errorf("build Notification SaaS publication outbox insert: %w", buildErr)
	}
	if _, err := executor.ExecContext(ctx, queryValue, args...); err != nil {
		return fmt.Errorf("insert Notification SaaS publication outbox: %w", err)
	}
	return nil
}
