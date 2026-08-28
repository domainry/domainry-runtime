package audit

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"time"

	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
)

type lifecycleSQLStore interface {
	DB() *sql.DB
	Identifier(string) string
	TableIdentifier(string) string
	Placeholder(int) string
}

type AuditSubjectLifecycleStore struct{ store lifecycleSQLStore }

func NewAuditSubjectLifecycleStore(store lifecycleSQLStore) *AuditSubjectLifecycleStore {
	return &AuditSubjectLifecycleStore{store: store}
}

func (s *AuditSubjectLifecycleStore) Owner(context.Context) string { return "audit" }

func (s *AuditSubjectLifecycleStore) PreviewSubject(ctx context.Context, workspaceID, identity string) (json.RawMessage, error) {
	var count int64
	query := "SELECT COUNT(*) FROM " + s.store.TableIdentifier("_audit_events") + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("actor_id") + " = " + s.store.Placeholder(2)
	if err := s.store.DB().QueryRowContext(ctx, query, workspaceID, identity).Scan(&count); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]int64{"audit_references": count})
}

func (s *AuditSubjectLifecycleStore) ExportSubject(ctx context.Context, workspaceID, identity string) (json.RawMessage, error) {
	query := "SELECT " + auditLifecycleColumns(s.store, "id", "event", "object_key", "record_id", "summary", "created_at") + " FROM " + s.store.TableIdentifier("_audit_events") + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("actor_id") + " = " + s.store.Placeholder(2) + " ORDER BY " + s.store.Identifier("created_at")
	rows, err := s.store.DB().QueryContext(ctx, query, workspaceID, identity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []map[string]string{}
	for rows.Next() {
		var id, event, objectKey, recordID, summary, createdAt string
		if err := rows.Scan(&id, &event, &objectKey, &recordID, &summary, &createdAt); err != nil {
			return nil, err
		}
		items = append(items, map[string]string{"id": id, "event": event, "object_key": objectKey, "record_id": recordID, "summary": summary, "created_at": createdAt})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return json.Marshal(items)
}

func (s *AuditSubjectLifecycleStore) EraseSubject(ctx context.Context, workspaceID, identity string, _ []lifecyclemodel.LegalHold) (json.RawMessage, error) {
	sum := sha256.Sum256([]byte(workspaceID + "\x00" + identity))
	anonymous := "erased-" + hex.EncodeToString(sum[:12])
	query := "UPDATE " + s.store.TableIdentifier("_audit_events") + " SET " + s.store.Identifier("actor_id") + " = " + s.store.Placeholder(1) + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(2) + " AND " + s.store.Identifier("actor_id") + " = " + s.store.Placeholder(3)
	result, err := s.store.DB().ExecContext(ctx, query, anonymous, workspaceID, identity)
	if err != nil {
		return nil, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"anonymized_audit_references": changed, "event_integrity_preserved": true, "at": time.Now().UTC()})
}

func auditLifecycleColumns(store lifecycleSQLStore, columns ...string) string {
	result := ""
	for index, column := range columns {
		if index > 0 {
			result += ", "
		}
		result += "COALESCE(" + store.Identifier(column) + ", '')"
	}
	return result
}
