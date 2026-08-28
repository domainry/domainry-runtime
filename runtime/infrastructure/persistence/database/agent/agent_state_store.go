// Agent state persistence.
package agent

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"sync/atomic"

	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	runtimeschema "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/schema"
)

type AgentStateStore struct {
	store  *database.RuntimeStore
	db     *sql.DB
	schema runtimeschema.SQLDatabase
	driver string
	ready  *atomic.Bool
}

func NewAgentStateStore(store *database.RuntimeStore) AgentStateStore {
	return AgentStateStore{store: store, db: store.DB(), schema: store.SchemaDB(), driver: store.Driver(), ready: &atomic.Bool{}}
}

func (r AgentStateStore) EnsureSchema(ctx context.Context) error {
	if r.store == nil || r.db == nil || r.schema == nil || r.ready == nil {
		return fmt.Errorf("agent state store unavailable")
	}
	if r.ready.Load() {
		return nil
	}
	if err := r.ensureTable(ctx); err != nil {
		return err
	}
	r.ready.Store(true)
	return nil
}

func (r AgentStateStore) ensureTable(ctx context.Context) error {
	textType := "TEXT"
	if r.driver == "mysql" {
		textType = "VARCHAR(255)"
	}
	query := "CREATE TABLE IF NOT EXISTS " + r.store.TableIdentifier("agent_runtime_state") + " (" +
		r.store.Identifier("kind") + " " + textType + " NOT NULL, " +
		r.store.Identifier("state_key") + " " + textType + " NOT NULL, " +
		r.store.Identifier("workspace_id") + " " + textType + " NOT NULL, " +
		r.store.Identifier("user_id") + " " + textType + " NOT NULL, " +
		r.store.Identifier("role_key") + " " + textType + " NOT NULL, " +
		r.store.Identifier("payload_json") + " TEXT NOT NULL, " +
		r.store.Identifier("updated_at") + " BIGINT NOT NULL, PRIMARY KEY (" + r.store.Identifier("workspace_id") + ", " + r.store.Identifier("kind") + ", " + r.store.Identifier("state_key") + "))"
	_, err := r.schema.ExecContext(ctx, query)
	return err
}

func (r AgentStateStore) Put(ctx context.Context, workspaceID string, value agentmodel.AgentStateRecord) error {
	return r.PutBatch(ctx, workspaceID, []agentmodel.AgentStateRecord{value})
}

