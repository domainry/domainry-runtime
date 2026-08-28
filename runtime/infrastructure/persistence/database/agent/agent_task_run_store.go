package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	agentrepository "github.com/domainry/domainry-runtime/runtime/domain/agent/repository"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	runtimeschema "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/schema"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/requestcontext"
)

type AgentTaskRunStore struct {
	store                  *database.RuntimeStore
	db                     *sql.DB
	schema                 runtimeschema.SQLDatabase
	schemaOnce             sync.Once
	schemaErr              error
	interactiveSchemaOnce  sync.Once
	interactiveSchemaError error
}

const agentTaskWorkerQueueKind = "agent_task"

func RegisterAgentTaskWorkerScope(ctx context.Context, store *database.RuntimeStore, executor database.WorkerScopeExecutor, workspaceID string, updatedAt time.Time) error {
	if store == nil {
		return fmt.Errorf("agent task worker scope store unavailable")
	}
	return store.RegisterWorkerQueueScope(ctx, executor, agentTaskWorkerQueueKind, workspaceID, updatedAt.UTC().Format(time.RFC3339Nano))
}

func NewAgentTaskRunStore(store *database.RuntimeStore) *AgentTaskRunStore {
	if store == nil {
		return &AgentTaskRunStore{}
	}
	return &AgentTaskRunStore{store: store, db: store.DB(), schema: store.SchemaDB()}
}

func (s *AgentTaskRunStore) EnsureSchema(ctx context.Context) error {
	if s == nil {
		return fmt.Errorf("agent task run store unavailable")
	}
	if s.store == nil {
		return fmt.Errorf("agent task run store unavailable")
	}
	if s.db == nil || s.schema == nil {
		return fmt.Errorf("agent task run store unavailable")
	}
	s.schemaOnce.Do(func() {
		s.schemaErr = s.ensureSchema(ctx)
	})
	return s.schemaErr
}

func (s *AgentTaskRunStore) ensureSchema(ctx context.Context) error {
	table := s.store.TableIdentifier("agent_task_runs")
	textType := "TEXT"
	if s.store.Driver() == "mysql" {
		textType = "VARCHAR(255)"
	}
	columns := []string{
		s.store.Identifier("workspace_id") + " " + textType + " NOT NULL",
		s.store.Identifier("run_id") + " " + textType + " NOT NULL",
		s.store.Identifier("idempotency_key") + " " + textType + " NOT NULL",
		s.store.Identifier("task_key") + " " + textType + " NOT NULL",
		s.store.Identifier("process_id") + " " + textType + " NOT NULL",
		s.store.Identifier("status") + " " + textType + " NOT NULL",
		s.store.Identifier("lease_owner") + " " + textType + " NOT NULL",
		s.store.Identifier("fencing_token") + " BIGINT NOT NULL",
		s.store.Identifier("lease_expires_at") + " BIGINT NOT NULL",
		s.store.Identifier("next_attempt_at") + " BIGINT NOT NULL",
		s.store.Identifier("payload_json") + " TEXT NOT NULL",
		s.store.Identifier("created_at") + " BIGINT NOT NULL",
		s.store.Identifier("updated_at") + " BIGINT NOT NULL",
		"PRIMARY KEY (" + s.store.Identifier("workspace_id") + ", " + s.store.Identifier("run_id") + ")",
		"UNIQUE (" + s.store.Identifier("workspace_id") + ", " + s.store.Identifier("idempotency_key") + ")",
	}
	if _, err := s.schema.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+table+" ("+strings.Join(columns, ", ")+")"); err != nil {
		return err
	}
	for name, columns := range map[string][]string{
		"idx_agent_task_claim":   {"workspace_id", "status", "next_attempt_at", "lease_expires_at", "created_at"},
		"idx_agent_task_process": {"workspace_id", "process_id", "status"},
		"idx_agent_task_key":     {"workspace_id", "task_key", "status"},
	} {
		parts := make([]string, 0, len(columns))
		for _, column := range columns {
			parts = append(parts, s.store.Identifier(column))
		}
		query := "CREATE INDEX IF NOT EXISTS " + s.store.Identifier(name) + " ON " + table + " (" + strings.Join(parts, ", ") + ")"
		if s.store.Driver() == "mysql" {
			query = "CREATE INDEX " + s.store.Identifier(name) + " ON " + table + " (" + strings.Join(parts, ", ") + ")"
		}
		if _, err := s.schema.ExecContext(ctx, query); err != nil && s.store.Driver() != "mysql" {
			return err
		}
	}
	return nil
}

