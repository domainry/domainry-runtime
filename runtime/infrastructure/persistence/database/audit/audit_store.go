// Audit persistence.
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type AuditStore struct {
	store *database.RuntimeStore
	db    *sql.DB
}

func NewAuditStore(store *database.RuntimeStore) AuditStore {
	return AuditStore{store: store, db: store.DB()}
}

func (r AuditStore) executor(ctx context.Context) database.ActionExecutionExecutor {
	if transaction := database.ActionExecutionTransaction(ctx); transaction != nil {
		return transaction
	}
	return r.db
}

func (r AuditStore) InsertAuditEvent(ctx context.Context, workspaceID string, event auditmodel.AuditEvent) error {
	workspace, err := principalmodel.NewWorkspaceID(workspaceID)
	if err != nil {
		return err
	}
	workspaceID = workspace.String()
	if strings.TrimSpace(event.WorkspaceID) != workspaceID {
		return fmt.Errorf("audit event workspace %q does not match repository workspace %q", event.WorkspaceID, workspaceID)
	}
	beforeJSON, err := json.Marshal(event.Before)
	if err != nil {
		return fmt.Errorf("encode audit before: %w", err)
	}
	afterJSON, err := json.Marshal(event.After)
	if err != nil {
		return fmt.Errorf("encode audit after: %w", err)
	}
	metadataJSON, err := json.Marshal(event.Metadata)
	if err != nil {
		return fmt.Errorf("encode audit metadata: %w", err)
	}
	columns := []string{"id", "event", "object_key", "record_id", "actor_id", "role_key", "summary", "metadata_json", "before_json", "after_json", "created_at"}
	statement, args, buildErr := ormbuilder.NewWorkspaceInsertBuilder(r.store.SQLRenderer, "_audit_events", workspaceID).Columns(columns...).Values(
		event.ID, event.Event, event.ObjectKey, event.RecordID, event.ActorID, event.RoleKey, event.Summary,
		string(metadataJSON), string(beforeJSON), string(afterJSON), event.CreatedAt,
	).Build()
	if buildErr != nil {
		return fmt.Errorf("build audit event insert: %w", buildErr)
	}
	_, err = r.executor(ctx).ExecContext(ctx, statement, args...)
	if err != nil {
		// A deterministic idempotent event may race or be replayed after the
		// business mutation committed. Only the exact previously stored payload
		// is accepted; the same key with different facts remains a hard error.
		var storedEvent, objectKey, recordID, actorID, roleKey, summary, storedMetadata, storedBefore, storedAfter string
		lookup, lookupArgs, lookupBuildErr := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "_audit_events", workspaceID).
			Columns("event", "object_key", "record_id", "actor_id", "role_key", "summary", "metadata_json", "before_json", "after_json").
			Where(ormbuilder.Equal("id", event.ID)).Limit(1).Build()
		if lookupBuildErr != nil {
			return fmt.Errorf("build audit event replay lookup: %w", lookupBuildErr)
		}
		lookupErr := r.executor(ctx).QueryRowContext(ctx, lookup, lookupArgs...).Scan(&storedEvent, &objectKey, &recordID, &actorID, &roleKey, &summary, &storedMetadata, &storedBefore, &storedAfter)
		if lookupErr == nil && storedEvent == event.Event && objectKey == event.ObjectKey && recordID == event.RecordID && actorID == event.ActorID && roleKey == event.RoleKey && summary == event.Summary && storedMetadata == string(metadataJSON) && storedBefore == string(beforeJSON) && storedAfter == string(afterJSON) {
			return nil
		}
		return fmt.Errorf("insert audit event: %w", err)
	}
	return nil
}

func escapeSQLLike(value string) string {
	value = strings.ReplaceAll(value, "~", "~~")
	value = strings.ReplaceAll(value, "%", "~%")
	return strings.ReplaceAll(value, "_", "~_")
}

func auditEventClassExpression() ormbuilder.Expression {
	return ormbuilder.Lower(ormbuilder.Concat(
		ormbuilder.Coalesce(ormbuilder.Column("event"), ormbuilder.Value("")),
		ormbuilder.Value(" "),
		ormbuilder.Coalesce(ormbuilder.Column("object_key"), ormbuilder.Value("")),
	))
}

