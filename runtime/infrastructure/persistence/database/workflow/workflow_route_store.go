package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/domainry/domainry-orm/query"
	workflowcontract "github.com/domainry/domainry-runtime/runtime/domain/workflow/contract"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
)

// workflowRouteStepsTable mirrors the host-owned Runtime schema 026 table.
const workflowRouteStepsTable = "_workflow_route_steps"

// WorkflowRouteStore owns the durable per-instance approval route rows.
type WorkflowRouteStore struct {
	store *database.RuntimeStore
	db    workflowDatabase
}

func NewWorkflowRouteStore(store *database.RuntimeStore) WorkflowRouteStore {
	return WorkflowRouteStore{store: store}
}

func (r WorkflowRouteStore) database() workflowDatabase {
	if r.db != nil {
		return r.db
	}
	return r.store.DB()
}

var workflowRouteStepColumns = []string{"workspace_id", "id", "process_id", "node_id", "step_no", "step_key", "title", "mode", "required_approvals", "status", "assignee_snapshot_json", "configured_by", "configured_at", "configure_source", "node_instance_id", "created_at", "updated_at"}

func workflowRouteStepValues(step workflowmodel.WorkflowRouteStep) ([]any, error) {
	assignees := step.AssigneeSnapshot
	if assignees == nil {
		assignees = []workflowmodel.WorkflowRouteAssignee{}
	}
	snapshot, err := json.Marshal(assignees)
	if err != nil {
		return nil, fmt.Errorf("encode workflow route assignees: %w", err)
	}
	return []any{step.WorkspaceID, step.ID, step.ProcessID, step.NodeID, step.StepNo, step.StepKey, step.Title, step.Mode, step.RequiredApprovals, step.Status, string(snapshot), step.ConfiguredBy, step.ConfiguredAt, step.ConfigureSource, step.NodeInstanceID, step.CreatedAt, step.UpdatedAt}, nil
}

func scanWorkflowRouteStep(scanner interface{ Scan(...any) error }) (workflowmodel.WorkflowRouteStep, error) {
	var step workflowmodel.WorkflowRouteStep
	var snapshot string
	if err := scanner.Scan(&step.WorkspaceID, &step.ID, &step.ProcessID, &step.NodeID, &step.StepNo, &step.StepKey, &step.Title, &step.Mode, &step.RequiredApprovals, &step.Status, &snapshot, &step.ConfiguredBy, &step.ConfiguredAt, &step.ConfigureSource, &step.NodeInstanceID, &step.CreatedAt, &step.UpdatedAt); err != nil {
		return workflowmodel.WorkflowRouteStep{}, err
	}
	if strings.TrimSpace(snapshot) != "" {
		if err := json.Unmarshal([]byte(snapshot), &step.AssigneeSnapshot); err != nil {
			return workflowmodel.WorkflowRouteStep{}, fmt.Errorf("decode workflow route assignees: %w", err)
		}
	}
	return step, nil
}

func (r WorkflowRouteStore) InsertRouteSteps(ctx context.Context, workspaceID string, steps []workflowmodel.WorkflowRouteStep) error {
	workspaceID, err := requireWorkflowWorkspaceID(workspaceID)
	if err != nil {
		return err
	}
	for _, step := range steps {
		step.WorkspaceID = workspaceID
		values, err := workflowRouteStepValues(step)
		if err != nil {
			return err
		}
		statement, args, err := query.NewWorkspaceInsertBuilder(r.store.SQLRenderer, workflowRouteStepsTable, workspaceID).Columns(workflowRouteStepColumns[1:]...).Values(values[1:]...).Build()
		if err != nil {
			return fmt.Errorf("build workflow route step insert: %w", err)
		}
		if _, err := r.database().ExecContext(ctx, statement, args...); err != nil {
			return err
		}
	}
	return nil
}

