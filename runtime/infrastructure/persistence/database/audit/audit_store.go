// Audit persistence.
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

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
	columns := []string{"id", "workspace_id", "event", "object_key", "record_id", "actor_id", "role_key", "summary", "metadata_json", "before_json", "after_json", "created_at"}
	query := "INSERT INTO " + r.store.TableIdentifier("_audit_events") + " (" + strings.Join(quotedColumns(r.store, columns), ", ") + ") VALUES (" + strings.Join(placeholders(r.store, len(columns)), ", ") + ")"
	_, err = r.executor(ctx).ExecContext(ctx, query, event.ID, workspaceID, event.Event, event.ObjectKey, event.RecordID, event.ActorID, event.RoleKey, event.Summary, string(metadataJSON), string(beforeJSON), string(afterJSON), event.CreatedAt)
	if err != nil {
		// A deterministic idempotent event may race or be replayed after the
		// business mutation committed. Only the exact previously stored payload
		// is accepted; the same key with different facts remains a hard error.
		var storedEvent, objectKey, recordID, actorID, roleKey, summary, storedMetadata, storedBefore, storedAfter string
		lookup := "SELECT " + strings.Join(quotedColumns(r.store, []string{"event", "object_key", "record_id", "actor_id", "role_key", "summary", "metadata_json", "before_json", "after_json"}), ", ") + " FROM " + r.store.TableIdentifier("_audit_events") + " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(1) + " AND " + r.store.Identifier("id") + " = " + r.store.Placeholder(2)
		lookupErr := r.executor(ctx).QueryRowContext(ctx, lookup, workspaceID, event.ID).Scan(&storedEvent, &objectKey, &recordID, &actorID, &roleKey, &summary, &storedMetadata, &storedBefore, &storedAfter)
		if lookupErr == nil && storedEvent == event.Event && objectKey == event.ObjectKey && recordID == event.RecordID && actorID == event.ActorID && roleKey == event.RoleKey && summary == event.Summary && storedMetadata == string(metadataJSON) && storedBefore == string(beforeJSON) && storedAfter == string(afterJSON) {
			return nil
		}
		return fmt.Errorf("insert audit event: %w", err)
	}
	return nil
}

func quotedColumns(store *database.RuntimeStore, columns []string) []string {
	quoted := make([]string, 0, len(columns))
	for _, column := range columns {
		quoted = append(quoted, store.Identifier(column))
	}
	return quoted
}

func placeholders(store *database.RuntimeStore, count int) []string {
	values := make([]string, 0, count)
	for index := 0; index < count; index++ {
		values = append(values, store.Placeholder(index+1))
	}
	return values
}

func escapeSQLLike(value string) string {
	value = strings.ReplaceAll(value, "~", "~~")
	value = strings.ReplaceAll(value, "%", "~%")
	return strings.ReplaceAll(value, "_", "~_")
}

