package schema

import "github.com/domainry/domainry-foundation/schemaownership"

const (
	WorkflowMigrationOwner        = "runtime/workflow"
	WorkflowExecutionsTable       = "_workflow_executions"
	WorkflowProcessInstancesTable = "_workflow_process_instances"
	WorkflowNodeInstancesTable    = "_workflow_node_instances"
	WorkflowTasksTable            = "_workflow_tasks"
	WorkflowProcessEventsTable    = "_workflow_process_events"
)

const workflowSubjectErasurePolicy = "subject erasure atomically anonymizes personal fields and cancels active workflow state; product retention governs the remaining execution evidence"

// WorkflowSchemaOwnership is the source-owned contract for every Runtime
// Workflow table. Runtime installs these tables in the host database for
// embedded modules and uses the same schema against a standalone database.
func WorkflowSchemaOwnership() []schemaownership.Table {
	return []schemaownership.Table{
		{
			Name: WorkflowExecutionsTable, Owner: WorkflowMigrationOwner, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"workspace_id", "id"},
			BoundedQueryPath: "workspace execution identity; process/status, operation and lease workers use dedicated indexes and bounded claims",
			DeletionPolicy:   workflowSubjectErasurePolicy,
		},
		{
			Name: WorkflowProcessInstancesTable, Owner: WorkflowMigrationOwner, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"workspace_id", "id"},
			BoundedQueryPath: "workspace process identity; business-record, status and operation indexes serve bounded lookups",
			DeletionPolicy:   workflowSubjectErasurePolicy,
		},
		{
			Name: WorkflowNodeInstancesTable, Owner: WorkflowMigrationOwner, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"workspace_id", "id"},
			BoundedQueryPath: "workspace process/node/iteration identity and process-scoped ordered lookup",
			DeletionPolicy:   workflowSubjectErasurePolicy,
		},
		{
			Name: WorkflowTasksTable, Owner: WorkflowMigrationOwner, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"workspace_id", "id"},
			BoundedQueryPath: "workspace task identity; assignee inbox and process task indexes are queried with enforced limits",
			DeletionPolicy:   workflowSubjectErasurePolicy,
		},
		{
			Name: WorkflowProcessEventsTable, Owner: WorkflowMigrationOwner, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"workspace_id", "id"},
			BoundedQueryPath: "workspace process timeline index ordered by created_at with bounded page size",
			DeletionPolicy:   workflowSubjectErasurePolicy,
		},
		{
			Name: WorkflowRouteStepsTable, Owner: WorkflowMigrationOwner, WorkspaceScope: schemaownership.ScopeWorkspace,
			RetentionClass: schemaownership.RetentionUserErase, PrimaryKey: []string{"workspace_id", "id"},
			BoundedQueryPath: "workspace process/step identity and status index; route reads are process-scoped and bounded",
			DeletionPolicy:   workflowSubjectErasurePolicy,
		},
	}
}

func WorkflowOwnedTables() []string {
	return schemaownership.Names(WorkflowSchemaOwnership())
}