func (r WorkflowRouteStore) ListRouteSteps(ctx context.Context, workspaceID, processID string) ([]workflowmodel.WorkflowRouteStep, error) {
	workspaceID, err := requireWorkflowWorkspaceID(workspaceID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(processID) == "" {
		return nil, fmt.Errorf("workflow route process is required")
	}
	statement, args, err := query.NewWorkspaceSelectBuilder(r.store.SQLRenderer, workflowRouteStepsTable, workspaceID).
		Columns(workflowRouteStepColumns...).Where(query.Equal("process_id", processID)).
		OrderBy(query.Ascending("step_no")).Build()
	if err != nil {
		return nil, err
	}
	rows, err := r.database().QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	steps := []workflowmodel.WorkflowRouteStep{}
	for rows.Next() {
		step, err := scanWorkflowRouteStep(rows)
		if err != nil {
			return nil, err
		}
		steps = append(steps, step)
	}
	return steps, rows.Err()
}

// UpdateRouteStepCAS writes the step only while the durable row still carries
// expectedStatus. Two concurrent decisions can therefore not both configure or
// activate the same step.
func (r WorkflowRouteStore) UpdateRouteStepCAS(ctx context.Context, workspaceID string, step workflowmodel.WorkflowRouteStep, expectedStatus string) (bool, error) {
	workspaceID, err := requireWorkflowWorkspaceID(workspaceID)
	if err != nil {
		return false, err
	}
	step.WorkspaceID = workspaceID
	tx, err := r.database().BeginTx(ctx, recordMutationTxOptions())
	if err != nil {
		return false, fmt.Errorf("begin workflow route step update: %w", err)
	}
	defer tx.Rollback()
	updated, err := updateWorkflowRouteStepTx(ctx, r.store, tx, step, expectedStatus)
	if err != nil || !updated {
		return updated, err
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit workflow route step update: %w", err)
	}
	return true, nil
}

type workflowRouteStepExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

func updateWorkflowRouteStepTx(ctx context.Context, store *database.RuntimeStore, tx workflowRouteStepExecutor, step workflowmodel.WorkflowRouteStep, expectedStatus string) (bool, error) {
	values, err := workflowRouteStepValues(step)
	if err != nil {
		return false, err
	}
	builder := query.NewWorkspaceUpdateBuilder(store.SQLRenderer, workflowRouteStepsTable, step.WorkspaceID)
	for index, column := range workflowRouteStepColumns {
		if index < 2 {
			continue
		}
		builder.Set(column, values[index])
	}
	predicates := []query.Predicate{query.Equal("id", step.ID)}
	if expected := strings.TrimSpace(expectedStatus); expected != "" {
		predicates = append(predicates, query.Equal("status", expected))
	}
	statement, args, err := builder.Where(query.And(predicates...)).Build()
	if err != nil {
		return false, fmt.Errorf("build workflow route step update: %w", err)
	}
	result, err := tx.ExecContext(ctx, statement, args...)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	return affected == 1, err
}

func insertWorkflowRouteStepTx(ctx context.Context, store *database.RuntimeStore, tx workflowRouteStepExecutor, step workflowmodel.WorkflowRouteStep) error {
	values, err := workflowRouteStepValues(step)
	if err != nil {
		return err
	}
	statement, args, err := query.NewWorkspaceInsertBuilder(store.SQLRenderer, workflowRouteStepsTable, step.WorkspaceID).Columns(workflowRouteStepColumns[1:]...).Values(values[1:]...).Build()
	if err != nil {
		return fmt.Errorf("build workflow route step insert: %w", err)
	}
	_, err = tx.ExecContext(ctx, statement, args...)
	return err
}

var _ workflowcontract.WorkflowRouteStore = WorkflowRouteStore{}

// workflowRouteStepExpectedStatus derives the compare-and-set predicate of a
// route step write from the status it moves to. Every durable transition has
// exactly one legal predecessor, so a decision computed from a stale snapshot
// writes nothing instead of overwriting a competing configuration.
func workflowRouteStepExpectedStatus(step workflowmodel.WorkflowRouteStep) string {
	switch strings.TrimSpace(step.Status) {
	case "pending":
		return "configurable"
	case "active":
		return "pending"
	case "approved", "rejected", "returned", "skipped":
		return "active"
	default:
		return ""
	}
}