func (r AuditStore) eventClassValue() string {
	event := "COALESCE(" + r.store.Identifier("event") + ", '')"
	objectKey := "COALESCE(" + r.store.Identifier("object_key") + ", '')"
	if r.store.Driver() == "mysql" {
		return "LOWER(CONCAT(" + event + ", ' ', " + objectKey + "))"
	}
	return "LOWER(" + event + " || ' ' || " + objectKey + ")"
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
	clauses, args := []string{}, []any{}
	if scoped {
		clauses = append(clauses, r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(1))
		args = append(args, workspaceID)
	}
	addEqual := func(column, value string) {
		if value = strings.TrimSpace(value); value != "" {
			args = append(args, value)
			clauses = append(clauses, r.store.Identifier(column)+" = "+r.store.Placeholder(len(args)))
		}
	}
	addEqual("object_key", query.ObjectKey)
	addEqual("record_id", query.RecordID)
	addEqual("event", query.Event)
	addEqual("actor_id", query.ActorID)
	addEqual("role_key", query.RoleKey)
	eventClassValue := r.eventClassValue()
	switch strings.TrimSpace(query.Class) {
	case auditmodel.AuditEventClassOperations:
		clauses = append(clauses, r.classMarkerExpression(eventClassValue, auditmodel.AuditEventClassMarkers(auditmodel.AuditEventClassOperations), &args))
	case auditmodel.AuditEventClassGovernance:
		operations := r.classMarkerExpression(eventClassValue, auditmodel.AuditEventClassMarkers(auditmodel.AuditEventClassOperations), &args)
		governance := r.classMarkerExpression(eventClassValue, auditmodel.AuditEventClassMarkers(auditmodel.AuditEventClassGovernance), &args)
		clauses = append(clauses, "NOT "+operations+" AND "+governance)
	case auditmodel.AuditEventClassBusiness:
		operations := r.classMarkerExpression(eventClassValue, auditmodel.AuditEventClassMarkers(auditmodel.AuditEventClassOperations), &args)
		governance := r.classMarkerExpression(eventClassValue, auditmodel.AuditEventClassMarkers(auditmodel.AuditEventClassGovernance), &args)
		clauses = append(clauses, "NOT "+operations+" AND NOT "+governance)
	}
	if value := strings.TrimSpace(query.CreatedFrom); value != "" {
		args = append(args, value)
		clauses = append(clauses, r.store.Identifier("created_at")+" >= "+r.store.Placeholder(len(args)))
	}
	if value := strings.TrimSpace(query.CreatedTo); value != "" {
		args = append(args, value)
		clauses = append(clauses, r.store.Identifier("created_at")+" <= "+r.store.Placeholder(len(args)))
	}
	if value := strings.TrimSpace(query.RequestID); value != "" {
		encodedRequestID, _ := json.Marshal(value)
		args = append(args, "%"+escapeSQLLike(`"request_id":`+string(encodedRequestID))+"%")
		clauses = append(clauses, r.store.Identifier("metadata_json")+" LIKE "+r.store.Placeholder(len(args))+" ESCAPE '~'")
	}
	if value := strings.TrimSpace(query.Cursor); value != "" {
		cursor, err := auditmodel.DecodeAuditEventCursor(value)
		if err != nil {
			return nil, fmt.Errorf("decode audit event cursor: %w", err)
		}
		args = append(args, cursor.CreatedAt, cursor.CreatedAt, cursor.ID)
		createdAtFirst := r.store.Placeholder(len(args) - 2)
		createdAtEqual := r.store.Placeholder(len(args) - 1)
		idBefore := r.store.Placeholder(len(args))
		clauses = append(clauses, "("+r.store.Identifier("created_at")+" < "+createdAtFirst+" OR ("+r.store.Identifier("created_at")+" = "+createdAtEqual+" AND "+r.store.Identifier("id")+" < "+idBefore+"))")
	}
	whereSQL := ""
	if len(clauses) > 0 {
		whereSQL = " WHERE " + strings.Join(clauses, " AND ")
	}
	args = append(args, limit)
	columns := []string{"id", "workspace_id", "event", "object_key", "record_id", "actor_id", "role_key", "summary", "metadata_json", "before_json", "after_json", "created_at"}
	rows, err := r.executor(ctx).QueryContext(ctx, "SELECT "+strings.Join(quotedColumns(r.store, columns), ", ")+" FROM "+r.store.TableIdentifier("_audit_events")+whereSQL+" ORDER BY "+r.store.Identifier("created_at")+" DESC, "+r.store.Identifier("id")+" DESC LIMIT "+r.store.Placeholder(len(args)), args...)
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

func (r AuditStore) classMarkerExpression(eventClassValue string, markers []string, args *[]any) string {
	parts := make([]string, 0, len(markers))
	for _, marker := range markers {
		*args = append(*args, "%"+escapeSQLLike(strings.ToLower(marker))+"%")
		parts = append(parts, eventClassValue+" LIKE "+r.store.Placeholder(len(*args))+" ESCAPE '~'")
	}
	if len(parts) == 0 {
		return "0 = 1"
	}
	return "(" + strings.Join(parts, " OR ") + ")"
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
	clauses := []string{r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(1), r.store.Identifier(field) + " <> ''"}
	args := []any{workspaceID}
	for _, filter := range []struct{ column, value, operator string }{{"object_key", query.ObjectKey, " = "}, {"created_at", query.CreatedFrom, " >= "}, {"created_at", query.CreatedTo, " <= "}} {
		if value := strings.TrimSpace(filter.value); value != "" {
			args = append(args, value)
			clauses = append(clauses, r.store.Identifier(filter.column)+filter.operator+r.store.Placeholder(len(args)))
		}
	}
	if value := strings.TrimSpace(query.Query); value != "" {
		args = append(args, "%"+escapeSQLLike(value)+"%")
		clauses = append(clauses, r.store.Identifier(field)+" LIKE "+r.store.Placeholder(len(args))+" ESCAPE '~'")
	}
	args = append(args, limit)
	rows, err := r.executor(ctx).QueryContext(ctx, "SELECT "+r.store.Identifier(field)+", COUNT(*) FROM "+r.store.TableIdentifier("_audit_events")+" WHERE "+strings.Join(clauses, " AND ")+" GROUP BY "+r.store.Identifier(field)+" ORDER BY COUNT(*) DESC, "+r.store.Identifier(field)+" ASC LIMIT "+r.store.Placeholder(len(args)), args...)
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
