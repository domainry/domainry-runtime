package workflow

import (
	"context"
	"strings"

	"github.com/domainry/domainry-agent-sdk/modulehost"
	agentpersistence "github.com/domainry/domainry-agent-sdk/persistence"
	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-orm/query"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

type workflowAgentTransactionTestStore struct{ store *database.RuntimeStore }

func (s workflowAgentTransactionTestStore) InsertAgentTask(ctx context.Context, executor modulehost.Executor, run agentpersistence.AgentTaskMutation) error {
	columns := []string{"run_id", "idempotency_key", "task_key", "process_id", "status", "lease_owner", "fencing_token", "lease_expires_at", "next_attempt_at", "payload_json", "created_at", "updated_at"}
	statement, args, err := query.NewWorkspaceInsertBuilder(s.store.SQLRenderer, "_agent_task_runs", run.WorkspaceID).Columns(columns...).Values(run.RunID, run.IdempotencyKey, run.TaskKey, run.ProcessID, run.Status, run.LeaseOwner, run.FencingToken, run.LeaseExpiresAt, run.NextAttemptAt, run.Payload, run.CreatedAtMillis, run.UpdatedAtMillis).Build()
	if err != nil {
		return err
	}
	if _, err := executor.ExecContext(ctx, statement, args...); err != nil {
		return err
	}
	return nil
}

func (s workflowAgentTransactionTestStore) UpdateAgentTask(ctx context.Context, executor modulehost.Executor, run agentpersistence.AgentTaskMutation) error {
	expected := strings.TrimSpace(run.ExpectedStatus)
	if expected == "" {
		expected = "running"
	}
	predicate := query.And(query.Equal("run_id", run.RunID), query.Equal("status", expected))
	if expected == "running" {
		predicate = query.And(predicate, query.Equal("lease_owner", run.LeaseOwner), query.Equal("fencing_token", run.FencingToken))
	}
	statement, args, err := query.NewWorkspaceUpdateBuilder(s.store.SQLRenderer, "_agent_task_runs", run.WorkspaceID).Set("status", run.Status).Set("payload_json", run.Payload).Set("updated_at", run.UpdatedAtMillis).Where(predicate).Build()
	if err != nil {
		return err
	}
	result, err := executor.ExecContext(ctx, statement, args...)
	if err != nil {
		return err
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if rows != 1 {
		return apperror.New(apperror.KindConflict, "agent.task.terminal_fence_rejected", nil, nil)
	}
	return nil
}

func newAgentWorkflowDecisionStore(store *database.RuntimeStore) WorkflowDecisionStore {
	return NewWorkflowDecisionStore(store, workflowAgentTransactionTestStore{store: store})
}
