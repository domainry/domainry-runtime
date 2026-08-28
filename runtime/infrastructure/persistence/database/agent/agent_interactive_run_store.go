package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	agentrepository "github.com/domainry/domainry-runtime/runtime/domain/agent/repository"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func (s *AgentTaskRunStore) ensureInteractiveRunSchema(ctx context.Context) error {
	if s == nil || s.store == nil || s.db == nil || s.schema == nil {
		return apperror.New(apperror.KindUnavailable, "agent.interactive.repository_unavailable", nil, nil)
	}
	s.interactiveSchemaOnce.Do(func() {
		s.interactiveSchemaError = s.createInteractiveRunSchema(ctx)
	})
	return s.interactiveSchemaError
}

func (s *AgentTaskRunStore) createInteractiveRunSchema(ctx context.Context) error {
	textType := "TEXT"
	if s.store.Driver() == "mysql" {
		textType = "VARCHAR(255)"
	}
	columns := []string{
		s.store.Identifier("workspace_id") + " " + textType + " NOT NULL", s.store.Identifier("run_id") + " " + textType + " NOT NULL",
		s.store.Identifier("session_id") + " " + textType + " NOT NULL", s.store.Identifier("user_id") + " " + textType + " NOT NULL",
		s.store.Identifier("role_key") + " " + textType + " NOT NULL", s.store.Identifier("surface") + " " + textType + " NOT NULL",
		s.store.Identifier("status") + " " + textType + " NOT NULL", s.store.Identifier("idempotency_key") + " " + textType + " NOT NULL",
		s.store.Identifier("process_id") + " " + textType + " NOT NULL", s.store.Identifier("task_run_id") + " " + textType + " NOT NULL",
		s.store.Identifier("payload_json") + " TEXT NOT NULL", s.store.Identifier("created_at") + " BIGINT NOT NULL", s.store.Identifier("updated_at") + " BIGINT NOT NULL",
		"PRIMARY KEY (" + s.store.Identifier("workspace_id") + ", " + s.store.Identifier("run_id") + ")",
		"UNIQUE (" + s.store.Identifier("workspace_id") + ", " + s.store.Identifier("idempotency_key") + ")",
	}
	if _, err := s.schema.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.store.TableIdentifier("agent_interactive_runs")+" ("+strings.Join(columns, ", ")+")"); err != nil {
		return err
	}
	return nil
}

func (s *AgentTaskRunStore) CreateInteractiveRun(ctx context.Context, run agentmodel.AgentInteractiveRun) (agentmodel.AgentInteractiveRun, bool, error) {
	if err := s.ensureInteractiveRunSchema(ctx); err != nil {
		return agentmodel.AgentInteractiveRun{}, false, err
	}
	payload, err := json.Marshal(run)
	if err != nil {
		return agentmodel.AgentInteractiveRun{}, false, err
	}
	columns := []string{"workspace_id", "run_id", "session_id", "user_id", "role_key", "surface", "status", "idempotency_key", "process_id", "task_run_id", "payload_json", "created_at", "updated_at"}
	query := "INSERT INTO " + s.store.TableIdentifier("agent_interactive_runs") + " (" + s.identifiers(columns) + ") VALUES (" + s.placeholders(len(columns)) + ")"
	_, err = s.db.ExecContext(ctx, query, run.WorkspaceID, run.ID, run.SessionID, run.UserID, run.RoleKey, run.Surface, string(run.Status), run.IdempotencyKey, run.ProcessID, run.TaskRunID, payload, run.CreatedAt.UnixMilli(), run.UpdatedAt.UnixMilli())
	if err == nil {
		return run, false, nil
	}
	existing, found, getErr := s.getInteractiveByIdempotency(ctx, run.WorkspaceID, run.IdempotencyKey)
	if getErr == nil && found {
		return existing, true, nil
	}
	return agentmodel.AgentInteractiveRun{}, false, err
}

func (s *AgentTaskRunStore) GetInteractiveRun(ctx context.Context, workspaceID, runID string) (agentmodel.AgentInteractiveRun, bool, error) {
	if err := s.ensureInteractiveRunSchema(ctx); err != nil {
		return agentmodel.AgentInteractiveRun{}, false, err
	}
	query := "SELECT " + s.store.Identifier("payload_json") + " FROM " + s.store.TableIdentifier("agent_interactive_runs") + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("run_id") + " = " + s.store.Placeholder(2)
	return scanInteractiveRun(s.db.QueryRowContext(ctx, query, strings.TrimSpace(workspaceID), strings.TrimSpace(runID)))
}

