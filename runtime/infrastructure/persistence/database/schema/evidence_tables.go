package schema

import (
	"context"
	"fmt"
)

func ensureEvidenceTables(ctx context.Context, s Store, tables map[string][]string, text string) error {
	workspaceIdentities := prepareWorkspaceScopedIdentities(tables)
	for _, table := range sortedRuntimeSchemaTables(tables) {
		if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier(table)+" ("+quotedColumnDefinitions(s, tables[table])+")"); err != nil {
			return fmt.Errorf("create %s: %w", table, err)
		}
	}
	if err := ensureWorkspaceScopedIdentities(ctx, s, workspaceIdentities); err != nil {
		return err
	}

	for _, table := range []string{"_automation_runs"} {
		if _, selected := tables[table]; !selected {
			continue
		}
		if err := s.EnsureRuntimeColumn(ctx, table, "lease_owner", text+" NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	if _, selected := tables["_automation_runs"]; selected {
		if err := s.EnsureRuntimeColumn(ctx, "_automation_runs", "fencing_token", "BIGINT NOT NULL DEFAULT 1"); err != nil {
			return err
		}
	}
	if _, selected := tables["_workflow_executions"]; selected {
		for column, definition := range map[string]string{
			"process_id": text, "node_id": text,
			"lease_owner": text + " NOT NULL DEFAULT ''", "lease_expires_at": text + " NOT NULL DEFAULT ''", "fencing_token": "BIGINT NOT NULL DEFAULT 0",
		} {
			if err := s.EnsureRuntimeColumn(ctx, "_workflow_executions", column, definition); err != nil {
				return err
			}
		}
	}
	indexes := []struct {
		name    string
		table   string
		columns []string
		unique  bool
	}{
		{name: "idx_runtime_publication_destination", table: "_publication_outbox", columns: []string{"publication_type", "workspace_id", "connector_key", "status"}},
		{name: "idx_runtime_publication_due", table: "_publication_outbox", columns: []string{"publication_type", "status", "next_attempt_at", "lease_expires_at", "created_at"}},
		{name: "idx_runtime_publication_operation", table: "_publication_outbox", columns: []string{"workspace_id", "operation_id"}},
		{name: "uniq_runtime_publication_dedup", table: "_publication_outbox", columns: []string{"workspace_id", "publication_type", "connector_key", "connection_key", "operation", "dedup_key"}, unique: true},

		{name: "uniq_runtime_notification_publication_source", table: "_publication_outbox", columns: []string{"publication_type", "workspace_id", "application_key", "source_event_id", "connector_key", "connection_key", "operation", "dedup_key"}, unique: true},
		{name: "uniq_automation_run_idempotency", table: "_automation_runs", columns: []string{"workspace_id", "run_kind", "idempotency_key"}, unique: true},
		{name: "idx_automation_run_rule", table: "_automation_runs", columns: []string{"workspace_id", "run_kind", "rule_key", "created_at"}},
		{name: "idx_automation_run_record", table: "_automation_runs", columns: []string{"workspace_id", "run_kind", "object_key", "record_id", "created_at"}},
		{name: "idx_automation_run_status", table: "_automation_runs", columns: []string{"workspace_id", "run_kind", "status", "created_at"}},
		{name: "idx_automation_run_lease", table: "_automation_runs", columns: []string{"run_kind", "status", "lease_expires_at"}},
		{name: "uniq_action_assurance_token_hash", table: "_action_assurance_grants", columns: []string{"token_hash"}, unique: true},
		{name: "idx_action_assurance_binding", table: "_action_assurance_grants", columns: []string{"workspace_id", "user_id", "action_key", "object_key", "record_id"}},
		{name: "idx_action_assurance_expiry", table: "_action_assurance_grants", columns: []string{"expires_at", "consumed_at"}},
		{name: "uniq_workflow_execution_workspace_id", table: "_workflow_executions", columns: []string{"workspace_id", "id"}, unique: true},
		{name: "idx_workflow_execution_process", table: "_workflow_executions", columns: []string{"workspace_id", "process_id", "node_id", "status"}},
		{name: "idx_workflow_execution_operation", table: "_workflow_executions", columns: []string{"workspace_id", "operation_id"}},
		{name: "idx_runtime_release_instance_expiry", table: "_release_instances", columns: []string{"lease_expires_at"}},
		{name: "idx_runtime_release_instance_cohort", table: "_release_instances", columns: []string{"generation", "combination_sha256"}},
	}
	for _, index := range indexes {
		if _, selected := tables[index.table]; !selected {
			continue
		}
		if err := s.CreateIndexIfMissing(ctx, index.table, index.name, index.unique, index.columns...); err != nil {
			return fmt.Errorf("create %s: %w", index.name, err)
		}
	}
	return nil
}
