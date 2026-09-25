package workflow

import (
	"context"
	"fmt"

	"github.com/domainry/domainry-orm/query"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowcontract "github.com/domainry/domainry-runtime/runtime/domain/workflow/contract"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/timevalue"
)

var _ workflowcontract.WorkflowWithdrawalStore = WorkflowProcessStore{}

func (r WorkflowProcessStore) CommitWorkflowWithdrawal(ctx context.Context, commit transactionmodel.WorkflowWithdrawalCommit) error {
	tx, err := r.database().BeginTx(ctx, recordMutationTxOptions())
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := database.ApplyWorkflowWithdrawalTx(ctx, r.store, tx, commit.Process.WorkspaceID, commit); err != nil {
		return err
	}
	return tx.Commit()
}

// ClaimWorkflowTimer commits the waiting-process claim and completed timer
// node together. A node write failure cannot strand the process as running.
func (r WorkflowProcessStore) ClaimWorkflowTimer(ctx context.Context, workspaceID string, process workflowmodel.WorkflowProcessInstance, node workflowmodel.WorkflowNodeInstance, expectedUpdatedAt string) (bool, error) {
	workspaceID, err := requireWorkflowWorkspaceID(workspaceID)
	if err != nil {
		return false, err
	}
	if process.ID == "" || process.UpdatedAt == expectedUpdatedAt || expectedUpdatedAt == "" || process.Status != "running" || node.ProcessID != process.ID || node.Status != "success" {
		return false, fmt.Errorf("invalid workflow timer revision claim")
	}
	tx, err := r.database().BeginTx(ctx, recordMutationTxOptions())
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	statement, args, err := query.NewWorkspaceUpdateBuilder(r.store.SQLRenderer, "_workflow_process_instances", workspaceID).Set("status", process.Status).Set("updated_at", timevalue.Millis(process.UpdatedAt)).Where(query.And(query.Equal("id", process.ID), query.Equal("status", "waiting"), query.Equal("updated_at", timevalue.Millis(expectedUpdatedAt)))).Build()
	if err != nil {
		return false, err
	}
	result, err := tx.ExecContext(ctx, statement, args...)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return false, err
	}
	node.WorkspaceID = workspaceID
	if err := NewWorkflowDecisionStore(r.store).updateNodeTx(ctx, tx, node); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