func (s *AgentTaskRunStore) getInteractiveByIdempotency(ctx context.Context, workspaceID, key string) (agentmodel.AgentInteractiveRun, bool, error) {
	query := "SELECT " + s.store.Identifier("payload_json") + " FROM " + s.store.TableIdentifier("agent_interactive_runs") + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("idempotency_key") + " = " + s.store.Placeholder(2)
	return scanInteractiveRun(s.db.QueryRowContext(ctx, query, workspaceID, key))
}

func scanInteractiveRun(row *sql.Row) (agentmodel.AgentInteractiveRun, bool, error) {
	var payload []byte
	if err := row.Scan(&payload); err == sql.ErrNoRows {
		return agentmodel.AgentInteractiveRun{}, false, nil
	} else if err != nil {
		return agentmodel.AgentInteractiveRun{}, false, err
	}
	var run agentmodel.AgentInteractiveRun
	if err := json.Unmarshal(payload, &run); err != nil {
		return agentmodel.AgentInteractiveRun{}, false, err
	}
	return run, true, nil
}

func (s *AgentTaskRunStore) ListInteractiveRuns(ctx context.Context, workspaceID, userID, roleKey string, filter agentrepository.AgentInteractiveRunFilter) ([]agentmodel.AgentInteractiveRun, error) {
	if err := s.ensureInteractiveRunSchema(ctx); err != nil {
		return nil, err
	}
	query := "SELECT " + s.store.Identifier("payload_json") + " FROM " + s.store.TableIdentifier("agent_interactive_runs") + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("user_id") + " = " + s.store.Placeholder(2) + " AND " + s.store.Identifier("role_key") + " = " + s.store.Placeholder(3)
	args := []any{strings.TrimSpace(workspaceID), strings.TrimSpace(userID), strings.TrimSpace(roleKey)}
	if len(filter.Statuses) > 0 {
		query += " AND " + s.store.Identifier("status") + " IN ("
		values := make([]string, len(filter.Statuses))
		for index, status := range filter.Statuses {
			args = append(args, string(status))
			values[index] = s.store.Placeholder(len(args))
		}
		query += strings.Join(values, ", ") + ")"
	}
	limit := filter.Limit
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	query += " ORDER BY " + s.store.Identifier("created_at") + " DESC LIMIT " + s.store.Placeholder(len(args)+1)
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	runs := []agentmodel.AgentInteractiveRun{}
	for rows.Next() {
		var payload []byte
		var run agentmodel.AgentInteractiveRun
		if err := rows.Scan(&payload); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(payload, &run); err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	return runs, rows.Err()
}

func (s *AgentTaskRunStore) SaveInteractiveRun(ctx context.Context, run agentmodel.AgentInteractiveRun, expectedRevision int64) (bool, error) {
	if err := s.ensureInteractiveRunSchema(ctx); err != nil {
		return false, err
	}
	current, found, err := s.GetInteractiveRun(ctx, run.WorkspaceID, run.ID)
	if err != nil || !found || current.Revision != expectedRevision {
		return false, err
	}
	payload, err := json.Marshal(run)
	if err != nil {
		return false, err
	}
	query := "UPDATE " + s.store.TableIdentifier("agent_interactive_runs") + " SET " + s.store.Identifier("status") + " = " + s.store.Placeholder(1) + ", " + s.store.Identifier("process_id") + " = " + s.store.Placeholder(2) + ", " + s.store.Identifier("task_run_id") + " = " + s.store.Placeholder(3) + ", " + s.store.Identifier("payload_json") + " = " + s.store.Placeholder(4) + ", " + s.store.Identifier("updated_at") + " = " + s.store.Placeholder(5) + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(6) + " AND " + s.store.Identifier("run_id") + " = " + s.store.Placeholder(7) + " AND " + s.store.Identifier("updated_at") + " = " + s.store.Placeholder(8)
	result, err := s.db.ExecContext(ctx, query, string(run.Status), run.ProcessID, run.TaskRunID, payload, run.UpdatedAt.UnixMilli(), run.WorkspaceID, run.ID, current.UpdatedAt.UnixMilli())
	if err != nil {
		return false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows == 1, nil
}

func (s *AgentTaskRunStore) CommitInteractiveTaskHandoff(ctx context.Context, run agentmodel.AgentInteractiveRun, expectedRevision int64, task agentmodel.AgentTaskRun) (agentmodel.AgentInteractiveRun, bool, error) {
	if err := s.ensureInteractiveRunSchema(ctx); err != nil {
		return agentmodel.AgentInteractiveRun{}, false, err
	}
	if err := s.EnsureSchema(ctx); err != nil {
		return agentmodel.AgentInteractiveRun{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return agentmodel.AgentInteractiveRun{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	query := "SELECT " + s.store.Identifier("payload_json") + " FROM " + s.store.TableIdentifier("agent_interactive_runs") + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("run_id") + " = " + s.store.Placeholder(2)
	var payload []byte
	if err := tx.QueryRowContext(ctx, query, run.WorkspaceID, run.ID).Scan(&payload); err != nil {
		return agentmodel.AgentInteractiveRun{}, false, err
	}
	var current agentmodel.AgentInteractiveRun
	if err := json.Unmarshal(payload, &current); err != nil {
		return agentmodel.AgentInteractiveRun{}, false, err
	}
	if current.TaskRunID != "" {
		return current, true, nil
	}
	if current.Revision != expectedRevision || current.Status != agentmodel.AgentInteractiveRunRunning || current.WorkspaceID != task.WorkspaceID || task.InteractiveRunID != current.ID {
		return agentmodel.AgentInteractiveRun{}, false, apperror.New(apperror.KindConflict, "agent.interactive.handoff_conflict", nil, nil)
	}
	taskPayload, err := json.Marshal(task)
	if err != nil {
		return agentmodel.AgentInteractiveRun{}, false, err
	}
	columns := []string{"workspace_id", "run_id", "idempotency_key", "task_key", "process_id", "status", "lease_owner", "fencing_token", "lease_expires_at", "next_attempt_at", "payload_json", "created_at", "updated_at"}
	insert := "INSERT INTO " + s.store.TableIdentifier("agent_task_runs") + " (" + s.identifiers(columns) + ") VALUES (" + s.placeholders(len(columns)) + ")"
	if _, err := tx.ExecContext(ctx, insert, task.WorkspaceID, task.ID, task.IdempotencyKey, task.TaskKey, task.ProcessID, string(task.Status), "", int64(0), int64(0), timeMillis(task.NextAttemptAt), taskPayload, task.CreatedAt.UnixMilli(), task.UpdatedAt.UnixMilli()); err != nil {
		return agentmodel.AgentInteractiveRun{}, false, err
	}
	if err := RegisterAgentTaskWorkerScope(ctx, s.store, tx, task.WorkspaceID, task.UpdatedAt); err != nil {
		return agentmodel.AgentInteractiveRun{}, false, err
	}
	now := time.Now().UTC()
	current.Status, current.RouteType, current.RoutedTargetKey, current.RoutedTargetVersion = agentmodel.AgentInteractiveRunHandedOff, run.RouteType, run.RoutedTargetKey, run.RoutedTargetVersion
	current.ProcessID, current.TaskRunID, current.UpdatedAt, current.CompletedAt, current.Revision = task.ProcessID, task.ID, now, &now, current.Revision+1
	updated, _ := json.Marshal(current)
	update := "UPDATE " + s.store.TableIdentifier("agent_interactive_runs") + " SET " + s.store.Identifier("status") + " = " + s.store.Placeholder(1) + ", " + s.store.Identifier("process_id") + " = " + s.store.Placeholder(2) + ", " + s.store.Identifier("task_run_id") + " = " + s.store.Placeholder(3) + ", " + s.store.Identifier("payload_json") + " = " + s.store.Placeholder(4) + ", " + s.store.Identifier("updated_at") + " = " + s.store.Placeholder(5) + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(6) + " AND " + s.store.Identifier("run_id") + " = " + s.store.Placeholder(7) + " AND " + s.store.Identifier("updated_at") + " = " + s.store.Placeholder(8)
	result, err := tx.ExecContext(ctx, update, string(current.Status), current.ProcessID, current.TaskRunID, updated, current.UpdatedAt.UnixMilli(), current.WorkspaceID, current.ID, run.UpdatedAt.UnixMilli())
	if err != nil {
		return agentmodel.AgentInteractiveRun{}, false, err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return agentmodel.AgentInteractiveRun{}, false, err
	}
	if rows != 1 {
		return agentmodel.AgentInteractiveRun{}, false, apperror.New(apperror.KindConflict, "agent.interactive.handoff_conflict", nil, nil)
	}
	if err := tx.Commit(); err != nil {
		return agentmodel.AgentInteractiveRun{}, false, err
	}
	return current, false, nil
}

func (s *AgentTaskRunStore) identifiers(columns []string) string {
	values := make([]string, len(columns))
	for index, column := range columns {
		values[index] = s.store.Identifier(column)
	}
	return strings.Join(values, ", ")
}

var _ agentrepository.AgentInteractiveRunRepository = (*AgentTaskRunStore)(nil)
