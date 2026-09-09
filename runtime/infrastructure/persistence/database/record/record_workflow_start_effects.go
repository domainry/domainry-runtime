package record

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/domainry/domainry-foundation/mutation"
	"github.com/domainry/domainry-orm/query"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

// insertWorkflowStartTx writes one Action-staged Workflow start: the starting
// process, its per-instance approval route and the intent that activates it.
// Every row belongs to the Action's own transaction, so an aborted Action can
// never leave a half-started process or an orphan route behind.
func (r RecordStore) insertWorkflowStartTx(ctx context.Context, tx TransactionExecutor, workspaceID string, start transactionmodel.WorkflowStartCommit) error {
	process := start.Process
	process.WorkspaceID = workspaceID
	if strings.TrimSpace(process.ID) == "" || strings.TrimSpace(process.WorkflowKey) == "" {
		return fmt.Errorf("staged workflow start requires a process identity")
	}
	definition, err := json.Marshal(process.DefinitionSnapshot)
	if err != nil {
		return fmt.Errorf("encode staged workflow definition snapshot: %w", err)
	}
	currentNodes, _ := json.Marshal(process.CurrentNodeIDs)
	variables, _ := json.Marshal(database.NonNilMap(process.Variables))
	result, _ := json.Marshal(database.NonNilMap(process.Result))
	columns := []string{"id", "workflow_key", "workflow_name", "workflow_definition_version_id", "definition_version", "definition_hash", "definition_json", "object_key", "record_id", "initiator_id", "initiator_role_key", "status", "current_node_ids_json", "variables_json", "result_json", "error_code", "created_at", "updated_at", "completed_at"}
	values := []any{process.ID, process.WorkflowKey, process.WorkflowName, process.DefinitionVersionID, process.DefinitionVersion, process.DefinitionHash, string(definition), process.ObjectKey, process.RecordID, process.InitiatorID, process.InitiatorRoleKey, process.Status, string(currentNodes), string(variables), string(result), process.ErrorCode, process.CreatedAt, process.UpdatedAt, database.NullableText(process.CompletedAt)}
	statement, args, err := query.NewWorkspaceInsertBuilder(r.store.SQLRenderer, "_workflow_process_instances", workspaceID).Columns(columns...).Values(values...).Build()
	if err != nil {
		return fmt.Errorf("build staged workflow process insert: %w", err)
	}
	if _, err := tx.ExecContext(ctx, statement, args...); err != nil {
		return fmt.Errorf("insert staged workflow process: %w", database.MutationConstraintError(err, "workflow_process", process.ID, mutation.MutationConflictIdempotency))
	}
	for _, step := range start.RouteSteps {
		step.WorkspaceID = workspaceID
		step.ProcessID = process.ID
		if err := r.insertWorkflowRouteStepTx(ctx, tx, step); err != nil {
			return err
		}
	}
	intent := start.Intent
	intent.WorkspaceID = workspaceID
	intent.ProcessID = process.ID
	return r.insertWorkflowIntentTx(ctx, tx, intent)
}

func (r RecordStore) insertWorkflowRouteStepTx(ctx context.Context, tx TransactionExecutor, step workflowmodel.WorkflowRouteStep) error {
	if strings.TrimSpace(step.ID) == "" || step.StepNo < 1 {
		return fmt.Errorf("staged workflow route step requires an identity and a positive step number")
	}
	assignees := step.AssigneeSnapshot
	if assignees == nil {
		assignees = []workflowmodel.WorkflowRouteAssignee{}
	}
	snapshot, err := json.Marshal(assignees)
	if err != nil {
		return fmt.Errorf("encode staged workflow route assignees: %w", err)
	}
	columns := []string{"id", "process_id", "node_id", "step_no", "step_key", "title", "mode", "required_approvals", "status", "assignee_snapshot_json", "configured_by", "configured_at", "configure_source", "node_instance_id", "created_at", "updated_at"}
	values := []any{step.ID, step.ProcessID, step.NodeID, step.StepNo, step.StepKey, step.Title, step.Mode, step.RequiredApprovals, step.Status, string(snapshot), step.ConfiguredBy, step.ConfiguredAt, step.ConfigureSource, step.NodeInstanceID, step.CreatedAt, step.UpdatedAt}
	statement, args, err := query.NewWorkspaceInsertBuilder(r.store.SQLRenderer, "_workflow_route_steps", step.WorkspaceID).Columns(columns...).Values(values...).Build()
	if err != nil {
		return fmt.Errorf("build staged workflow route step insert: %w", err)
	}
	if _, err := tx.ExecContext(ctx, statement, args...); err != nil {
		return fmt.Errorf("insert staged workflow route step: %w", database.MutationConstraintError(err, "workflow_route_step", step.ID, mutation.MutationConflictIdempotency))
	}
	return nil
}
