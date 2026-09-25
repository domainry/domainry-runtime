package database

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-orm/query"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/timevalue"
)

// ApplyWorkflowWithdrawalTx is shared by Action and native Workflow storage.
// The caller owns rollback: every child, event and business mutation commits
// together. No task list limit can leave uncancelled children behind.
func ApplyWorkflowWithdrawalTx(ctx context.Context, store *RuntimeStore, tx ActionExecutionExecutor, workspaceID string, commit transactionmodel.WorkflowWithdrawalCommit) error {
	p := commit.Process
	if strings.TrimSpace(workspaceID) == "" || p.WorkspaceID != workspaceID || p.ID == "" || p.InitiatorID != commit.ActorID || commit.ActorID == "" || commit.CommandID == "" || p.Status != "cancelled" || commit.ExpectedStatus != "waiting" || commit.ExpectedUpdatedAt == "" || p.UpdatedAt == commit.ExpectedUpdatedAt {
		return fmt.Errorf("invalid workflow withdrawal commit")
	}
	variables, err := json.Marshal(NonNilMap(p.Variables))
	if err != nil {
		return err
	}
	result, err := json.Marshal(NonNilMap(p.Result))
	if err != nil {
		return err
	}
	statement, args, err := query.NewWorkspaceUpdateBuilder(store.SQLRenderer, "_workflow_process_instances", workspaceID).
		Set("status", p.Status).Set("current_node_ids_json", "[]").Set("variables_json", string(variables)).Set("result_json", string(result)).Set("updated_at", timevalue.Millis(p.UpdatedAt)).Set("completed_at", timevalue.Millis(p.CompletedAt)).
		Where(query.And(query.Equal("id", p.ID), query.Equal("workflow_key", p.WorkflowKey), query.Equal("initiator_id", commit.ActorID), query.Equal("status", commit.ExpectedStatus), query.Equal("updated_at", timevalue.Millis(commit.ExpectedUpdatedAt)))).Build()
	if err != nil {
		return err
	}
	applied, err := tx.ExecContext(ctx, statement, args...)
	if err != nil {
		return err
	}
	affected, err := applied.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return apperror.New(apperror.KindConflict, "backend.workflow.withdrawal_snapshot_changed", nil, nil)
	}
	for _, item := range []struct {
		table     string
		statuses  []any
		completed bool
	}{
		{"_workflow_tasks", []any{"open", "pending"}, true},
		{"_workflow_node_instances", []any{"waiting", "running", "pending"}, true},
		{"_workflow_route_steps", []any{"active", "pending", "configurable"}, false},
		{"_workflow_executions", []any{"pending", "running", "waiting", "retrying"}, false},
	} {
		builder := query.NewWorkspaceUpdateBuilder(store.SQLRenderer, item.table, workspaceID).Set("status", "cancelled")
		if item.table == "_workflow_node_instances" {
			builder.Set("completed_at", timevalue.Millis(p.CompletedAt))
		} else {
			builder.Set("updated_at", timevalue.Millis(p.UpdatedAt))
			if item.table == "_workflow_executions" {
				builder.Set("node_id", "").Set("next_run_at", int64(0)).Set("last_error", "").Set("lease_owner", "").Set("lease_expires_at", int64(0))
			}
			if item.completed {
				builder.Set("completed_at", timevalue.Millis(p.CompletedAt)).Set("completed_by", commit.ActorID)
			}
		}
		active := query.In("status", item.statuses...)
		if item.table == "_workflow_executions" {
			active = query.Or(active, query.And(query.Equal("status", "failed"), query.NotEqual("next_run_at", int64(0))))
		}
		builder.Where(query.And(query.Equal("process_id", p.ID), active))
		statement, args, err := builder.Build()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, statement, args...); err != nil {
			return err
		}
	}
	metadata, _ := json.Marshal(map[string]any{"command_id": commit.CommandID, "business_outcome": "withdrawn"})
	statement, args, err = query.NewWorkspaceInsertBuilder(store.SQLRenderer, "_workflow_process_events", workspaceID).
		Columns("id", "process_id", "node_id", "task_id", "event", "actor_id", "summary", "metadata_json", "created_at").
		Values(commit.CommandID, p.ID, "", "", "process_cancelled", commit.ActorID, "workflow.event.process.cancelled", string(metadata), timevalue.Millis(p.CompletedAt)).Build()
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, statement, args...)
	return err
}