// BackfillWorkerScopes inventories legacy Agent tasks once during Runtime
// startup. It is deliberately not part of EnsureSchema: ordinary Agent reads
// and writes must not perform a global workspace inventory.
func (s *AgentTaskRunStore) BackfillWorkerScopes(ctx context.Context) error {
	if err := s.EnsureSchema(ctx); err != nil {
		return err
	}
	query := "SELECT " + s.store.Identifier("workspace_id") + ", MAX(" + s.store.Identifier("updated_at") + ") FROM " + s.store.TableIdentifier("agent_task_runs") + " GROUP BY " + s.store.Identifier("workspace_id")
	rows, err := s.schema.QueryContext(ctx, query)
	if err != nil {
		return err
	}
	type workerScope struct {
		workspaceID     string
		updatedAtMillis int64
	}
	scopes := []workerScope{}
	for rows.Next() {
		var scope workerScope
		if err := rows.Scan(&scope.workspaceID, &scope.updatedAtMillis); err != nil {
			_ = rows.Close()
			return err
		}
		scopes = append(scopes, scope)
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, scope := range scopes {
		if err := RegisterAgentTaskWorkerScope(ctx, s.store, s.schema, scope.workspaceID, time.UnixMilli(scope.updatedAtMillis)); err != nil {
			return err
		}
	}
	return nil
}

func (s *AgentTaskRunStore) Create(ctx context.Context, run agentmodel.AgentTaskRun) (agentmodel.AgentTaskRun, bool, error) {
	if err := s.EnsureSchema(ctx); err != nil {
		return agentmodel.AgentTaskRun{}, false, err
	}
	payload, err := json.Marshal(run)
	if err != nil {
		return agentmodel.AgentTaskRun{}, false, err
	}
	query := "INSERT INTO " + s.store.TableIdentifier("agent_task_runs") + " (" + s.agentTaskColumns() + ") VALUES (" + s.placeholders(13) + ")"
	_, err = s.db.ExecContext(ctx, query, run.WorkspaceID, run.ID, run.IdempotencyKey, run.TaskKey, run.ProcessID, string(run.Status), "", int64(0), int64(0), timeMillis(run.NextAttemptAt), payload, run.CreatedAt.UnixMilli(), run.UpdatedAt.UnixMilli())
	if err == nil {
		if registerErr := RegisterAgentTaskWorkerScope(ctx, s.store, s.db, run.WorkspaceID, run.UpdatedAt); registerErr != nil {
			return agentmodel.AgentTaskRun{}, false, registerErr
		}
		return run, false, nil
	}
	existing, found, getErr := s.getByIdempotency(ctx, run.WorkspaceID, run.IdempotencyKey)
	if getErr == nil && found {
		if registerErr := RegisterAgentTaskWorkerScope(ctx, s.store, s.db, existing.WorkspaceID, existing.UpdatedAt); registerErr != nil {
			return agentmodel.AgentTaskRun{}, false, registerErr
		}
		return existing, true, nil
	}
	return agentmodel.AgentTaskRun{}, false, err
}

func (s *AgentTaskRunStore) Get(ctx context.Context, workspaceID, runID string) (agentmodel.AgentTaskRun, bool, error) {
	if err := s.EnsureSchema(ctx); err != nil {
		return agentmodel.AgentTaskRun{}, false, err
	}
	query := "SELECT " + s.store.Identifier("payload_json") + " FROM " + s.store.TableIdentifier("agent_task_runs") + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("run_id") + " = " + s.store.Placeholder(2)
	return s.scanRun(s.db.QueryRowContext(ctx, query, strings.TrimSpace(workspaceID), strings.TrimSpace(runID)))
}

func (s *AgentTaskRunStore) getByIdempotency(ctx context.Context, workspaceID, key string) (agentmodel.AgentTaskRun, bool, error) {
	query := "SELECT " + s.store.Identifier("payload_json") + " FROM " + s.store.TableIdentifier("agent_task_runs") + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("idempotency_key") + " = " + s.store.Placeholder(2)
	return s.scanRun(s.db.QueryRowContext(ctx, query, workspaceID, key))
}

type rowScanner interface{ Scan(...any) error }

func (s *AgentTaskRunStore) scanRun(row rowScanner) (agentmodel.AgentTaskRun, bool, error) {
	var payload []byte
	if err := row.Scan(&payload); err == sql.ErrNoRows {
		return agentmodel.AgentTaskRun{}, false, nil
	} else if err != nil {
		return agentmodel.AgentTaskRun{}, false, err
	}
	var run agentmodel.AgentTaskRun
	if err := json.Unmarshal(payload, &run); err != nil {
		return agentmodel.AgentTaskRun{}, false, err
	}
	return run, true, nil
}

func (s *AgentTaskRunStore) List(ctx context.Context, workspaceID string, filter agentrepository.AgentTaskRunFilter) ([]agentmodel.AgentTaskRun, error) {
	if err := s.EnsureSchema(ctx); err != nil {
		return nil, err
	}
	where, args := []string{s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1)}, []any{strings.TrimSpace(workspaceID)}
	if filter.ProcessID != "" {
		args = append(args, strings.TrimSpace(filter.ProcessID))
		where = append(where, s.store.Identifier("process_id")+" = "+s.store.Placeholder(len(args)))
	}
	if filter.TaskKey != "" {
		args = append(args, strings.TrimSpace(filter.TaskKey))
		where = append(where, s.store.Identifier("task_key")+" = "+s.store.Placeholder(len(args)))
	}
	if len(filter.Statuses) > 0 {
		values := []string{}
		for _, status := range filter.Statuses {
			args = append(args, string(status))
			values = append(values, s.store.Placeholder(len(args)))
		}
		where = append(where, s.store.Identifier("status")+" IN ("+strings.Join(values, ", ")+")")
	}
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := "SELECT " + s.store.Identifier("payload_json") + " FROM " + s.store.TableIdentifier("agent_task_runs") + " WHERE " + strings.Join(where, " AND ") + " ORDER BY " + s.store.Identifier("created_at") + " ASC LIMIT " + fmt.Sprint(limit)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []agentmodel.AgentTaskRun{}
	for rows.Next() {
		run, _, err := s.scanRun(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, rows.Err()
}

func (s *AgentTaskRunStore) ClaimNext(ctx context.Context, workspaceID string, owner string, now time.Time, duration time.Duration) (agentrepository.AgentTaskClaim, bool, error) {
	if err := s.EnsureSchema(ctx); err != nil {
		return agentrepository.AgentTaskClaim{}, false, err
	}
	ctx = requestcontext.WithWorkspaceID(ctx, strings.TrimSpace(workspaceID))
	ctx = requestcontext.WithActorID(ctx, strings.TrimSpace(owner))
	var lastErr error
	for attempt := 0; attempt < 16; attempt++ {
		claim, found, err := s.claimNextOnce(ctx, workspaceID, owner, now, duration)
		if err == nil {
			return claim, found, nil
		}
		if !agentTaskClaimRetryable(err) {
			return agentrepository.AgentTaskClaim{}, false, err
		}
		lastErr = err
		delay := time.Duration(attempt+1) * time.Millisecond
		select {
		case <-ctx.Done():
			return agentrepository.AgentTaskClaim{}, false, ctx.Err()
		case <-time.After(delay):
		}
	}
	return agentrepository.AgentTaskClaim{}, false, fmt.Errorf("agent task claim retry exhausted: %w", lastErr)
}

func (s *AgentTaskRunStore) ClaimAgentTaskRun(ctx context.Context, workspaceID, runID, owner string, now time.Time, duration time.Duration) (agentrepository.AgentTaskClaim, bool, error) {
	if err := s.EnsureSchema(ctx); err != nil {
		return agentrepository.AgentTaskClaim{}, false, err
	}
	workspaceID, runID, owner = strings.TrimSpace(workspaceID), strings.TrimSpace(runID), strings.TrimSpace(owner)
	if len(workspaceID) == 0 {
		return agentrepository.AgentTaskClaim{}, false, fmt.Errorf("agent task direct claim is invalid")
	}
	if runID == "" || owner == "" || duration <= 0 {
		return agentrepository.AgentTaskClaim{}, false, fmt.Errorf("agent task direct claim is invalid")
	}
	ctx = requestcontext.WithWorkspaceID(ctx, workspaceID)
	ctx = requestcontext.WithActorID(ctx, owner)
	var lastErr error
	for attempt := 0; attempt < 16; attempt++ {
		claim, found, err := s.claimAgentTaskRunOnce(ctx, workspaceID, runID, owner, now, duration)
		if err == nil {
			return claim, found, nil
		}
		if !agentTaskClaimRetryable(err) {
			return agentrepository.AgentTaskClaim{}, false, err
		}
		lastErr = err
		select {
		case <-ctx.Done():
			return agentrepository.AgentTaskClaim{}, false, ctx.Err()
		case <-time.After(time.Duration(attempt+1) * time.Millisecond):
		}
	}
	return agentrepository.AgentTaskClaim{}, false, fmt.Errorf("agent task direct claim retry exhausted: %w", lastErr)
}

func (s *AgentTaskRunStore) claimAgentTaskRunOnce(ctx context.Context, workspaceID, runID, owner string, now time.Time, duration time.Duration) (agentrepository.AgentTaskClaim, bool, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return agentrepository.AgentTaskClaim{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	eligible := "((" + s.store.Identifier("status") + " IN (" + s.store.Placeholder(3) + ", " + s.store.Placeholder(4) + ") AND " + s.store.Identifier("next_attempt_at") + " <= " + s.store.Placeholder(5) + ") OR (" + s.store.Identifier("status") + " = " + s.store.Placeholder(6) + " AND " + s.store.Identifier("lease_expires_at") + " <= " + s.store.Placeholder(7) + "))"
	query := "SELECT " + s.store.Identifier("payload_json") + ", " + s.store.Identifier("fencing_token") + " FROM " + s.store.TableIdentifier("agent_task_runs") + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("run_id") + " = " + s.store.Placeholder(2) + " AND " + eligible
	var payload []byte
	var priorToken int64
	err = tx.QueryRowContext(ctx, query, workspaceID, runID, string(agentmodel.AgentTaskRunPending), string(agentmodel.AgentTaskRunRetryScheduled), now.UnixMilli(), string(agentmodel.AgentTaskRunRunning), now.UnixMilli()).Scan(&payload, &priorToken)
	if err == sql.ErrNoRows {
		return agentrepository.AgentTaskClaim{}, false, nil
	}
	if err != nil {
		return agentrepository.AgentTaskClaim{}, false, err
	}
	var run agentmodel.AgentTaskRun
	if err := json.Unmarshal(payload, &run); err != nil {
		return agentrepository.AgentTaskClaim{}, false, err
	}
	token, expires := priorToken+1, now.Add(duration)
	run.Status, run.Attempt, run.Revision, run.UpdatedAt = agentmodel.AgentTaskRunRunning, run.Attempt+1, run.Revision+1, now
	run.Lease = agentmodel.AgentTaskLease{Owner: owner, FencingToken: token, ExpiresAt: expires}
	run.Attempts = append(run.Attempts, agentmodel.AgentTaskAttempt{Number: run.Attempt, StartedAt: now})
	updatedPayload, _ := json.Marshal(run)
	update := "UPDATE " + s.store.TableIdentifier("agent_task_runs") + " SET " + s.store.Identifier("status") + " = " + s.store.Placeholder(1) + ", " + s.store.Identifier("lease_owner") + " = " + s.store.Placeholder(2) + ", " + s.store.Identifier("fencing_token") + " = " + s.store.Placeholder(3) + ", " + s.store.Identifier("lease_expires_at") + " = " + s.store.Placeholder(4) + ", " + s.store.Identifier("payload_json") + " = " + s.store.Placeholder(5) + ", " + s.store.Identifier("updated_at") + " = " + s.store.Placeholder(6) + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(7) + " AND " + s.store.Identifier("run_id") + " = " + s.store.Placeholder(8) + " AND " + s.store.Identifier("fencing_token") + " = " + s.store.Placeholder(9) + " AND " + eligibleWithOffset(s.store, 9)
	result, err := tx.ExecContext(ctx, update, string(run.Status), owner, token, expires.UnixMilli(), updatedPayload, now.UnixMilli(), workspaceID, runID, priorToken, string(agentmodel.AgentTaskRunPending), string(agentmodel.AgentTaskRunRetryScheduled), now.UnixMilli(), string(agentmodel.AgentTaskRunRunning), now.UnixMilli())
	if err != nil {
		return agentrepository.AgentTaskClaim{}, false, err
	}
	oneRow, rowsErr := agentTaskExactlyOneRow(result)
	if rowsErr != nil || !oneRow {
		return agentrepository.AgentTaskClaim{}, false, rowsErr
	}
	if err := tx.Commit(); err != nil {
		return agentrepository.AgentTaskClaim{}, false, err
	}
	return agentrepository.AgentTaskClaim{Run: run, Lease: run.Lease}, true, nil
}

func (s *AgentTaskRunStore) claimNextOnce(ctx context.Context, workspaceID string, owner string, now time.Time, duration time.Duration) (agentrepository.AgentTaskClaim, bool, error) {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return agentrepository.AgentTaskClaim{}, false, err
	}
	defer func() { _ = tx.Rollback() }()
	eligible := "((" + s.store.Identifier("status") + " IN (" + s.store.Placeholder(2) + ", " + s.store.Placeholder(3) + ") AND " + s.store.Identifier("next_attempt_at") + " <= " + s.store.Placeholder(4) + ") OR (" + s.store.Identifier("status") + " = " + s.store.Placeholder(5) + " AND " + s.store.Identifier("lease_expires_at") + " <= " + s.store.Placeholder(6) + "))"
	query := "SELECT " + s.store.Identifier("run_id") + ", " + s.store.Identifier("payload_json") + ", " + s.store.Identifier("fencing_token") + " FROM " + s.store.TableIdentifier("agent_task_runs") + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + eligible + " ORDER BY " + s.store.Identifier("created_at") + " ASC LIMIT 1"
	var runID string
	var payload []byte
	var priorToken int64
	err = tx.QueryRowContext(ctx, query, workspaceID, string(agentmodel.AgentTaskRunPending), string(agentmodel.AgentTaskRunRetryScheduled), now.UnixMilli(), string(agentmodel.AgentTaskRunRunning), now.UnixMilli()).Scan(&runID, &payload, &priorToken)
	if err == sql.ErrNoRows {
		return agentrepository.AgentTaskClaim{}, false, nil
	}
	if err != nil {
		return agentrepository.AgentTaskClaim{}, false, err
	}
	var run agentmodel.AgentTaskRun
	if err := json.Unmarshal(payload, &run); err != nil {
		return agentrepository.AgentTaskClaim{}, false, err
	}
	token, expires := priorToken+1, now.Add(duration)
	run.Status, run.Attempt, run.Revision, run.UpdatedAt = agentmodel.AgentTaskRunRunning, run.Attempt+1, run.Revision+1, now
	run.Lease = agentmodel.AgentTaskLease{Owner: owner, FencingToken: token, ExpiresAt: expires}
	run.Attempts = append(run.Attempts, agentmodel.AgentTaskAttempt{Number: run.Attempt, StartedAt: now})
	updatedPayload, _ := json.Marshal(run)
	update := "UPDATE " + s.store.TableIdentifier("agent_task_runs") + " SET " + s.store.Identifier("status") + " = " + s.store.Placeholder(1) + ", " + s.store.Identifier("lease_owner") + " = " + s.store.Placeholder(2) + ", " + s.store.Identifier("fencing_token") + " = " + s.store.Placeholder(3) + ", " + s.store.Identifier("lease_expires_at") + " = " + s.store.Placeholder(4) + ", " + s.store.Identifier("payload_json") + " = " + s.store.Placeholder(5) + ", " + s.store.Identifier("updated_at") + " = " + s.store.Placeholder(6) + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(7) + " AND " + s.store.Identifier("run_id") + " = " + s.store.Placeholder(8) + " AND " + s.store.Identifier("fencing_token") + " = " + s.store.Placeholder(9) + " AND " + eligibleWithOffset(s.store, 9)
	result, err := tx.ExecContext(ctx, update, string(run.Status), owner, token, expires.UnixMilli(), updatedPayload, now.UnixMilli(), workspaceID, runID, priorToken, string(agentmodel.AgentTaskRunPending), string(agentmodel.AgentTaskRunRetryScheduled), now.UnixMilli(), string(agentmodel.AgentTaskRunRunning), now.UnixMilli())
	if err != nil {
		return agentrepository.AgentTaskClaim{}, false, err
	}
	oneRow, rowsErr := agentTaskExactlyOneRow(result)
	if rowsErr != nil {
		return agentrepository.AgentTaskClaim{}, false, rowsErr
	}
	if !oneRow {
		return agentrepository.AgentTaskClaim{}, false, nil
	}
	if err := tx.Commit(); err != nil {
		return agentrepository.AgentTaskClaim{}, false, err
	}
	return agentrepository.AgentTaskClaim{Run: run, Lease: run.Lease}, true, nil
}

func agentTaskClaimRetryable(err error) bool {
	if err == nil {
		return false
	}
	message := strings.ToLower(err.Error())
	for _, marker := range []string{
		"database is locked",
		"database table is locked",
		"sqlite_busy",
		"deadlock",
		"error 1213",
		"sqlstate 40001",
		"serialization failure",
		"could not serialize access",
		"lock wait timeout",
	} {
		if strings.Contains(message, marker) {
			return true
		}
	}
	return false
}

func eligibleWithOffset(store *database.RuntimeStore, offset int) string {
	return "((" + store.Identifier("status") + " IN (" + store.Placeholder(offset+1) + ", " + store.Placeholder(offset+2) + ") AND " + store.Identifier("next_attempt_at") + " <= " + store.Placeholder(offset+3) + ") OR (" + store.Identifier("status") + " = " + store.Placeholder(offset+4) + " AND " + store.Identifier("lease_expires_at") + " <= " + store.Placeholder(offset+5) + "))"
}

func (s *AgentTaskRunStore) Heartbeat(ctx context.Context, workspaceID, runID string, owner string, token int64, now time.Time, duration time.Duration) (agentrepository.AgentTaskHeartbeatResult, error) {
	expires := now.Add(duration)
	query := "UPDATE " + s.store.TableIdentifier("agent_task_runs") + " SET " + s.store.Identifier("lease_expires_at") + " = " + s.store.Placeholder(1) + ", " + s.store.Identifier("updated_at") + " = " + s.store.Placeholder(2) + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(3) + " AND " + s.store.Identifier("run_id") + " = " + s.store.Placeholder(4) + " AND " + s.store.Identifier("status") + " = " + s.store.Placeholder(5) + " AND " + s.store.Identifier("lease_owner") + " = " + s.store.Placeholder(6) + " AND " + s.store.Identifier("fencing_token") + " = " + s.store.Placeholder(7) + " AND " + s.store.Identifier("lease_expires_at") + " > " + s.store.Placeholder(8)
	result, err := s.db.ExecContext(ctx, query, expires.UnixMilli(), now.UnixMilli(), workspaceID, runID, string(agentmodel.AgentTaskRunRunning), owner, token, now.UnixMilli())
	if err != nil {
		return agentrepository.AgentTaskHeartbeatResult{}, err
	}
	oneRow, rowsErr := agentTaskExactlyOneRow(result)
	if rowsErr != nil {
		return agentrepository.AgentTaskHeartbeatResult{}, rowsErr
	}
	if !oneRow {
		return agentrepository.AgentTaskHeartbeatResult{Lost: true}, nil
	}
	run, found, err := s.Get(ctx, workspaceID, runID)
	if err != nil || !found {
		return agentrepository.AgentTaskHeartbeatResult{}, err
	}
	run.Lease.ExpiresAt, run.UpdatedAt, run.Revision = expires, now, run.Revision+1
	payload, _ := json.Marshal(run)
	persist := "UPDATE " + s.store.TableIdentifier("agent_task_runs") + " SET " + s.store.Identifier("payload_json") + " = " + s.store.Placeholder(1) + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(2) + " AND " + s.store.Identifier("run_id") + " = " + s.store.Placeholder(3) + " AND " + s.store.Identifier("status") + " = " + s.store.Placeholder(4) + " AND " + s.store.Identifier("lease_owner") + " = " + s.store.Placeholder(5) + " AND " + s.store.Identifier("fencing_token") + " = " + s.store.Placeholder(6)
	if _, err := s.db.ExecContext(ctx, persist, payload, workspaceID, runID, string(agentmodel.AgentTaskRunRunning), owner, token); err != nil {
		return agentrepository.AgentTaskHeartbeatResult{}, err
	}
	return agentrepository.AgentTaskHeartbeatResult{Lease: agentmodel.AgentTaskLease{Owner: owner, FencingToken: token, ExpiresAt: expires}}, nil
}

func (s *AgentTaskRunStore) SaveRunning(ctx context.Context, run agentmodel.AgentTaskRun, owner string, token int64) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	read := "SELECT " + s.store.Identifier("payload_json") + " FROM " + s.store.TableIdentifier("agent_task_runs") + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("run_id") + " = " + s.store.Placeholder(2) + " AND " + s.store.Identifier("lease_owner") + " = " + s.store.Placeholder(3) + " AND " + s.store.Identifier("fencing_token") + " = " + s.store.Placeholder(4) + " AND " + s.store.Identifier("status") + " = " + s.store.Placeholder(5)
	var currentPayload []byte
	if err := tx.QueryRowContext(ctx, read, run.WorkspaceID, run.ID, owner, token, string(agentmodel.AgentTaskRunRunning)).Scan(&currentPayload); err == sql.ErrNoRows {
		return apperror.New(apperror.KindConflict, "agent.task.terminal_fence_rejected", nil, nil)
	} else if err != nil {
		return err
	}
	var current agentmodel.AgentTaskRun
	if err := json.Unmarshal(currentPayload, &current); err != nil {
		return err
	}
	// Tool calls run concurrently with the provider execution and append durable
	// evidence under the same lease. A terminal/retry write must merge those
	// facts instead of replacing them with the worker's older in-memory copy.
	run.ToolCallCount = current.ToolCallCount
	run.Evidence.Authorization = current.Evidence.Authorization
	run.Evidence.ToolInvocationRefs = current.Evidence.ToolInvocationRefs
	run.Evidence.ToolInvocations = current.Evidence.ToolInvocations
	if run.Revision <= current.Revision {
		run.Revision = current.Revision + 1
	}
	payload, err := json.Marshal(run)
	if err != nil {
		return err
	}
	next := timeMillis(run.NextAttemptAt)
	query := "UPDATE " + s.store.TableIdentifier("agent_task_runs") + " SET " + s.store.Identifier("status") + " = " + s.store.Placeholder(1) + ", " + s.store.Identifier("next_attempt_at") + " = " + s.store.Placeholder(2) + ", " + s.store.Identifier("payload_json") + " = " + s.store.Placeholder(3) + ", " + s.store.Identifier("updated_at") + " = " + s.store.Placeholder(4) + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(5) + " AND " + s.store.Identifier("run_id") + " = " + s.store.Placeholder(6) + " AND " + s.store.Identifier("lease_owner") + " = " + s.store.Placeholder(7) + " AND " + s.store.Identifier("fencing_token") + " = " + s.store.Placeholder(8) + " AND " + s.store.Identifier("status") + " = " + s.store.Placeholder(9)
	result, err := tx.ExecContext(ctx, query, string(run.Status), next, payload, run.UpdatedAt.UnixMilli(), run.WorkspaceID, run.ID, owner, token, string(agentmodel.AgentTaskRunRunning))
	if err != nil {
		return err
	}
	updated, rowsErr := agentTaskExactlyOneRow(result)
	if rowsErr != nil {
		return rowsErr
	}
	if !updated {
		return apperror.New(apperror.KindConflict, "agent.task.terminal_fence_rejected", nil, nil)
	}
	return tx.Commit()
}

func (s *AgentTaskRunStore) SaveWaitingApproval(ctx context.Context, run agentmodel.AgentTaskRun, expectedRevision int64) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	read := "SELECT " + s.store.Identifier("payload_json") + " FROM " + s.store.TableIdentifier("agent_task_runs") + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("run_id") + " = " + s.store.Placeholder(2) + " AND " + s.store.Identifier("status") + " = " + s.store.Placeholder(3)
	var payload []byte
	if err := tx.QueryRowContext(ctx, read, run.WorkspaceID, run.ID, string(agentmodel.AgentTaskRunWaitingApproval)).Scan(&payload); err == sql.ErrNoRows {
		return apperror.New(apperror.KindConflict, "agent.task.approval_state_conflict", nil, nil)
	} else if err != nil {
		return err
	}
	var current agentmodel.AgentTaskRun
	if err := json.Unmarshal(payload, &current); err != nil {
		return err
	}
	if current.Revision != expectedRevision {
		return apperror.New(apperror.KindConflict, "agent.task.approval_state_conflict", nil, nil)
	}
	if current.Approval == nil {
		return apperror.New(apperror.KindConflict, "agent.task.approval_state_conflict", nil, nil)
	}
	if run.Approval == nil {
		return apperror.New(apperror.KindConflict, "agent.task.approval_state_conflict", nil, nil)
	}
	if current.Approval.ProposalID != run.Approval.ProposalID {
		return apperror.New(apperror.KindConflict, "agent.task.approval_state_conflict", nil, nil)
	}
	payload, err = json.Marshal(run)
	if err != nil {
		return err
	}
	query := "UPDATE " + s.store.TableIdentifier("agent_task_runs") + " SET " + s.store.Identifier("status") + " = " + s.store.Placeholder(1) + ", " + s.store.Identifier("payload_json") + " = " + s.store.Placeholder(2) + ", " + s.store.Identifier("updated_at") + " = " + s.store.Placeholder(3) + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(4) + " AND " + s.store.Identifier("run_id") + " = " + s.store.Placeholder(5) + " AND " + s.store.Identifier("status") + " = " + s.store.Placeholder(6)
	result, err := tx.ExecContext(ctx, query, string(run.Status), payload, run.UpdatedAt.UnixMilli(), run.WorkspaceID, run.ID, string(agentmodel.AgentTaskRunWaitingApproval))
	if err != nil {
		return err
	}
	updated, rowsErr := agentTaskExactlyOneRow(result)
	if rowsErr != nil {
		return rowsErr
	}
	if !updated {
		return apperror.New(apperror.KindConflict, "agent.task.approval_state_conflict", nil, nil)
	}
	return tx.Commit()
}