func (r AuditStore) ListAuditEvents(ctx context.Context, workspaceID string, query auditmodel.AuditEventQuery) ([]auditmodel.AuditEvent, error) {
	workspace, err := principalmodel.NewWorkspaceID(workspaceID)
	if err != nil {
		return nil, err
	}
	return r.listAuditEvents(ctx, workspace.String(), true, query)
}

func (r AuditStore) ListAuditEventsForSystem(ctx context.Context, scope principalmodel.SystemScope, query auditmodel.AuditEventQuery) ([]auditmodel.AuditEvent, error) {
	if _, err := principalmodel.NewSystemQueryScope(scope); err != nil {
		return nil, err
	}
	return r.listAuditEvents(ctx, "", false, query)
}

func (r AuditStore) listAuditEvents(ctx context.Context, workspaceID string, scoped bool, query auditmodel.AuditEventQuery) ([]auditmodel.AuditEvent, error) {
	limit := query.Limit
	if limit <= 0 {
		limit = 100
	} else if limit > 1000 {
		limit = 1000
	}
	predicates := make([]ormbuilder.Predicate, 0, 12)
	addEqual := func(column, value string) {
		if value = strings.TrimSpace(value); value != "" {
			predicates = append(predicates, ormbuilder.Equal(column, value))
		}
	}
	addEqual("object_key", query.ObjectKey)
	addEqual("record_id", query.RecordID)
	addEqual("event", query.Event)
	addEqual("actor_id", query.ActorID)
	addEqual("role_key", query.RoleKey)
	eventClassValue := auditEventClassExpression()
	switch strings.TrimSpace(query.Class) {
	case auditmodel.AuditEventClassOperations:
		predicates = append(predicates, auditClassMarkerPredicate(eventClassValue, auditmodel.AuditEventClassMarkers(auditmodel.AuditEventClassOperations)))
	case auditmodel.AuditEventClassGovernance:
		operations := auditClassMarkerPredicate(eventClassValue, auditmodel.AuditEventClassMarkers(auditmodel.AuditEventClassOperations))
		governance := auditClassMarkerPredicate(eventClassValue, auditmodel.AuditEventClassMarkers(auditmodel.AuditEventClassGovernance))
		predicates = append(predicates, ormbuilder.And(ormbuilder.Not(operations), governance))
	case auditmodel.AuditEventClassBusiness:
		operations := auditClassMarkerPredicate(eventClassValue, auditmodel.AuditEventClassMarkers(auditmodel.AuditEventClassOperations))
		governance := auditClassMarkerPredicate(eventClassValue, auditmodel.AuditEventClassMarkers(auditmodel.AuditEventClassGovernance))
		predicates = append(predicates, ormbuilder.And(ormbuilder.Not(operations), ormbuilder.Not(governance)))
	}
	if value := strings.TrimSpace(query.CreatedFrom); value != "" {
		predicates = append(predicates, ormbuilder.GreaterThanOrEqual("created_at", value))
	}
	if value := strings.TrimSpace(query.CreatedTo); value != "" {
		predicates = append(predicates, ormbuilder.LessThanOrEqual("created_at", value))
	}
	if value := strings.TrimSpace(query.RequestID); value != "" {
		encodedRequestID, _ := json.Marshal(value)
		predicates = append(predicates, ormbuilder.LikeEscaped("metadata_json", "%"+escapeSQLLike(`"request_id":`+string(encodedRequestID))+"%"))
	}
	if value := strings.TrimSpace(query.Cursor); value != "" {
		cursor, err := auditmodel.DecodeAuditEventCursor(value)
		if err != nil {
			return nil, fmt.Errorf("decode audit event cursor: %w", err)
		}
		predicates = append(predicates, ormbuilder.Or(
			ormbuilder.LessThan("created_at", cursor.CreatedAt),
			ormbuilder.And(ormbuilder.Equal("created_at", cursor.CreatedAt), ormbuilder.LessThan("id", cursor.ID)),
		))
	}
	columns := []string{"id", "workspace_id", "event", "object_key", "record_id", "actor_id", "role_key", "summary", "metadata_json", "before_json", "after_json", "created_at"}
	var builder *ormbuilder.SelectBuilder
	if scoped {
		builder = ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "_audit_events", workspaceID).Columns(columns...)
	} else {
		builder = ormbuilder.NewSelectBuilder(r.store.SQLRenderer, "_audit_events").Columns(columns...)
	}
	if len(predicates) > 0 {
		builder.Where(ormbuilder.And(predicates...))
	}
	statement, args, buildErr := builder.OrderBy(ormbuilder.Descending("created_at"), ormbuilder.Descending("id")).Limit(limit).Build()
	if buildErr != nil {
		return nil, fmt.Errorf("build audit event list: %w", buildErr)
	}
	rows, err := r.executor(ctx).QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, fmt.Errorf("list audit events: %w", err)
	}
	defer rows.Close()
	events := []auditmodel.AuditEvent{}
	for rows.Next() {
		var event auditmodel.AuditEvent
		var metadataJSON, beforeJSON, afterJSON string
		if err := rows.Scan(&event.ID, &event.WorkspaceID, &event.Event, &event.ObjectKey, &event.RecordID, &event.ActorID, &event.RoleKey, &event.Summary, &metadataJSON, &beforeJSON, &afterJSON, &event.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan audit event: %w", err)
		}
		_ = json.Unmarshal([]byte(metadataJSON), &event.Metadata)
		_ = json.Unmarshal([]byte(beforeJSON), &event.Before)
		_ = json.Unmarshal([]byte(afterJSON), &event.After)
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read audit events: %w", err)
	}
	return events, nil
}

