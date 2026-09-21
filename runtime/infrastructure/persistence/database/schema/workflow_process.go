package schema

import (
	"context"
	"fmt"
)

func EnsureWorkflowProcessSchema(ctx context.Context, s Store) error {
	text := s.ApplicationSchemaIDColumnType()
	tables := map[string][]string{
		"_workflow_process_instances": {
			"workspace_id " + text + " NOT NULL", "id " + text + " NOT NULL", "workflow_key " + text + " NOT NULL", "workflow_name TEXT NOT NULL",
			"workflow_definition_version_id " + text + " NOT NULL DEFAULT ''",
			"definition_version INTEGER NOT NULL", "definition_hash " + text + " NOT NULL", "definition_json TEXT NOT NULL",
			"object_key " + text, "record_id " + text, "initiator_id " + text + " NOT NULL", "initiator_role_key " + text,
			"status " + text + " NOT NULL", "current_node_ids_json TEXT NOT NULL", "variables_json TEXT NOT NULL",
			"result_json TEXT NOT NULL", "error_code " + text, "created_at " + text + " NOT NULL", "updated_at " + text + " NOT NULL", "completed_at " + text,
		},
		"_workflow_definitions": {
			"id " + text + " PRIMARY KEY", "workflow_key " + text + " NOT NULL", "name TEXT NOT NULL", "owner_user_id " + text,
			"enabled INTEGER NOT NULL", "current_draft_version_id " + text, "current_published_version_id " + text,
			"created_at " + text + " NOT NULL", "updated_at " + text + " NOT NULL",
		},
		"_workflow_definition_versions": {
			"id " + text + " PRIMARY KEY", "definition_id " + text + " NOT NULL", "version_no INTEGER NOT NULL",
			"status " + text + " NOT NULL", "revision INTEGER NOT NULL", "content_hash " + text, "workflow_json TEXT NOT NULL",
			"validation_report_json TEXT NOT NULL", "publish_note TEXT", "created_by " + text + " NOT NULL", "published_by " + text,
			"publish_idempotency_key " + text, "created_at " + text + " NOT NULL", "updated_at " + text + " NOT NULL",
			"published_at " + text, "archived_at " + text,
		},
		"_workflow_node_instances": {
			"workspace_id " + text + " NOT NULL", "id " + text + " NOT NULL", "process_id " + text + " NOT NULL", "node_id " + text + " NOT NULL",
			"node_type " + text + " NOT NULL", "iteration INTEGER NOT NULL", "status " + text + " NOT NULL",
			"input_json TEXT NOT NULL", "output_json TEXT NOT NULL", "error_code " + text,
			"started_at " + text + " NOT NULL", "completed_at " + text,
		},
		"_workflow_tasks": {
			"workspace_id " + text + " NOT NULL", "id " + text + " NOT NULL", "process_id " + text + " NOT NULL", "node_instance_id " + text + " NOT NULL", "node_id " + text + " NOT NULL",
			"title TEXT NOT NULL", "assignee_user_id " + text, "assignee_name TEXT", "assignee_role_key " + text,
			"assignee_resolver_key " + text, "assignee_evidence_json TEXT NOT NULL DEFAULT '{\"matches\":[]}'",
			"resolver_snapshot_json TEXT NOT NULL DEFAULT '[]'", "candidate_source " + text, "node_definition_version INTEGER NOT NULL DEFAULT 0", "sequence_no INTEGER NOT NULL",
			"status " + text + " NOT NULL", "decision " + text, "comment TEXT", "due_at " + text,
			"completed_by " + text, "completed_at " + text, "created_at " + text + " NOT NULL", "updated_at " + text + " NOT NULL",
		},
		"_workflow_process_events": {
			"workspace_id " + text + " NOT NULL", "id " + text + " NOT NULL", "process_id " + text + " NOT NULL", "node_id " + text, "task_id " + text,
			"event " + text + " NOT NULL", "actor_id " + text + " NOT NULL", "summary TEXT NOT NULL", "metadata_json TEXT NOT NULL", "created_at " + text + " NOT NULL",
		},
	}
	for _, table := range sortedRuntimeSchemaTables(tables) {
		if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier(table)+" ("+quotedColumnDefinitions(s, tables[table])+")"); err != nil {
			return fmt.Errorf("create %s: %w", table, err)
		}
	}
	for column, definition := range map[string]string{
		"assignee_name": "TEXT", "resolver_snapshot_json": "TEXT NOT NULL DEFAULT '[]'",
		"candidate_source": text, "node_definition_version": "INTEGER NOT NULL DEFAULT 0",
	} {
		if err := s.EnsureRuntimeColumn(ctx, "_workflow_tasks", column, definition); err != nil {
			return err
		}
	}
	if err := s.EnsureRuntimeColumn(ctx, "_workflow_process_instances", "workflow_definition_version_id", text+" NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	indexes := []struct {
		name, table string
		columns     []string
		unique      bool
	}{
		{name: "uniq_workflow_process_workspace_id", table: "_workflow_process_instances", columns: []string{"workspace_id", "id"}, unique: true},
		{name: "uniq_workflow_node_workspace_id", table: "_workflow_node_instances", columns: []string{"workspace_id", "id"}, unique: true},
		{name: "uniq_workflow_task_workspace_id", table: "_workflow_tasks", columns: []string{"workspace_id", "id"}, unique: true},
		{name: "uniq_workflow_event_workspace_id", table: "_workflow_process_events", columns: []string{"workspace_id", "id"}, unique: true},
		{name: "idx_workflow_process_business_record", table: "_workflow_process_instances", columns: []string{"workspace_id", "object_key", "record_id", "created_at"}},
		{name: "idx_workflow_definition_key", table: "_workflow_definitions", columns: []string{"workflow_key"}, unique: true},
		{name: "idx_workflow_version_number", table: "_workflow_definition_versions", columns: []string{"definition_id", "version_no"}, unique: true},
		{name: "idx_workflow_version_status", table: "_workflow_definition_versions", columns: []string{"definition_id", "status", "updated_at"}},
		{name: "idx_workflow_publish_idempotency", table: "_workflow_definition_versions", columns: []string{"definition_id", "publish_idempotency_key"}, unique: true},
		{name: "idx_workflow_process_status", table: "_workflow_process_instances", columns: []string{"workspace_id", "status", "updated_at"}},
		{name: "idx_workflow_node_process", table: "_workflow_node_instances", columns: []string{"workspace_id", "process_id", "node_id", "iteration"}, unique: true},
		{name: "idx_workflow_task_assignee", table: "_workflow_tasks", columns: []string{"workspace_id", "assignee_user_id", "status", "created_at"}},
		{name: "idx_workflow_task_assignee_process", table: "_workflow_tasks", columns: []string{"workspace_id", "assignee_user_id", "process_id"}},
		{name: "idx_workflow_task_process", table: "_workflow_tasks", columns: []string{"workspace_id", "process_id", "node_id", "status"}},
		{name: "idx_workflow_event_process", table: "_workflow_process_events", columns: []string{"workspace_id", "process_id", "created_at"}},
	}
	for _, index := range indexes {
		if err := s.CreateIndexIfMissing(ctx, index.table, index.name, index.unique, index.columns...); err != nil {
			return fmt.Errorf("create %s: %w", index.name, err)
		}
	}
	return EnsureWorkflowRouteStepsSchema(ctx, s)
}
