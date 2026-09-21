package schema

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/domainry/domainry-orm/query"
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

	if err := s.CreateIndexIfMissing(ctx, "_worker_queue_scopes", "uniq_runtime_worker_queue_scope", true, "queue_kind", "scope_key"); err != nil {
		return fmt.Errorf("create uniq_runtime_worker_queue_scope: %w", err)
	}
	for _, queue := range []struct {
		kind  string
		table string
	}{
		{kind: "runtime_publication_outbox", table: "_publication_outbox"},
		{kind: "workflow_continuation", table: "_workflow_executions"},
	} {
		exists, existsErr := runtimeSchemaTableExists(ctx, s, queue.table)
		if existsErr != nil {
			return existsErr
		}
		if exists {
			if err := backfillWorkerQueueScopes(ctx, s, queue.kind, queue.table); err != nil {
				return err
			}
		}
	}
	deleteLegacyScopes, deleteLegacyScopeArgs, err := query.NewDeleteBuilder(s.RuntimeRenderer(), "_worker_queue_scopes").
		Where(query.Equal("queue_kind", "runtime_publication_outbox")).
		Build()
	if err != nil {
		return fmt.Errorf("build legacy integration outbox worker task cleanup: %w", err)
	}
	if _, err := s.SchemaDB().ExecContext(ctx, deleteLegacyScopes, deleteLegacyScopeArgs...); err != nil {
		return fmt.Errorf("remove legacy integration outbox worker tasks: %w", err)
	}
	for _, table := range []string{"_automation_instruction_executions", "_publication_outbox"} {
		if err := s.EnsureRuntimeColumn(ctx, table, "lease_owner", text+" NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	if err := s.EnsureRuntimeColumn(ctx, "_automation_instruction_executions", "fencing_token", "BIGINT NOT NULL DEFAULT 1"); err != nil {
		return err
	}
	for column, definition := range map[string]string{
		"request_fingerprint": text + " NOT NULL DEFAULT ''",
		"lease_owner":         text + " NOT NULL DEFAULT ''",
		"lease_expires_at":    text + " NOT NULL DEFAULT ''",
		"fencing_token":       "BIGINT NOT NULL DEFAULT 0",
		"response_status":     "INTEGER NOT NULL DEFAULT 0",
		"error_code":          text + " NOT NULL DEFAULT ''",
		"expires_at":          text + " NOT NULL DEFAULT ''",
	} {
		if err := s.EnsureRuntimeColumn(ctx, "_action_executions", column, definition); err != nil {
			return err
		}
	}
	if err := s.EnsureRuntimeColumn(ctx, "_publication_outbox", "lease_expires_at", text+" NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	for column, definition := range map[string]string{
		"dedup_key":           text + " NOT NULL DEFAULT ''",
		"request_fingerprint": text + " NOT NULL DEFAULT ''",
		"fencing_token":       "BIGINT NOT NULL DEFAULT 0",
		"publication_type":    text + " NOT NULL DEFAULT 'integration.connector'",
		"application_key":     text + " NOT NULL DEFAULT ''",
		"source_event_id":     text + " NOT NULL DEFAULT ''",
		"event_type":          text + " NOT NULL DEFAULT ''",
		"intent_json":         "TEXT NOT NULL DEFAULT '{}'",
		"connector_key":       text + " NOT NULL DEFAULT ''",
		"connection_key":      text,
		"operation":           text + " NOT NULL DEFAULT ''",
		"payload_json":        "TEXT NOT NULL DEFAULT '{}'",
		"event_id":            text,
		"request_ref":         "TEXT",
		"response_ref":        "TEXT",
		"error":               "TEXT",
		"remote_event_id":     text + " NOT NULL DEFAULT ''",
		"last_error_code":     text + " NOT NULL DEFAULT ''",
		"last_error":          "TEXT NOT NULL DEFAULT ''",
		"terminal_at":         text + " NOT NULL DEFAULT ''",
		"created_by":          text + " NOT NULL DEFAULT ''",
	} {
		if err := s.EnsureRuntimeColumn(ctx, "_publication_outbox", column, definition); err != nil {
			return err
		}
	}
	backfillDedupKey, backfillDedupKeyArgs, err := query.NewUpdateBuilder(s.RuntimeRenderer(), "_publication_outbox").
		SetExpression("dedup_key", query.Column("id")).
		Where(query.Equal("dedup_key", "")).
		Build()
	if err != nil {
		return fmt.Errorf("build integration outbox dedup key backfill: %w", err)
	}
	if _, err := s.SchemaDB().ExecContext(ctx, backfillDedupKey, backfillDedupKeyArgs...); err != nil {
		return fmt.Errorf("backfill integration outbox dedup key: %w", err)
	}
	for column, definition := range map[string]string{
		"process_id": text, "node_id": text,
		"lease_owner": text + " NOT NULL DEFAULT ''", "lease_expires_at": text + " NOT NULL DEFAULT ''", "fencing_token": "BIGINT NOT NULL DEFAULT 0",
	} {
		if err := s.EnsureRuntimeColumn(ctx, "_workflow_executions", column, definition); err != nil {
			return err
		}
	}
	if err := prepareIdempotencyReceiptMigrations(ctx, s,
		idempotencyReceiptMigrationSpec{table: "_action_executions", scopeColumns: []string{"object_key", "record_id", "action_key"}, backfillColumns: []string{"object_key", "action_key"}},
		idempotencyReceiptMigrationSpec{table: "_record_mutation_executions", scopeColumns: []string{"operation", "object_key", "target_id"}, backfillColumns: []string{"operation", "object_key"}},
		idempotencyReceiptMigrationSpec{table: "_workflow_execution_receipts", scopeColumns: []string{"workflow_key"}, backfillColumns: []string{"workflow_key"}},
	); err != nil {
		return err
	}
	indexes := []struct {
		name    string
		table   string
		columns []string
		unique  bool
	}{
		{name: "uniq_transaction_boundary_intent", table: "_transaction_boundary_intents", columns: []string{"workspace_id", "owner", "operation", "idempotency_key"}, unique: true},
		{name: "idx_transaction_boundary_intent_due", table: "_transaction_boundary_intents", columns: []string{"status", "next_attempt_at", "lease_expires_at"}},
		{name: "idx_runtime_publication_destination", table: "_publication_outbox", columns: []string{"publication_type", "workspace_id", "connector_key", "status"}},
		{name: "idx_runtime_publication_due", table: "_publication_outbox", columns: []string{"publication_type", "status", "next_attempt_at", "lease_expires_at", "created_at"}},
		{name: "uniq_runtime_publication_dedup", table: "_publication_outbox", columns: []string{"workspace_id", "publication_type", "connector_key", "connection_key", "operation", "dedup_key"}, unique: true},

		{name: "uniq_runtime_notification_publication_source", table: "_publication_outbox", columns: []string{"publication_type", "workspace_id", "application_key", "source_event_id", "connector_key", "connection_key", "operation", "dedup_key"}, unique: true},
		{name: "idx_automation_execution_rule", table: "_automation_rule_executions", columns: []string{"workspace_id", "rule_key", "created_at"}},
		{name: "idx_automation_execution_record", table: "_automation_rule_executions", columns: []string{"workspace_id", "object_key", "record_id", "created_at"}},
		{name: "idx_automation_execution_status", table: "_automation_rule_executions", columns: []string{"workspace_id", "status", "created_at"}},
		{name: "idx_automation_instruction_idempotency", table: "_automation_instruction_executions", columns: []string{"workspace_id", "idempotency_key"}, unique: true},
		{name: "idx_automation_instruction_status", table: "_automation_instruction_executions", columns: []string{"workspace_id", "status", "lease_expires_at"}},
		{name: "uniq_business_action_execution_scope", table: "_action_executions", columns: []string{"workspace_id", "object_key", "record_id", "action_key", "idempotency_key"}, unique: true},
		{name: "idx_business_action_execution_lease", table: "_action_executions", columns: []string{"status", "lease_expires_at"}},
		{name: "uniq_action_assurance_token_hash", table: "_action_assurance_grants", columns: []string{"token_hash"}, unique: true},
		{name: "idx_action_assurance_binding", table: "_action_assurance_grants", columns: []string{"workspace_id", "user_id", "action_key", "object_key", "record_id"}},
		{name: "idx_action_assurance_expiry", table: "_action_assurance_grants", columns: []string{"expires_at", "consumed_at"}},
		{name: "uniq_record_mutation_execution_scope", table: "_record_mutation_executions", columns: []string{"workspace_id", "operation", "object_key", "target_id", "idempotency_key"}, unique: true},
		{name: "idx_record_mutation_execution_lease", table: "_record_mutation_executions", columns: []string{"status", "lease_expires_at"}},
		{name: "uniq_workflow_execution_workspace_id", table: "_workflow_executions", columns: []string{"workspace_id", "id"}, unique: true},
		{name: "idx_workflow_execution_process", table: "_workflow_executions", columns: []string{"workspace_id", "process_id", "node_id", "status"}},
		{name: "uniq_workflow_execution_receipt_scope", table: "_workflow_execution_receipts", columns: []string{"workspace_id", "workflow_key", "idempotency_key"}, unique: true},
		{name: "idx_workflow_execution_receipt_lease", table: "_workflow_execution_receipts", columns: []string{"status", "lease_expires_at"}},
		{name: "uniq_runtime_operation_key", table: "_operation_requests", columns: []string{"workspace_id", "system_purpose", "kind", "idempotency_key"}, unique: true},
		{name: "idx_runtime_operation_status", table: "_operation_requests", columns: []string{"workspace_id", "status", "created_at"}},
		{name: "uniq_runtime_operation_control", table: "_operation_controls", columns: []string{"system_purpose", "control_kind", "owner"}, unique: true},
		{name: "idx_runtime_operation_control_state", table: "_operation_controls", columns: []string{"system_purpose", "control_kind", "state"}},
		{name: "idx_runtime_release_instance_expiry", table: "_release_instances", columns: []string{"lease_expires_at"}},
		{name: "idx_runtime_release_instance_cohort", table: "_release_instances", columns: []string{"generation", "combination_sha256"}},
		{name: "idx_runtime_break_glass_active", table: "_operation_break_glass_grants", columns: []string{"workspace_id", "state", "expires_at"}},
		{name: "uniq_runtime_break_glass_audit", table: "_operation_break_glass_grants", columns: []string{"workspace_id", "audit_event_id"}, unique: true},
		{name: "uniq_runtime_database_retirement_object", table: "_operation_database_retirements", columns: []string{"engine", "database_name", "schema_name", "object_kind", "object_name", "parent_name"}, unique: true},
		{name: "idx_runtime_database_retirement_status", table: "_operation_database_retirements", columns: []string{"state", "updated_at"}},
	}
	for _, index := range indexes {
		if err := s.CreateIndexIfMissing(ctx, index.table, index.name, index.unique, index.columns...); err != nil {
			return fmt.Errorf("create %s: %w", index.name, err)
		}
	}
	return nil
}

