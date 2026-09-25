package schema

import (
	"context"
	"fmt"

	ormschema "github.com/domainry/domainry-orm/schema"
)

// WorkflowRouteStepsTable holds the per-instance approval route of one
// Workflow process. The rows are the electorate authority for a route-driven
// approval node, so they are host-owned Runtime schema and migrate under the
// single _schema_migrations ledger.
const WorkflowRouteStepsTable = "_workflow_route_steps"

// EnsureWorkflowRouteStepsSchema is Runtime schema 026.
func EnsureWorkflowRouteStepsSchema(ctx context.Context, store Store) error {
	statement, args, err := ormschema.NewTable(store.RuntimeRenderer(), WorkflowRouteStepsTable).IfNotExists().Columns(
		ormschema.Column("workspace_id", ormschema.TextKey(128)).NotNull(),
		ormschema.Column("id", ormschema.TextKey(128)).NotNull(),
		ormschema.Column("process_id", ormschema.TextKey(128)).NotNull(),
		ormschema.Column("node_id", ormschema.TextKey(128)).NotNull(),
		ormschema.Column("step_no", ormschema.Integer()).NotNull(),
		ormschema.Column("step_key", ormschema.TextKey(128)).NotNull(),
		ormschema.Column("title", ormschema.Text()).NotNull(),
		ormschema.Column("mode", ormschema.TextKey(32)).NotNull().DefaultValue("any"),
		ormschema.Column("required_approvals", ormschema.Integer()).NotNull().DefaultValue(0),
		ormschema.Column("status", ormschema.TextKey(32)).NotNull(),
		ormschema.Column("assignee_snapshot_json", ormschema.Text()).NotNull(),
		ormschema.Column("configured_by", ormschema.TextKey(128)).NotNull().DefaultValue(""),
		ormschema.Column("configured_at", ormschema.BigInt()).NotNull().DefaultValue(0),
		ormschema.Column("configure_source", ormschema.TextKey(64)).NotNull().DefaultValue(""),
		ormschema.Column("node_instance_id", ormschema.TextKey(128)).NotNull().DefaultValue(""),
		ormschema.Column("created_at", ormschema.BigInt()).NotNull(),
		ormschema.Column("updated_at", ormschema.BigInt()).NotNull(),
	).PrimaryKey("workspace_id", "id").Build()
	if err != nil {
		return fmt.Errorf("build %s: %w", WorkflowRouteStepsTable, err)
	}
	if _, err := store.SchemaDB().ExecContext(ctx, statement, args...); err != nil {
		return fmt.Errorf("create %s: %w", WorkflowRouteStepsTable, err)
	}
	if err := store.CreateIndexIfMissing(ctx, WorkflowRouteStepsTable, "uniq_workflow_route_step_no", true, "workspace_id", "process_id", "step_no"); err != nil {
		return fmt.Errorf("create uniq_workflow_route_step_no: %w", err)
	}
	if err := store.CreateIndexIfMissing(ctx, WorkflowRouteStepsTable, "idx_workflow_route_step_status", false, "workspace_id", "process_id", "status"); err != nil {
		return fmt.Errorf("create idx_workflow_route_step_status: %w", err)
	}
	return nil
}