func (r AgentStateStore) PutBatch(ctx context.Context, workspaceID string, values []agentmodel.AgentStateRecord) error {
	workspace, err := principalmodel.NewWorkspaceID(workspaceID)
	if err != nil {
		return err
	}
	workspaceID = workspace.String()
	if err := r.EnsureSchema(ctx); err != nil {
		return fmt.Errorf("ensure agent state table: %w", err)
	}
	byKey := make(map[string]agentmodel.AgentStateRecord, len(values))
	order := make([]string, 0, len(values))
	for _, value := range values {
		if strings.TrimSpace(value.WorkspaceID) != workspaceID {
			return fmt.Errorf("agent state workspace %q does not match repository workspace %q", value.WorkspaceID, workspaceID)
		}
		key := strings.TrimSpace(value.Kind) + "\x00" + strings.TrimSpace(value.Key)
		if _, exists := byKey[key]; !exists {
			order = append(order, key)
		}
		byKey[key] = value
	}
	if len(order) == 0 {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	columns := []string{"kind", "state_key", "workspace_id", "user_id", "role_key", "payload_json", "updated_at"}
	for start := 0; start < len(order); start += 50 {
		end := min(start+50, len(order))
		rows := make([]string, 0, end-start)
		args := make([]any, 0, (end-start)*len(columns))
		for _, key := range order[start:end] {
			value := byKey[key]
			placeholders := make([]string, len(columns))
			for index := range placeholders {
				placeholders[index] = r.store.Placeholder(len(args) + index + 1)
			}
			rows = append(rows, "("+strings.Join(placeholders, ", ")+")")
			args = append(args, strings.TrimSpace(value.Kind), strings.TrimSpace(value.Key), workspaceID, strings.TrimSpace(value.UserID), strings.TrimSpace(value.RoleKey), []byte(value.Payload), value.UpdatedAt)
		}
		insertSQL := "INSERT INTO " + r.store.TableIdentifier("agent_runtime_state") + " (" + strings.Join(database.QuotedColumns(r.store, columns), ", ") + ") VALUES " + strings.Join(rows, ", ") + r.agentStateUpsertClause()
		if _, err := tx.ExecContext(ctx, insertSQL, args...); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r AgentStateStore) agentStateUpsertClause() string {
	columns := []string{"user_id", "role_key", "payload_json", "updated_at"}
	assignments := make([]string, 0, len(columns))
	if r.driver == "mysql" {
		for _, column := range columns {
			assignments = append(assignments, r.store.Identifier(column)+" = VALUES("+r.store.Identifier(column)+")")
		}
		return " ON DUPLICATE KEY UPDATE " + strings.Join(assignments, ", ")
	}
	for _, column := range columns {
		assignments = append(assignments, r.store.Identifier(column)+" = excluded."+r.store.Identifier(column))
	}
	return " ON CONFLICT (" + strings.Join(database.QuotedColumns(r.store, []string{"workspace_id", "kind", "state_key"}), ", ") + ") DO UPDATE SET " + strings.Join(assignments, ", ")
}

func (r AgentStateStore) CompareAndSwap(ctx context.Context, workspaceID string, value agentmodel.AgentStateRecord, expectedUpdatedAt int64) (bool, error) {
	workspace, err := principalmodel.NewWorkspaceID(workspaceID)
	if err != nil {
		return false, err
	}
	workspaceID = workspace.String()
	if strings.TrimSpace(value.WorkspaceID) != workspaceID {
		return false, fmt.Errorf("agent state workspace %q does not match repository workspace %q", value.WorkspaceID, workspaceID)
	}
	if err := r.EnsureSchema(ctx); err != nil {
		return false, err
	}
	query := "UPDATE " + r.store.TableIdentifier("agent_runtime_state") + " SET " + r.store.Identifier("payload_json") + " = " + r.store.Placeholder(1) + ", " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(2) + " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(3) + " AND " + r.store.Identifier("kind") + " = " + r.store.Placeholder(4) + " AND " + r.store.Identifier("state_key") + " = " + r.store.Placeholder(5) + " AND " + r.store.Identifier("updated_at") + " = " + r.store.Placeholder(6)
	result, err := r.db.ExecContext(ctx, query, []byte(value.Payload), value.UpdatedAt, workspaceID, strings.TrimSpace(value.Kind), strings.TrimSpace(value.Key), expectedUpdatedAt)
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows == 1, nil
}

func (r AgentStateStore) Get(ctx context.Context, workspaceID, kind, key string) (agentmodel.AgentStateRecord, bool, error) {
	workspace, err := principalmodel.NewWorkspaceID(workspaceID)
	if err != nil {
		return agentmodel.AgentStateRecord{}, false, err
	}
	workspaceID = workspace.String()
	if err := r.EnsureSchema(ctx); err != nil {
		return agentmodel.AgentStateRecord{}, false, err
	}
	query := "SELECT " + strings.Join([]string{r.store.Identifier("workspace_id"), r.store.Identifier("user_id"), r.store.Identifier("role_key"), r.store.Identifier("payload_json"), r.store.Identifier("updated_at")}, ", ") + " FROM " + r.store.TableIdentifier("agent_runtime_state") + " WHERE " + r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(1) + " AND " + r.store.Identifier("kind") + " = " + r.store.Placeholder(2) + " AND " + r.store.Identifier("state_key") + " = " + r.store.Placeholder(3)
	value := agentmodel.AgentStateRecord{Kind: kind, Key: key}
	var payload []byte
	err = r.db.QueryRowContext(ctx, query, workspaceID, kind, key).Scan(&value.WorkspaceID, &value.UserID, &value.RoleKey, &payload, &value.UpdatedAt)
	if err == sql.ErrNoRows {
		return agentmodel.AgentStateRecord{}, false, nil
	}
	if err != nil {
		return agentmodel.AgentStateRecord{}, false, err
	}
	value.Payload = append(value.Payload[:0], payload...)
	return value, true, nil
}

func (r AgentStateStore) List(ctx context.Context, workspaceID, kind, userID, roleKey string) ([]agentmodel.AgentStateRecord, error) {
	workspace, err := principalmodel.NewWorkspaceID(workspaceID)
	if err != nil {
		return nil, err
	}
	workspaceID = workspace.String()
	if err := r.EnsureSchema(ctx); err != nil {
		return nil, err
	}
	where := []string{r.store.Identifier("workspace_id") + " = " + r.store.Placeholder(1), r.store.Identifier("kind") + " = " + r.store.Placeholder(2)}
	args := []any{workspaceID, kind}
	for _, filter := range []struct{ column, value string }{{"user_id", userID}, {"role_key", roleKey}} {
		if strings.TrimSpace(filter.value) == "" {
			continue
		}
		args = append(args, strings.TrimSpace(filter.value))
		where = append(where, r.store.Identifier(filter.column)+" = "+r.store.Placeholder(len(args)))
	}
	query := "SELECT " + strings.Join([]string{r.store.Identifier("state_key"), r.store.Identifier("workspace_id"), r.store.Identifier("user_id"), r.store.Identifier("role_key"), r.store.Identifier("payload_json"), r.store.Identifier("updated_at")}, ", ") + " FROM " + r.store.TableIdentifier("agent_runtime_state") + " WHERE " + strings.Join(where, " AND ") + " ORDER BY " + r.store.Identifier("updated_at") + " DESC"
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []agentmodel.AgentStateRecord{}
	for rows.Next() {
		value := agentmodel.AgentStateRecord{Kind: kind}
		var payload []byte
		if err := rows.Scan(&value.Key, &value.WorkspaceID, &value.UserID, &value.RoleKey, &payload, &value.UpdatedAt); err != nil {
			return nil, err
		}
		value.Payload = append(value.Payload[:0], payload...)
		values = append(values, value)
	}
	return values, rows.Err()
}
