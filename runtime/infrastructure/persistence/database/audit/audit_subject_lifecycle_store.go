package audit

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"time"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type AuditSubjectLifecycleStore struct {
	db       *sql.DB
	renderer ormbuilder.Renderer
}

func NewAuditSubjectLifecycleStore(store *database.RuntimeStore) *AuditSubjectLifecycleStore {
	return &AuditSubjectLifecycleStore{db: store.DB(), renderer: store.SQLRenderer}
}

func (s *AuditSubjectLifecycleStore) Owner(context.Context) string { return "audit" }

func (s *AuditSubjectLifecycleStore) PreviewSubject(ctx context.Context, workspaceID, identity string) (json.RawMessage, error) {
	var count int64
	statement, args, err := ormbuilder.NewWorkspaceSelectBuilder(s.renderer, "_audit_events", workspaceID).
		Projections(ormbuilder.Project(ormbuilder.CountAll())).Where(ormbuilder.Equal("actor_id", identity)).Build()
	if err != nil {
		return nil, err
	}
	if err := s.db.QueryRowContext(ctx, statement, args...).Scan(&count); err != nil {
		return nil, err
	}
	return json.Marshal(map[string]int64{"audit_references": count})
}

func (s *AuditSubjectLifecycleStore) ExportSubject(ctx context.Context, workspaceID, identity string) (json.RawMessage, error) {
	columns := []string{"id", "event", "object_key", "record_id", "summary", "created_at"}
	projections := make([]ormbuilder.Projection, len(columns))
	for index, column := range columns {
		projections[index] = ormbuilder.Project(ormbuilder.Coalesce(ormbuilder.Column(column), ormbuilder.Value("")))
	}
	statement, args, err := ormbuilder.NewWorkspaceSelectBuilder(s.renderer, "_audit_events", workspaceID).
		Projections(projections...).Where(ormbuilder.Equal("actor_id", identity)).OrderBy(ormbuilder.Ascending("created_at")).Build()
	if err != nil {
		return nil, err
	}
	rows, err := s.db.QueryContext(ctx, statement, args...)
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
	statement, args, err := ormbuilder.NewWorkspaceUpdateBuilder(s.renderer, "_audit_events", workspaceID).
		Set("actor_id", anonymous).Where(ormbuilder.Equal("actor_id", identity)).Build()
	if err != nil {
		return nil, err
	}
	result, err := s.db.ExecContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]any{"anonymized_audit_references": changed, "event_integrity_preserved": true, "at": time.Now().UTC()})
}