func auditClassMarkerPredicate(eventClassValue ormbuilder.Expression, markers []string) ormbuilder.Predicate {
	predicates := make([]ormbuilder.Predicate, 0, len(markers))
	for _, marker := range markers {
		predicates = append(predicates, ormbuilder.LikeValueEscaped(eventClassValue, "%"+escapeSQLLike(strings.ToLower(marker))+"%"))
	}
	if len(predicates) == 0 {
		return ormbuilder.AlwaysFalse()
	}
	return ormbuilder.Or(predicates...)
}

func (r AuditStore) ListAuditOptions(ctx context.Context, workspaceID string, query auditmodel.AuditOptionQuery) ([]auditmodel.AuditOption, error) {
	workspace, err := principalmodel.NewWorkspaceID(workspaceID)
	if err != nil {
		return nil, err
	}
	workspaceID = workspace.String()
	field := strings.TrimSpace(query.Field)
	switch field {
	case "record_id", "actor_id", "role_key", "event":
	default:
		return nil, fmt.Errorf("unsupported audit option field %q", field)
	}
	limit := query.Limit
	if limit <= 0 {
		limit = 10
	} else if limit > 50 {
		limit = 50
	}
	predicates := []ormbuilder.Predicate{ormbuilder.NotEqual(field, "")}
	if value := strings.TrimSpace(query.ObjectKey); value != "" {
		predicates = append(predicates, ormbuilder.Equal("object_key", value))
	}
	if value := strings.TrimSpace(query.CreatedFrom); value != "" {
		predicates = append(predicates, ormbuilder.GreaterThanOrEqual("created_at", value))
	}
	if value := strings.TrimSpace(query.CreatedTo); value != "" {
		predicates = append(predicates, ormbuilder.LessThanOrEqual("created_at", value))
	}
	if value := strings.TrimSpace(query.Query); value != "" {
		predicates = append(predicates, ormbuilder.LikeEscaped(field, "%"+escapeSQLLike(value)+"%"))
	}
	count := ormbuilder.CountAll()
	statement, args, buildErr := ormbuilder.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "_audit_events", workspaceID).
		Projections(ormbuilder.Project(ormbuilder.Column(field)), ormbuilder.Project(count)).
		Where(ormbuilder.And(predicates...)).GroupBy(ormbuilder.Column(field)).
		OrderBy(ormbuilder.DescendingExpression(count), ormbuilder.Ascending(field)).Limit(limit).Build()
	if buildErr != nil {
		return nil, fmt.Errorf("build audit option list: %w", buildErr)
	}
	rows, err := r.executor(ctx).QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, fmt.Errorf("list audit options: %w", err)
	}
	defer rows.Close()
	options := []auditmodel.AuditOption{}
	for rows.Next() {
		var option auditmodel.AuditOption
		if err := rows.Scan(&option.Value, &option.Count); err != nil {
			return nil, fmt.Errorf("scan audit option: %w", err)
		}
		option.Label = option.Value
		options = append(options, option)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read audit options: %w", err)
	}
	return options, nil
}