func backfillWorkerQueueScopes(ctx context.Context, s Store, queueKind, table string) error {
	queueKind, table = strings.TrimSpace(queueKind), strings.TrimSpace(table)
	queryValue := "SELECT " + s.Identifier("workspace_id") + ", MAX(" + s.Identifier("updated_at") + ") FROM " + s.TableIdentifier(table) + " GROUP BY " + s.Identifier("workspace_id")
	rows, err := s.SchemaDB().QueryContext(ctx, queryValue)
	if err != nil {
		return fmt.Errorf("inventory %s worker queue scopes: %w", queueKind, err)
	}
	type queueScope struct{ workspaceID, updatedAt string }
	values := []queueScope{}
	for rows.Next() {
		var value queueScope
		if err := rows.Scan(&value.workspaceID, &value.updatedAt); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan %s worker queue scope: %w", queueKind, err)
		}
		value.workspaceID = strings.TrimSpace(value.workspaceID)
		if value.workspaceID != "" {
			values = append(values, value)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate %s worker queue scopes: %w", queueKind, err)
	}
	_ = rows.Close()
	for _, value := range values {
		digest := sha256.Sum256([]byte(queueKind + "\x00" + value.workspaceID))
		id := "worker_scope:" + hex.EncodeToString(digest[:12])
		insert := query.NewInsertBuilder(s.RuntimeRenderer(), "_worker_queue_scopes").
			Columns("id", "queue_kind", "scope_key", "updated_at").
			Values(id, queueKind, value.workspaceID, value.updatedAt)
		insert, err = s.RuntimeProfile().ApplyUpsert(insert, []string{"queue_kind", "scope_key"}, query.Assign("updated_at", value.updatedAt))
		if err != nil {
			return fmt.Errorf("build %s worker queue scope upsert: %w", queueKind, err)
		}
		statement, arguments, err := insert.Build()
		if err != nil {
			return fmt.Errorf("build %s worker queue scope upsert: %w", queueKind, err)
		}
		if _, err := s.SchemaDB().ExecContext(ctx, statement, arguments...); err != nil {
			return fmt.Errorf("backfill %s worker queue scope: %w", queueKind, err)
		}
	}
	return nil
}

func runtimeSchemaTableExists(ctx context.Context, s Store, table string) (bool, error) {
	table = strings.TrimSpace(table)
	exists, err := s.RuntimeTableExists(ctx, table)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("inspect runtime schema table %s: %w", table, err)
	}
	return exists, nil
}
