package agent

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/requestcontext"
	ormbuilder "github.com/domainry/domainry-orm/builder"
	agentmodel "github.com/domainry/domainry-runtime/runtime/domain/agent/model"
	agentrepository "github.com/domainry/domainry-runtime/runtime/domain/agent/repository"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type AgentTaskRunStore struct {
	store *database.RuntimeStore
	db    *sql.DB
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
	return &AgentTaskRunStore{store: store, db: store.DB()}
}

// BackfillWorkerScopes inventories legacy Agent tasks once during Runtime
// startup. It is deliberately not part of EnsureSchema: ordinary Agent reads
// and writes must not perform a global workspace inventory.
func (s *AgentTaskRunStore) BackfillWorkerScopes(ctx context.Context) error {
	query, args, buildErr := ormbuilder.NewSelectBuilder(s.store.SQLRenderer, "agent_task_runs").Projections(
		ormbuilder.Project(ormbuilder.Column("workspace_id")),
		ormbuilder.Project(ormbuilder.Max(ormbuilder.Column("updated_at"))),
	).GroupBy(ormbuilder.Column("workspace_id")).Build()
	if buildErr != nil {
		return buildErr
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
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
		if err := RegisterAgentTaskWorkerScope(ctx, s.store, s.db, scope.workspaceID, time.UnixMilli(scope.updatedAtMillis)); err != nil {
			return err
		}
	}
	return nil
}

func (s *AgentTaskRunStore) Create(ctx context.Context, run agentmodel.AgentTaskRun) (agentmodel.AgentTaskRun, bool, error) {
	payload, err := json.Marshal(run)
	if err != nil {
		return agentmodel.AgentTaskRun{}, false, err
	}
	statement, args, buildErr := ormbuilder.NewWorkspaceInsertBuilder(s.store.SQLRenderer, "agent_task_runs", run.WorkspaceID).
		Columns(agentTaskRunColumns()...).Values(run.ID, run.IdempotencyKey, run.TaskKey, run.ProcessID, string(run.Status), "", int64(0), int64(0), timeMillis(run.NextAttemptAt), payload, run.CreatedAt.UnixMilli(), run.UpdatedAt.UnixMilli()).Build()
	if buildErr != nil {
		return agentmodel.AgentTaskRun{}, false, buildErr
	}
	_, err = s.db.ExecContext(ctx, statement, args...)
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
	statement, args, err := ormbuilder.NewWorkspaceSelectBuilder(s.store.SQLRenderer, "agent_task_runs", strings.TrimSpace(workspaceID)).
		Columns("payload_json").Where(ormbuilder.Equal("run_id", strings.TrimSpace(runID))).Limit(1).Build()
	if err != nil {
		return agentmodel.AgentTaskRun{}, false, err
	}
	return s.scanRun(s.db.QueryRowContext(ctx, statement, args...))
}