func (s *AgentTaskRunStore) SaveTerminalOverride(ctx context.Context, run agentmodel.AgentTaskRun, expectedRevision int64) error {
	if !run.Status.Terminal() {
		return apperror.New(apperror.KindConflict, "agent.task.override_terminal_required", nil, nil)
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	read := "SELECT " + s.store.Identifier("payload_json") + " FROM " + s.store.TableIdentifier("agent_task_runs") + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("run_id") + " = " + s.store.Placeholder(2) + " AND " + s.store.Identifier("status") + " = " + s.store.Placeholder(3)
	var payload []byte
	if err := tx.QueryRowContext(ctx, read, run.WorkspaceID, run.ID, string(run.Status)).Scan(&payload); err == sql.ErrNoRows {
		return apperror.New(apperror.KindConflict, "agent.task.override_state_conflict", nil, nil)
	} else if err != nil {
		return err
	}
	var current agentmodel.AgentTaskRun
	if err := json.Unmarshal(payload, &current); err != nil {
		return err
	}
	if current.Revision != expectedRevision {
		return apperror.New(apperror.KindConflict, "agent.task.override_state_conflict", nil, nil)
	}
	payload, err = json.Marshal(run)
	if err != nil {
		return err
	}
	query := "UPDATE " + s.store.TableIdentifier("agent_task_runs") + " SET " + s.store.Identifier("payload_json") + " = " + s.store.Placeholder(1) + ", " + s.store.Identifier("updated_at") + " = " + s.store.Placeholder(2) + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(3) + " AND " + s.store.Identifier("run_id") + " = " + s.store.Placeholder(4) + " AND " + s.store.Identifier("status") + " = " + s.store.Placeholder(5)
	result, err := tx.ExecContext(ctx, query, payload, run.UpdatedAt.UnixMilli(), run.WorkspaceID, run.ID, string(run.Status))
	if err != nil {
		return err
	}
	updated, rowsErr := agentTaskExactlyOneRow(result)
	if rowsErr != nil {
		return rowsErr
	}
	if !updated {
		return apperror.New(apperror.KindConflict, "agent.task.override_state_conflict", nil, nil)
	}
	return tx.Commit()
}

func (s *AgentTaskRunStore) RequestCancel(ctx context.Context, workspaceID, runID, reason string, now time.Time) (agentmodel.AgentTaskRun, bool, error) {
	run, found, err := s.Get(ctx, workspaceID, runID)
	if err != nil || !found {
		return run, false, err
	}
	if run.Status.Terminal() || run.CancelRequestedAt != nil {
		return run, true, nil
	}
	previousUpdatedAt := run.UpdatedAt
	run.CancelRequestedAt, run.CancellationReason, run.UpdatedAt, run.Revision = &now, reason, now, run.Revision+1
	if run.Status == agentmodel.AgentTaskRunPending || run.Status == agentmodel.AgentTaskRunRetryScheduled {
		run.Status, run.CompletedAt = agentmodel.AgentTaskRunCancelled, &now
	}
	payload, _ := json.Marshal(run)
	query := "UPDATE " + s.store.TableIdentifier("agent_task_runs") + " SET " + s.store.Identifier("status") + " = " + s.store.Placeholder(1) + ", " + s.store.Identifier("payload_json") + " = " + s.store.Placeholder(2) + ", " + s.store.Identifier("updated_at") + " = " + s.store.Placeholder(3) + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(4) + " AND " + s.store.Identifier("run_id") + " = " + s.store.Placeholder(5) + " AND " + s.store.Identifier("updated_at") + " = " + s.store.Placeholder(6)
	result, err := s.db.ExecContext(ctx, query, string(run.Status), payload, now.UnixMilli(), workspaceID, runID, previousUpdatedAt.UnixMilli())
	if err != nil {
		return agentmodel.AgentTaskRun{}, false, err
	}
	updated, rowsErr := agentTaskExactlyOneRow(result)
	if rowsErr != nil {
		return agentmodel.AgentTaskRun{}, false, rowsErr
	}
	if !updated {
		latest, _, getErr := s.Get(ctx, workspaceID, runID)
		return latest, true, getErr
	}
	return run, false, nil
}

func (s *AgentTaskRunStore) SaveOperationalTransition(ctx context.Context, run agentmodel.AgentTaskRun, expectedStatus agentmodel.AgentTaskRunStatus, expectedRevision int64) error {
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	read := "SELECT " + s.store.Identifier("payload_json") + " FROM " + s.store.TableIdentifier("agent_task_runs") + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("run_id") + " = " + s.store.Placeholder(2) + " AND " + s.store.Identifier("status") + " = " + s.store.Placeholder(3)
	var currentPayload []byte
	if err := tx.QueryRowContext(ctx, read, run.WorkspaceID, run.ID, string(expectedStatus)).Scan(&currentPayload); err == sql.ErrNoRows {
		return apperror.New(apperror.KindConflict, "agent.task.operation_state_conflict", nil, nil)
	} else if err != nil {
		return err
	}
	var current agentmodel.AgentTaskRun
	if json.Unmarshal(currentPayload, &current) != nil || current.Revision != expectedRevision {
		return apperror.New(apperror.KindConflict, "agent.task.operation_state_conflict", nil, nil)
	}
	payload, err := json.Marshal(run)
	if err != nil {
		return err
	}
	query := "UPDATE " + s.store.TableIdentifier("agent_task_runs") + " SET " + s.store.Identifier("status") + " = " + s.store.Placeholder(1) + ", " + s.store.Identifier("payload_json") + " = " + s.store.Placeholder(2) + ", " + s.store.Identifier("updated_at") + " = " + s.store.Placeholder(3) + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(4) + " AND " + s.store.Identifier("run_id") + " = " + s.store.Placeholder(5) + " AND " + s.store.Identifier("status") + " = " + s.store.Placeholder(6)
	result, err := tx.ExecContext(ctx, query, string(run.Status), payload, run.UpdatedAt.UnixMilli(), run.WorkspaceID, run.ID, string(expectedStatus))
	if err != nil {
		return err
	}
	updated, rowsErr := agentTaskExactlyOneRow(result)
	if rowsErr != nil {
		return rowsErr
	}
	if !updated {
		return apperror.New(apperror.KindConflict, "agent.task.operation_state_conflict", nil, nil)
	}
	return tx.Commit()
}

func (s *AgentTaskRunStore) agentTaskColumns() string {
	columns := []string{"workspace_id", "run_id", "idempotency_key", "task_key", "process_id", "status", "lease_owner", "fencing_token", "lease_expires_at", "next_attempt_at", "payload_json", "created_at", "updated_at"}
	for index := range columns {
		columns[index] = s.store.Identifier(columns[index])
	}
	return strings.Join(columns, ", ")
}

func (s *AgentTaskRunStore) placeholders(count int) string {
	values := make([]string, count)
	for index := range values {
		values[index] = s.store.Placeholder(index + 1)
	}
	return strings.Join(values, ", ")
}

func timeMillis(value *time.Time) int64 {
	if value == nil {
		return 0
	}
	return value.UTC().UnixMilli()
}

func agentTaskExactlyOneRow(result sql.Result) (bool, error) {
	rows, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return rows == 1, nil
}

var _ agentrepository.AgentTaskRunRepository = (*AgentTaskRunStore)(nil)
var _ agentrepository.AgentToolCallLedger = (*AgentTaskRunStore)(nil)

func (s *AgentTaskRunStore) BeginAgentToolCall(ctx context.Context, start agentrepository.AgentToolCallStart) (string, int, error) {
	if err := s.EnsureSchema(ctx); err != nil {
		return "", 0, err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = tx.Rollback() }()
	query := "SELECT " + s.store.Identifier("payload_json") + ", " + s.store.Identifier("status") + ", " + s.store.Identifier("lease_owner") + ", " + s.store.Identifier("fencing_token") + " FROM " + s.store.TableIdentifier("agent_task_runs") + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("run_id") + " = " + s.store.Placeholder(2)
	var payload []byte
	var status, owner string
	var token int64
	if err := tx.QueryRowContext(ctx, query, start.WorkspaceID, start.TaskRunID).Scan(&payload, &status, &owner, &token); err != nil {
		return "", 0, err
	}
	if status != string(agentmodel.AgentTaskRunRunning) || owner != start.Owner || token != start.FencingToken {
		return "", 0, apperror.New(apperror.KindConflict, "agent.task.tool_fence_rejected", nil, nil)
	}
	var run agentmodel.AgentTaskRun
	if err := json.Unmarshal(payload, &run); err != nil {
		return "", 0, err
	}
	if start.MaxToolCalls <= 0 || run.ToolCallCount >= start.MaxToolCalls {
		return "", run.ToolCallCount, apperror.New(apperror.KindRateLimited, "agent.task.tool_call_limit", nil, nil)
	}
	usedCost := 0
	for _, invocation := range run.Evidence.ToolInvocations {
		usedCost += invocation.CostUnits
	}
	if start.CostUnits <= 0 || start.MaxCostUnits <= 0 || usedCost+start.CostUnits > start.MaxCostUnits {
		return "", run.ToolCallCount, apperror.New(apperror.KindRateLimited, "agent.task.cost_budget_exceeded", nil, nil)
	}
	run.ToolCallCount++
	run.Revision++
	run.UpdatedAt = time.Now().UTC()
	ref := fmt.Sprintf("agent_tool_%s_%d", run.ID, run.ToolCallCount)
	run.Evidence.Authorization = append(run.Evidence.Authorization, start.Authorization)
	run.Evidence.ToolInvocationRefs = append(run.Evidence.ToolInvocationRefs, ref)
	run.Evidence.ToolInvocations = append(run.Evidence.ToolInvocations, agentmodel.AgentTaskToolInvocationEvidence{Ref: ref, Tool: start.Tool, InputHash: start.InputHash, Status: "running", Authorization: start.Authorization, StartedAt: run.UpdatedAt, CostUnits: start.CostUnits})
	updated, _ := json.Marshal(run)
	update := "UPDATE " + s.store.TableIdentifier("agent_task_runs") + " SET " + s.store.Identifier("payload_json") + " = " + s.store.Placeholder(1) + ", " + s.store.Identifier("updated_at") + " = " + s.store.Placeholder(2) + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(3) + " AND " + s.store.Identifier("run_id") + " = " + s.store.Placeholder(4) + " AND " + s.store.Identifier("status") + " = " + s.store.Placeholder(5) + " AND " + s.store.Identifier("lease_owner") + " = " + s.store.Placeholder(6) + " AND " + s.store.Identifier("fencing_token") + " = " + s.store.Placeholder(7)
	result, err := tx.ExecContext(ctx, update, updated, run.UpdatedAt.UnixMilli(), start.WorkspaceID, start.TaskRunID, string(agentmodel.AgentTaskRunRunning), start.Owner, start.FencingToken)
	if err != nil {
		return "", 0, err
	}
	oneRow, rowsErr := agentTaskExactlyOneRow(result)
	if rowsErr != nil {
		return "", 0, rowsErr
	}
	if !oneRow {
		return "", 0, apperror.New(apperror.KindConflict, "agent.task.tool_fence_rejected", nil, nil)
	}
	if err := tx.Commit(); err != nil {
		return "", 0, err
	}
	return ref, run.ToolCallCount, nil
}

func (s *AgentTaskRunStore) FinishAgentToolCall(ctx context.Context, finish agentrepository.AgentToolCallFinish) error {
	if err := s.EnsureSchema(ctx); err != nil {
		return err
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	query := "SELECT " + s.store.Identifier("payload_json") + ", " + s.store.Identifier("status") + ", " + s.store.Identifier("lease_owner") + ", " + s.store.Identifier("fencing_token") + " FROM " + s.store.TableIdentifier("agent_task_runs") + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(1) + " AND " + s.store.Identifier("run_id") + " = " + s.store.Placeholder(2)
	var payload []byte
	var status, owner string
	var token int64
	if err := tx.QueryRowContext(ctx, query, finish.WorkspaceID, finish.TaskRunID).Scan(&payload, &status, &owner, &token); err != nil {
		return err
	}
	if status != string(agentmodel.AgentTaskRunRunning) || owner != finish.Owner || token != finish.FencingToken {
		return apperror.New(apperror.KindConflict, "agent.task.tool_fence_rejected", nil, nil)
	}
	var run agentmodel.AgentTaskRun
	if err := json.Unmarshal(payload, &run); err != nil {
		return err
	}
	now := time.Now().UTC()
	found := false
	for index := range run.Evidence.ToolInvocations {
		item := &run.Evidence.ToolInvocations[index]
		if item.Ref != finish.CallRef {
			continue
		}
		if item.FinishedAt != nil {
			return nil
		}
		item.Status, item.ErrorCode, item.FinishedAt = finish.Status, finish.ErrorCode, &now
		item.DurationMilliseconds = now.Sub(item.StartedAt).Milliseconds()
		item.OutputHash = strings.TrimSpace(fmt.Sprint(finish.Evidence["output_hash"]))
		found = true
		break
	}
	if !found {
		return apperror.New(apperror.KindNotFound, "agent.task.tool_call_not_found", nil, nil)
	}
	run.UpdatedAt = now
	run.Revision++
	updated, _ := json.Marshal(run)
	update := "UPDATE " + s.store.TableIdentifier("agent_task_runs") + " SET " + s.store.Identifier("payload_json") + " = " + s.store.Placeholder(1) + ", " + s.store.Identifier("updated_at") + " = " + s.store.Placeholder(2) + " WHERE " + s.store.Identifier("workspace_id") + " = " + s.store.Placeholder(3) + " AND " + s.store.Identifier("run_id") + " = " + s.store.Placeholder(4) + " AND " + s.store.Identifier("status") + " = " + s.store.Placeholder(5) + " AND " + s.store.Identifier("lease_owner") + " = " + s.store.Placeholder(6) + " AND " + s.store.Identifier("fencing_token") + " = " + s.store.Placeholder(7)
	result, err := tx.ExecContext(ctx, update, updated, now.UnixMilli(), finish.WorkspaceID, finish.TaskRunID, string(agentmodel.AgentTaskRunRunning), finish.Owner, finish.FencingToken)
	if err != nil {
		return err
	}
	oneRow, rowsErr := agentTaskExactlyOneRow(result)
	if rowsErr != nil {
		return rowsErr
	}
	if !oneRow {
		return apperror.New(apperror.KindConflict, "agent.task.tool_fence_rejected", nil, nil)
	}
	return tx.Commit()
}
