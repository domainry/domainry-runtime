package workflow

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	ormdriver "github.com/domainry/domainry-orm/driver"
	"github.com/domainry/domainry-orm/query"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowcontract "github.com/domainry/domainry-runtime/runtime/domain/workflow/contract"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/timevalue"
)

func (r WorkflowProcessStore) ListApprovalTasks(ctx context.Context, workspaceID, processID, nodeInstanceID string) ([]workflowmodel.WorkflowTask, error) {
	workspaceID, err := requireWorkflowWorkspaceID(workspaceID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(processID) == "" || strings.TrimSpace(nodeInstanceID) == "" {
		return nil, fmt.Errorf("workflow approval process and node instance are required")
	}
	statement, args, err := query.NewWorkspaceSelectBuilder(r.store.SQLRenderer, "_workflow_tasks", workspaceID).
		Columns(workflowTaskColumns()...).Where(query.And(query.Equal("process_id", processID), query.Equal("node_instance_id", nodeInstanceID))).
		OrderBy(query.Ascending("sequence"), query.Ascending("id")).Build()
	if err != nil {
		return nil, err
	}
	rows, err := r.database().QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	tasks := []workflowmodel.WorkflowTask{}
	for rows.Next() {
		task, err := scanWorkflowTask(rows)
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, task)
	}
	return tasks, rows.Err()
}

// Acquire the process revision before writing the task, so approvals computed
// from the same snapshot cannot both commit stale counts or continuations.
func (r WorkflowDecisionStore) guardApprovalSnapshotTx(ctx context.Context, tx *sql.Tx, commit transactionmodel.WorkflowDecisionCommit) error {
	if commit.ExpectedProcessUpdatedAt == "" {
		return nil
	}
	process := commit.Process
	if process == nil || process.ID != commit.DecidedTask.ProcessID || process.UpdatedAt == commit.ExpectedProcessUpdatedAt {
		return fmt.Errorf("workflow decision requires an advanced process snapshot")
	}
	statement, args, err := query.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "_workflow_process_instances", commit.WorkspaceID).
		Set("updated_at", timevalue.Millis(process.UpdatedAt)).
		Where(query.And(query.Equal("id", process.ID), query.Equal("updated_at", timevalue.Millis(commit.ExpectedProcessUpdatedAt)), query.Equal("status", "waiting"))).Build()
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, statement, args...)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return workflowcontract.ErrWorkflowDecisionSnapshotChanged
	}
	return nil
}

var _ workflowcontract.WorkflowApprovalTaskReader = WorkflowProcessStore{}

func (r WorkflowDecisionStore) approvalSnapshotError(err error) error {
	kind := r.store.Engine.ClassifyError(err)
	if kind == ormdriver.ErrorSerialization || kind == ormdriver.ErrorDeadlock || r.store.Driver() == "sqlite" && kind == ormdriver.ErrorUnavailable {
		return fmt.Errorf("%w: %w", workflowcontract.ErrWorkflowDecisionSnapshotChanged, err)
	}
	return err
}