func (s *AgentTaskRunStore) getByIdempotency(ctx context.Context, workspaceID, key string) (agentmodel.AgentTaskRun, bool, error) {
	statement, args, err := ormbuilder.NewWorkspaceSelectBuilder(s.store.SQLRenderer, "agent_task_runs", workspaceID).
		Columns("payload_json").Where(ormbuilder.Equal("idempotency_key", key)).Limit(1).Build()
	if err != nil {
		return agentmodel.AgentTaskRun{}, false, err
	}
	return s.scanRun(s.db.QueryRowContext(ctx, statement, args...))
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
	predicates := []ormbuilder.Predicate{}
	if filter.ProcessID != "" {
		predicates = append(predicates, ormbuilder.Equal("process_id", strings.TrimSpace(filter.ProcessID)))
	}
	if filter.TaskKey != "" {
		predicates = append(predicates, ormbuilder.Equal("task_key", strings.TrimSpace(filter.TaskKey)))
	}
	if len(filter.Statuses) > 0 {
		values := make([]any, len(filter.Statuses))
		for index, status := range filter.Statuses {
			values[index] = string(status)
		}
		predicates = append(predicates, ormbuilder.In("status", values...))
	}
	limit := filter.Limit
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	builder := ormbuilder.NewWorkspaceSelectBuilder(s.store.SQLRenderer, "agent_task_runs", strings.TrimSpace(workspaceID)).Columns("payload_json")
	if len(predicates) > 0 {
		builder = builder.Where(ormbuilder.And(predicates...))
	}
	statement, args, buildErr := builder.OrderBy(ormbuilder.Ascending("created_at")).Limit(limit).Build()
	if buildErr != nil {
		return nil, buildErr
	}
	rows, err := s.db.QueryContext(ctx, statement, args...)
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
	ctx = requestcontext.WithWorkspaceID(ctx, strings.TrimSpace(workspaceID))
	ctx = requestcontext.WithActorID(ctx, strings.TrimSpace(owner))
	var lastErr error
	for attempt := 0; attempt < 16; attempt++ {
		claim, found, err := s.claimNextOnce(ctx, workspaceID, owner, now, duration)
		if err == nil {
			return claim, found, nil
		}
		if !s.store.IsTransientError(err) {
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
		if !s.store.IsTransientError(err) {
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
	query, queryArgs, buildErr := ormbuilder.NewWorkspaceSelectBuilder(s.store.SQLRenderer, "agent_task_runs", workspaceID).
		Columns("payload_json", "fencing_token").Where(ormbuilder.And(
		ormbuilder.Equal("run_id", runID), agentTaskEligiblePredicate(now),
	)).Limit(1).Build()
	if buildErr != nil {
		return agentrepository.AgentTaskClaim{}, false, buildErr
	}
	var payload []byte
	var priorToken int64
	err = tx.QueryRowContext(ctx, query, queryArgs...).Scan(&payload, &priorToken)
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
	update, updateArgs, buildErr := agentTaskClaimUpdate(s.store, workspaceID, runID, owner, token, priorToken, expires, now, updatedPayload)
	if buildErr != nil {
		return agentrepository.AgentTaskClaim{}, false, buildErr
	}
	result, err := tx.ExecContext(ctx, update, updateArgs...)
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
	query, queryArgs, buildErr := ormbuilder.NewWorkspaceSelectBuilder(s.store.SQLRenderer, "agent_task_runs", workspaceID).
		Columns("run_id", "payload_json", "fencing_token").Where(agentTaskEligiblePredicate(now)).
		OrderBy(ormbuilder.Ascending("created_at")).Limit(1).Build()
	if buildErr != nil {
		return agentrepository.AgentTaskClaim{}, false, buildErr
	}
	var runID string
	var payload []byte
	var priorToken int64
	err = tx.QueryRowContext(ctx, query, queryArgs...).Scan(&runID, &payload, &priorToken)
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
	update, updateArgs, buildErr := agentTaskClaimUpdate(s.store, workspaceID, runID, owner, token, priorToken, expires, now, updatedPayload)
	if buildErr != nil {
		return agentrepository.AgentTaskClaim{}, false, buildErr
	}
	result, err := tx.ExecContext(ctx, update, updateArgs...)
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

func (s *AgentTaskRunStore) Heartbeat(ctx context.Context, workspaceID, runID string, owner string, token int64, now time.Time, duration time.Duration) (agentrepository.AgentTaskHeartbeatResult, error) {
	expires := now.Add(duration)
	statement, args, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "agent_task_runs", workspaceID).
		Set("lease_expires_at", expires.UnixMilli()).Set("updated_at", now.UnixMilli()).Where(ormbuilder.And(
		ormbuilder.Equal("run_id", runID), ormbuilder.Equal("status", string(agentmodel.AgentTaskRunRunning)),
		ormbuilder.Equal("lease_owner", owner), ormbuilder.Equal("fencing_token", token), ormbuilder.GreaterThan("lease_expires_at", now.UnixMilli()),
	)).Build()
	if buildErr != nil {
		return agentrepository.AgentTaskHeartbeatResult{}, buildErr
	}
	result, err := s.db.ExecContext(ctx, statement, args...)
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
	persist, persistArgs, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "agent_task_runs", workspaceID).
		Set("payload_json", payload).Where(agentTaskLeasePredicate(runID, owner, token)).Build()
	if buildErr != nil {
		return agentrepository.AgentTaskHeartbeatResult{}, buildErr
	}
	if _, err := s.db.ExecContext(ctx, persist, persistArgs...); err != nil {
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
	read, readArgs, buildErr := ormbuilder.NewWorkspaceSelectBuilder(s.store.SQLRenderer, "agent_task_runs", run.WorkspaceID).
		Columns("payload_json").Where(agentTaskLeasePredicate(run.ID, owner, token)).Limit(1).Build()
	if buildErr != nil {
		return buildErr
	}
	var currentPayload []byte
	if err := tx.QueryRowContext(ctx, read, readArgs...).Scan(&currentPayload); err == sql.ErrNoRows {
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
	statement, args, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "agent_task_runs", run.WorkspaceID).
		Set("status", string(run.Status)).Set("next_attempt_at", next).Set("payload_json", payload).Set("updated_at", run.UpdatedAt.UnixMilli()).
		Where(agentTaskLeasePredicate(run.ID, owner, token)).Build()
	if buildErr != nil {
		return buildErr
	}
	result, err := tx.ExecContext(ctx, statement, args...)
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
	read, readArgs, buildErr := agentTaskPayloadSelect(s.store, run.WorkspaceID, run.ID, string(agentmodel.AgentTaskRunWaitingApproval))
	if buildErr != nil {
		return buildErr
	}
	var payload []byte
	if err := tx.QueryRowContext(ctx, read, readArgs...).Scan(&payload); err == sql.ErrNoRows {
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
	statement, args, buildErr := agentTaskStatusUpdate(s.store, run, payload, agentmodel.AgentTaskRunWaitingApproval)
	if buildErr != nil {
		return buildErr
	}
	result, err := tx.ExecContext(ctx, statement, args...)
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
	read, readArgs, buildErr := agentTaskPayloadSelect(s.store, run.WorkspaceID, run.ID, string(run.Status))
	if buildErr != nil {
		return buildErr
	}
	var payload []byte
	if err := tx.QueryRowContext(ctx, read, readArgs...).Scan(&payload); err == sql.ErrNoRows {
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
	statement, args, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "agent_task_runs", run.WorkspaceID).
		Set("payload_json", payload).Set("updated_at", run.UpdatedAt.UnixMilli()).Where(agentTaskStatusPredicate(run.ID, run.Status)).Build()
	if buildErr != nil {
		return buildErr
	}
	result, err := tx.ExecContext(ctx, statement, args...)
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
	statement, args, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "agent_task_runs", workspaceID).
		Set("status", string(run.Status)).Set("payload_json", payload).Set("updated_at", now.UnixMilli()).Where(ormbuilder.And(
		ormbuilder.Equal("run_id", runID), ormbuilder.Equal("updated_at", previousUpdatedAt.UnixMilli()),
	)).Build()
	if buildErr != nil {
		return agentmodel.AgentTaskRun{}, false, buildErr
	}
	result, err := s.db.ExecContext(ctx, statement, args...)
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
	read, readArgs, buildErr := agentTaskPayloadSelect(s.store, run.WorkspaceID, run.ID, string(expectedStatus))
	if buildErr != nil {
		return buildErr
	}
	var currentPayload []byte
	if err := tx.QueryRowContext(ctx, read, readArgs...).Scan(&currentPayload); err == sql.ErrNoRows {
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
	statement, args, buildErr := agentTaskStatusUpdate(s.store, run, payload, expectedStatus)
	if buildErr != nil {
		return buildErr
	}
	result, err := tx.ExecContext(ctx, statement, args...)
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

func agentTaskRunColumns() []string {
	return []string{"run_id", "idempotency_key", "task_key", "process_id", "status", "lease_owner", "fencing_token", "lease_expires_at", "next_attempt_at", "payload_json", "created_at", "updated_at"}
}

func agentTaskStatusPredicate(runID string, status agentmodel.AgentTaskRunStatus) ormbuilder.Predicate {
	return ormbuilder.And(ormbuilder.Equal("run_id", runID), ormbuilder.Equal("status", string(status)))
}

func agentTaskLeasePredicate(runID, owner string, token int64) ormbuilder.Predicate {
	return ormbuilder.And(
		agentTaskStatusPredicate(runID, agentmodel.AgentTaskRunRunning),
		ormbuilder.Equal("lease_owner", owner),
		ormbuilder.Equal("fencing_token", token),
	)
}

func agentTaskEligiblePredicate(now time.Time) ormbuilder.Predicate {
	return ormbuilder.Or(
		ormbuilder.And(
			ormbuilder.In("status", string(agentmodel.AgentTaskRunPending), string(agentmodel.AgentTaskRunRetryScheduled)),
			ormbuilder.LessThanOrEqual("next_attempt_at", now.UnixMilli()),
		),
		ormbuilder.And(
			ormbuilder.Equal("status", string(agentmodel.AgentTaskRunRunning)),
			ormbuilder.LessThanOrEqual("lease_expires_at", now.UnixMilli()),
		),
	)
}

func agentTaskClaimUpdate(store *database.RuntimeStore, workspaceID, runID, owner string, token, priorToken int64, expires, now time.Time, payload []byte) (string, []any, error) {
	return ormbuilder.NewWorkspaceUpdateBuilder(store.SQLRenderer, "agent_task_runs", workspaceID).
		Set("status", string(agentmodel.AgentTaskRunRunning)).Set("lease_owner", owner).Set("fencing_token", token).
		Set("lease_expires_at", expires.UnixMilli()).Set("payload_json", payload).Set("updated_at", now.UnixMilli()).
		Where(ormbuilder.And(
			ormbuilder.Equal("run_id", runID), ormbuilder.Equal("fencing_token", priorToken), agentTaskEligiblePredicate(now),
		)).Build()
}

func agentTaskPayloadSelect(store *database.RuntimeStore, workspaceID, runID, status string) (string, []any, error) {
	return ormbuilder.NewWorkspaceSelectBuilder(store.SQLRenderer, "agent_task_runs", workspaceID).
		Columns("payload_json").Where(ormbuilder.And(
		ormbuilder.Equal("run_id", runID), ormbuilder.Equal("status", status),
	)).Limit(1).Build()
}

func agentTaskStatusUpdate(store *database.RuntimeStore, run agentmodel.AgentTaskRun, payload []byte, expectedStatus agentmodel.AgentTaskRunStatus) (string, []any, error) {
	return ormbuilder.NewWorkspaceUpdateBuilder(store.SQLRenderer, "agent_task_runs", run.WorkspaceID).
		Set("status", string(run.Status)).Set("payload_json", payload).Set("updated_at", run.UpdatedAt.UnixMilli()).
		Where(agentTaskStatusPredicate(run.ID, expectedStatus)).Build()
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
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return "", 0, err
	}
	defer func() { _ = tx.Rollback() }()
	query, queryArgs, buildErr := ormbuilder.NewWorkspaceSelectBuilder(s.store.SQLRenderer, "agent_task_runs", start.WorkspaceID).
		Columns("payload_json", "status", "lease_owner", "fencing_token").Where(ormbuilder.Equal("run_id", start.TaskRunID)).Limit(1).Build()
	if buildErr != nil {
		return "", 0, buildErr
	}
	var payload []byte
	var status, owner string
	var token int64
	if err := tx.QueryRowContext(ctx, query, queryArgs...).Scan(&payload, &status, &owner, &token); err != nil {
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
	update, updateArgs, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "agent_task_runs", start.WorkspaceID).
		Set("payload_json", updated).Set("updated_at", run.UpdatedAt.UnixMilli()).Where(agentTaskLeasePredicate(start.TaskRunID, start.Owner, start.FencingToken)).Build()
	if buildErr != nil {
		return "", 0, buildErr
	}
	result, err := tx.ExecContext(ctx, update, updateArgs...)
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
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	query, queryArgs, buildErr := ormbuilder.NewWorkspaceSelectBuilder(s.store.SQLRenderer, "agent_task_runs", finish.WorkspaceID).
		Columns("payload_json", "status", "lease_owner", "fencing_token").Where(ormbuilder.Equal("run_id", finish.TaskRunID)).Limit(1).Build()
	if buildErr != nil {
		return buildErr
	}
	var payload []byte
	var status, owner string
	var token int64
	if err := tx.QueryRowContext(ctx, query, queryArgs...).Scan(&payload, &status, &owner, &token); err != nil {
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
	update, updateArgs, buildErr := ormbuilder.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "agent_task_runs", finish.WorkspaceID).
		Set("payload_json", updated).Set("updated_at", now.UnixMilli()).Where(agentTaskLeasePredicate(finish.TaskRunID, finish.Owner, finish.FencingToken)).Build()
	if buildErr != nil {
		return buildErr
	}
	result, err := tx.ExecContext(ctx, update, updateArgs...)
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
