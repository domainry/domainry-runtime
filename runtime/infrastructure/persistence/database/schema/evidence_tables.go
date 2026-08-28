package schema

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func ensureEvidenceTables(ctx context.Context, s Store, tables map[string][]string, text string) error {
	workspaceIdentities := prepareWorkspaceScopedIdentities(tables)
	for _, table := range sortedRuntimeSchemaTables(tables) {
		if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier(table)+" ("+quotedColumnDefinitions(s, tables[table])+")"); err != nil {
			return fmt.Errorf("create %s: %w", table, err)
		}
	}
	if err := ensureMySQLAuditCursorColumns(ctx, s); err != nil {
		return err
	}
	if err := ensureMySQLLargeEvidenceColumns(ctx, s); err != nil {
		return err
	}
	if err := s.EnsureRuntimeColumn(ctx, "_audit_events", "workspace_id", text); err != nil {
		return err
	}
	if _, err := s.SchemaDB().ExecContext(ctx, "UPDATE "+s.TableIdentifier("_audit_events")+" SET "+s.Identifier("workspace_id")+" = COALESCE(NULLIF("+s.Identifier("workspace_id")+", ''), "+s.Placeholder(1)+")", principalmodel.InstallationWorkspaceID); err != nil {
		return fmt.Errorf("backfill audit event workspace: %w", err)
	}
	if err := ensureWorkspaceScopedIdentities(ctx, s, workspaceIdentities); err != nil {
		return err
	}
	if _, exists := tables["record_batch_jobs"]; exists {
		if err := backfillWorkerQueueScopes(ctx, s, "record_batch", "record_batch_jobs"); err != nil {
			return err
		}
	}
	notificationEventsExist, err := runtimeSchemaTableExists(ctx, s, "notification_events")
	if err != nil {
		return err
	}
	if notificationEventsExist {
		if err := backfillWorkerQueueScopes(ctx, s, "notification_inbox", "notification_events"); err != nil {
			return err
		}
	}
	for _, queue := range []struct {
		kind  string
		table string
	}{
		{kind: "integration_event", table: "integration_events"},
		{kind: "integration_outbox", table: "integration_outbox_messages"},
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
	if _, err := s.SchemaDB().ExecContext(ctx, "DELETE FROM "+s.TableIdentifier("runtime_worker_queue_scopes")+" WHERE "+s.Identifier("queue_kind")+" = "+s.Placeholder(1), "integration_outbox_task"); err != nil {
		return fmt.Errorf("remove legacy integration outbox worker tasks: %w", err)
	}
	if err := s.EnsureRuntimeColumn(ctx, "integration_connections", "provider_key", text+" NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	for _, column := range []string{"expires_at", "rotated_at", "revoked_at", "last_tested_at", "last_test_status", "last_test_error"} {
		definition := text + " NOT NULL DEFAULT ''"
		if column == "last_test_error" {
			definition = "TEXT NOT NULL DEFAULT ''"
		}
		if err := s.EnsureRuntimeColumn(ctx, "integration_secrets", column, definition); err != nil {
			return err
		}
	}
	if err := s.EnsureRuntimeColumn(ctx, "integration_invocations", "provider_key", text+" NOT NULL DEFAULT ''"); err != nil {
		return err
	}
	for _, table := range []string{"automation_instruction_executions", "integration_events", "integration_outbox_messages"} {
		if err := s.EnsureRuntimeColumn(ctx, table, "lease_owner", text+" NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	if err := s.EnsureRuntimeColumn(ctx, "automation_instruction_executions", "fencing_token", "BIGINT NOT NULL DEFAULT 1"); err != nil {
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
		if err := s.EnsureRuntimeColumn(ctx, "business_action_executions", column, definition); err != nil {
			return err
		}
	}
	for _, table := range []string{"integration_events", "integration_outbox_messages"} {
		if err := s.EnsureRuntimeColumn(ctx, table, "lease_expires_at", text+" NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	if err := s.EnsureRuntimeColumn(ctx, "integration_events", "fencing_token", "BIGINT NOT NULL DEFAULT 0"); err != nil {
		return err
	}
	for column, definition := range map[string]string{
		"ack_deadline_at":     text + " NOT NULL DEFAULT ''",
		"dedup_key":           text + " NOT NULL DEFAULT ''",
		"request_fingerprint": text + " NOT NULL DEFAULT ''",
		"fencing_token":       "BIGINT NOT NULL DEFAULT 0",
	} {
		if err := s.EnsureRuntimeColumn(ctx, "integration_outbox_messages", column, definition); err != nil {
			return err
		}
	}
	if _, err := s.SchemaDB().ExecContext(ctx, "UPDATE "+s.TableIdentifier("integration_outbox_messages")+" SET "+s.Identifier("dedup_key")+" = "+s.Identifier("id")+" WHERE "+s.Identifier("dedup_key")+" = ''"); err != nil {
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
		idempotencyReceiptMigrationSpec{table: "business_action_executions", scopeColumns: []string{"object_key", "record_id", "action_key"}, backfillColumns: []string{"object_key", "action_key"}},
		idempotencyReceiptMigrationSpec{table: "record_mutation_executions", scopeColumns: []string{"operation", "object_key", "target_id"}, backfillColumns: []string{"operation", "object_key"}},
		idempotencyReceiptMigrationSpec{table: "workflow_execution_receipts", scopeColumns: []string{"workflow_key"}, backfillColumns: []string{"workflow_key"}},
	); err != nil {
		return err
	}
	indexes := []struct {
		name    string
		table   string
		columns []string
		unique  bool
	}{
		{name: "uniq_integration_events_external", table: "integration_events", columns: []string{"workspace_id", "provider", "external_id"}, unique: true},
		{name: "uniq_integration_event_mapping_intent", table: "integration_event_mapping_intents", columns: []string{"workspace_id", "event_id"}, unique: true},
		{name: "idx_integration_event_mapping_intent_status", table: "integration_event_mapping_intents", columns: []string{"workspace_id", "status", "created_at"}},
		{name: "uniq_transaction_boundary_intent", table: "transaction_boundary_intents", columns: []string{"workspace_id", "owner", "operation", "idempotency_key"}, unique: true},
		{name: "idx_transaction_boundary_intent_due", table: "transaction_boundary_intents", columns: []string{"status", "next_attempt_at", "lease_expires_at"}},
		{name: "uniq_integration_api_keys_workspace_key", table: "integration_api_keys", columns: []string{"workspace_id", "api_key"}, unique: true},
		{name: "uniq_integration_api_keys_token_hash", table: "integration_api_keys", columns: []string{"token_hash"}, unique: true},
		{name: "idx_integration_api_keys_status", table: "integration_api_keys", columns: []string{"workspace_id", "status"}},
		{name: "idx_integration_events_status", table: "integration_events", columns: []string{"workspace_id", "provider", "status"}},
		{name: "uniq_integration_webhook_nonce", table: "integration_webhook_nonces", columns: []string{"workspace_id", "connector_key", "nonce"}, unique: true},
		{name: "idx_integration_webhook_nonce_expiry", table: "integration_webhook_nonces", columns: []string{"expires_at"}},
		{name: "uniq_integration_webhook_subscription", table: "integration_webhook_subscriptions", columns: []string{"workspace_id", "subscription_key"}, unique: true},
		{name: "idx_integration_webhook_subscription_connection", table: "integration_webhook_subscriptions", columns: []string{"workspace_id", "connection_key"}},
		{name: "idx_integration_outbox_connector", table: "integration_outbox_messages", columns: []string{"workspace_id", "connector_key", "status"}},
		{name: "idx_integration_outbox_due", table: "integration_outbox_messages", columns: []string{"status", "next_attempt_at"}},
		{name: "idx_integration_outbox_ack_due", table: "integration_outbox_messages", columns: []string{"status", "ack_deadline_at"}},
		{name: "uniq_integration_outbox_dedup", table: "integration_outbox_messages", columns: []string{"workspace_id", "connector_key", "connection_key", "operation", "dedup_key"}, unique: true},
		{name: "uniq_notification_publication_source", table: "notification_publication_outbox", columns: []string{"tenant_id", "workspace_id", "application_key", "source_event_id"}, unique: true},
		{name: "idx_notification_publication_due", table: "notification_publication_outbox", columns: []string{"status", "next_attempt_at", "lease_expires_at", "created_at"}},
		{name: "uniq_integration_credential_refresh_lease", table: "integration_credential_refresh_leases", columns: []string{"workspace_id", "connection_key"}, unique: true},
		{name: "idx_integration_credential_refresh_lease_expiry", table: "integration_credential_refresh_leases", columns: []string{"lease_expires_at"}},
		{name: "uniq_integration_secret_material", table: "integration_secret_materials", columns: []string{"workspace_id", "secret_key"}, unique: true},
		{name: "uniq_integration_secrets_workspace_key", table: "integration_secrets", columns: []string{"workspace_id", "secret_key"}, unique: true},
		{name: "idx_integration_secrets_status", table: "integration_secrets", columns: []string{"workspace_id", "status"}},
		{name: "uniq_connector_provider_state", table: "connector_provider_states", columns: []string{"workspace_id", "connector_key", "provider_key", "connection_key", "task_key"}, unique: true},
		{name: "idx_connector_provider_state_due", table: "connector_provider_states", columns: []string{"status", "due_at", "lease_expires_at"}},
		{name: "idx_automation_execution_rule", table: "automation_rule_executions", columns: []string{"workspace_id", "rule_key", "created_at"}},
		{name: "idx_automation_execution_record", table: "automation_rule_executions", columns: []string{"workspace_id", "object_key", "record_id", "created_at"}},
		{name: "idx_automation_execution_status", table: "automation_rule_executions", columns: []string{"workspace_id", "status", "created_at"}},
		{name: "idx_automation_instruction_idempotency", table: "automation_instruction_executions", columns: []string{"workspace_id", "idempotency_key"}, unique: true},
		{name: "idx_automation_instruction_status", table: "automation_instruction_executions", columns: []string{"workspace_id", "status", "lease_expires_at"}},
		{name: "uniq_business_action_execution_scope", table: "business_action_executions", columns: []string{"workspace_id", "object_key", "record_id", "action_key", "idempotency_key"}, unique: true},
		{name: "idx_business_action_execution_lease", table: "business_action_executions", columns: []string{"status", "lease_expires_at"}},
		{name: "uniq_action_assurance_token_hash", table: "action_assurance_grants", columns: []string{"token_hash"}, unique: true},
		{name: "idx_action_assurance_binding", table: "action_assurance_grants", columns: []string{"workspace_id", "user_id", "action_key", "object_key", "record_id"}},
		{name: "idx_action_assurance_expiry", table: "action_assurance_grants", columns: []string{"expires_at", "consumed_at"}},
		{name: "uniq_record_mutation_execution_scope", table: "record_mutation_executions", columns: []string{"workspace_id", "operation", "object_key", "target_id", "idempotency_key"}, unique: true},
		{name: "idx_record_mutation_execution_lease", table: "record_mutation_executions", columns: []string{"status", "lease_expires_at"}},
		{name: "uniq_record_batch_job_idempotency", table: "record_batch_jobs", columns: []string{"workspace_id", "kind", "object_key", "idempotency_key"}, unique: true},
		{name: "idx_record_batch_job_due", table: "record_batch_jobs", columns: []string{"status", "next_attempt_at", "lease_expires_at", "created_at"}},
		{name: "uniq_record_batch_job_chunk", table: "record_batch_job_chunks", columns: []string{"workspace_id", "job_id", "sequence_no"}, unique: true},
		{name: "uniq_runtime_worker_queue_scope", table: "runtime_worker_queue_scopes", columns: []string{"queue_kind", "scope_key"}, unique: true},
		{name: "uniq_workflow_execution_workspace_id", table: "_workflow_executions", columns: []string{"workspace_id", "id"}, unique: true},
		{name: "idx_workflow_execution_process", table: "_workflow_executions", columns: []string{"workspace_id", "process_id", "node_id", "status"}},
		{name: "uniq_workflow_execution_receipt_scope", table: "workflow_execution_receipts", columns: []string{"workspace_id", "workflow_key", "idempotency_key"}, unique: true},
		{name: "idx_workflow_execution_receipt_lease", table: "workflow_execution_receipts", columns: []string{"status", "lease_expires_at"}},
		{name: "uniq_runtime_operation_key", table: "runtime_operations", columns: []string{"workspace_id", "system_purpose", "kind", "idempotency_key"}, unique: true},
		{name: "idx_runtime_operation_status", table: "runtime_operations", columns: []string{"workspace_id", "status", "created_at"}},
		{name: "uniq_report_snapshot_idempotency", table: "report_snapshots", columns: []string{"workspace_id", "report_key", "access_scope_hash", "idempotency_key"}, unique: true},
		{name: "idx_report_snapshot_latest", table: "report_snapshots", columns: []string{"workspace_id", "report_key", "access_scope_hash", "status", "refreshed_at"}},
		{name: "uniq_report_export_idempotency", table: "report_export_artifacts", columns: []string{"workspace_id", "requester_user_id", "report_key", "idempotency_key"}, unique: true},
		{name: "uniq_report_export_token", table: "report_export_artifacts", columns: []string{"workspace_id", "token"}, unique: true},
		{name: "idx_report_export_expiry", table: "report_export_artifacts", columns: []string{"workspace_id", "expires_at"}},
		{name: "uniq_business_audit_export_idempotency", table: "business_audit_export_artifacts", columns: []string{"workspace_id", "requester_user_id", "idempotency_key"}, unique: true},
		{name: "uniq_business_audit_export_token_hash", table: "business_audit_export_artifacts", columns: []string{"workspace_id", "token_sha256"}, unique: true},
		{name: "idx_business_audit_export_expiry", table: "business_audit_export_artifacts", columns: []string{"workspace_id", "expires_at"}},
		{name: "idx_audit_event_actor_cursor", table: "_audit_events", columns: auditEventActorCursorColumns()},
		{name: "idx_audit_event_record_cursor", table: "_audit_events", columns: auditEventRecordCursorColumns()},
		{name: "uniq_runtime_operation_control", table: "runtime_operation_controls", columns: []string{"system_purpose", "control_kind", "owner"}, unique: true},
		{name: "idx_runtime_operation_control_state", table: "runtime_operation_controls", columns: []string{"system_purpose", "control_kind", "state"}},
		{name: "idx_runtime_release_instance_expiry", table: "runtime_release_instances", columns: []string{"lease_expires_at"}},
		{name: "idx_runtime_release_instance_cohort", table: "runtime_release_instances", columns: []string{"generation", "combination_sha256"}},
		{name: "idx_runtime_break_glass_active", table: "runtime_break_glass_grants", columns: []string{"workspace_id", "state", "expires_at"}},
		{name: "uniq_runtime_break_glass_audit", table: "runtime_break_glass_grants", columns: []string{"workspace_id", "audit_event_id"}, unique: true},
		{name: "uniq_runtime_database_retirement_object", table: "runtime_database_retirements", columns: []string{"engine", "database_name", "schema_name", "object_kind", "object_name", "parent_name"}, unique: true},
		{name: "idx_runtime_database_retirement_status", table: "runtime_database_retirements", columns: []string{"state", "updated_at"}},
		{name: "uniq_integration_external_identities_workspace_key", table: "integration_external_identities", columns: []string{"workspace_id", "identity_key"}, unique: true},
		{name: "uniq_integration_external_identities_subject", table: "integration_external_identities", columns: []string{"workspace_id", "provider", "external_subject_type", "external_subject"}, unique: true},
		{name: "idx_integration_external_identities_actor", table: "integration_external_identities", columns: []string{"actor_id", "role_key"}},
	}
	for _, index := range indexes {
		if err := s.CreateIndexIfMissing(ctx, index.table, index.name, index.unique, index.columns...); err != nil {
			return fmt.Errorf("create %s: %w", index.name, err)
		}
	}
	return nil
}

func ensureMySQLLargeEvidenceColumns(ctx context.Context, s Store) error {
	if s.Driver() != "mysql" {
		return nil
	}
	specs := map[string][]string{
		"report_export_artifacts":         {"content_base64"},
		"business_audit_export_artifacts": {"content_base64"},
		"record_batch_job_chunks":         {"content"},
	}
	query := "SELECT TABLE_NAME, COLUMN_NAME, DATA_TYPE FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND ((TABLE_NAME = " + s.Placeholder(1) + " AND COLUMN_NAME = " + s.Placeholder(2) + ") OR (TABLE_NAME = " + s.Placeholder(3) + " AND COLUMN_NAME = " + s.Placeholder(4) + ") OR (TABLE_NAME = " + s.Placeholder(5) + " AND COLUMN_NAME = " + s.Placeholder(6) + "))"
	rows, err := s.SchemaDB().QueryContext(ctx, query,
		"report_export_artifacts", "content_base64",
		"business_audit_export_artifacts", "content_base64",
		"record_batch_job_chunks", "content",
	)
	if err != nil {
		return fmt.Errorf("inspect MySQL large evidence columns: %w", err)
	}
	modifications := map[string][]string{}
	for rows.Next() {
		var table, column, dataType string
		if err := rows.Scan(&table, &column, &dataType); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan MySQL large evidence column: %w", err)
		}
		if !strings.EqualFold(dataType, "longtext") {
			modifications[table] = append(modifications[table], column)
		}
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate MySQL large evidence columns: %w", err)
	}
	_ = rows.Close()
	for table, columns := range modifications {
		allowed := specs[table]
		for _, column := range columns {
			valid := false
			for _, candidate := range allowed {
				valid = valid || column == candidate
			}
			if !valid {
				return fmt.Errorf("inspect MySQL large evidence columns: unexpected %s.%s", table, column)
			}
			statement := "ALTER TABLE " + s.TableIdentifier(table) + " MODIFY COLUMN " + s.Identifier(column) + " LONGTEXT NOT NULL"
			if _, err := s.SchemaDB().ExecContext(ctx, statement); err != nil {
				return fmt.Errorf("normalize MySQL large evidence column %s.%s: %w", table, column, err)
			}
		}
	}
	return nil
}

func auditEventActorCursorColumns() []string {
	return []string{"workspace_id", "actor_id", "created_at", "id"}
}

func auditEventRecordCursorColumns() []string {
	return []string{"workspace_id", "object_key", "record_id", "created_at", "id"}
}

func ensureMySQLAuditCursorColumns(ctx context.Context, s Store) error {
	if s.Driver() != "mysql" {
		return nil
	}
	type columnSpec struct {
		name        string
		nullability string
	}
	specs := []columnSpec{{name: "id", nullability: "NOT NULL"}, {name: "created_at", nullability: "NOT NULL"}}
	query := "SELECT COLUMN_NAME, COLUMN_TYPE, CHARACTER_SET_NAME, COLLATION_NAME FROM information_schema.COLUMNS WHERE TABLE_SCHEMA = DATABASE() AND TABLE_NAME = " + s.Placeholder(1) + " AND COLUMN_NAME IN (" + s.Placeholder(2) + ", " + s.Placeholder(3) + ")"
	rows, err := s.SchemaDB().QueryContext(ctx, query, "_audit_events", specs[0].name, specs[1].name)
	if err != nil {
		return fmt.Errorf("inspect MySQL audit cursor columns: %w", err)
	}
	type columnState struct{ columnType, characterSet, collation string }
	states := map[string]columnState{}
	for rows.Next() {
		var name string
		var state columnState
		if err := rows.Scan(&name, &state.columnType, &state.characterSet, &state.collation); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan MySQL audit cursor column: %w", err)
		}
		states[name] = state
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return fmt.Errorf("iterate MySQL audit cursor columns: %w", err)
	}
	_ = rows.Close()
	modifications := make([]string, 0, len(specs))
	for _, spec := range specs {
		state, exists := states[spec.name]
		if !exists {
			return fmt.Errorf("inspect MySQL audit cursor columns: %s is missing", spec.name)
		}
		if strings.EqualFold(state.columnType, "varchar(191)") && strings.EqualFold(state.characterSet, "ascii") && strings.EqualFold(state.collation, "ascii_bin") {
			continue
		}
		modifications = append(modifications, "MODIFY COLUMN "+s.Identifier(spec.name)+" "+mysqlAuditCursorColumnType+" "+spec.nullability)
	}
	if len(modifications) == 0 {
		return nil
	}
	if _, err := s.SchemaDB().ExecContext(ctx, "ALTER TABLE "+s.TableIdentifier("_audit_events")+" "+strings.Join(modifications, ", ")); err != nil {
		return fmt.Errorf("normalize MySQL audit cursor columns: %w", err)
	}
	return nil
}

func backfillWorkerQueueScopes(ctx context.Context, s Store, queueKind, table string) error {
	queueKind, table = strings.TrimSpace(queueKind), strings.TrimSpace(table)
	query := "SELECT " + s.Identifier("workspace_id") + ", MAX(" + s.Identifier("updated_at") + ") FROM " + s.TableIdentifier(table) + " GROUP BY " + s.Identifier("workspace_id")
	rows, err := s.SchemaDB().QueryContext(ctx, query)
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
		insert := "INSERT INTO " + s.TableIdentifier("runtime_worker_queue_scopes") + " (" + s.Identifier("id") + ", " + s.Identifier("queue_kind") + ", " + s.Identifier("scope_key") + ", " + s.Identifier("updated_at") + ") VALUES (" + s.Placeholder(1) + ", " + s.Placeholder(2) + ", " + s.Placeholder(3) + ", " + s.Placeholder(4) + ")"
		if s.Driver() == "mysql" {
			insert += " ON DUPLICATE KEY UPDATE " + s.Identifier("updated_at") + " = VALUES(" + s.Identifier("updated_at") + ")"
		} else {
			insert += " ON CONFLICT DO NOTHING"
		}
		if _, err := s.SchemaDB().ExecContext(ctx, insert, id, queueKind, value.workspaceID, value.updatedAt); err != nil {
			return fmt.Errorf("backfill %s worker queue scope: %w", queueKind, err)
		}
		if s.Driver() != "mysql" {
			update := "UPDATE " + s.TableIdentifier("runtime_worker_queue_scopes") + " SET " + s.Identifier("updated_at") + " = " + s.Placeholder(1) + " WHERE " + s.Identifier("queue_kind") + " = " + s.Placeholder(2) + " AND " + s.Identifier("scope_key") + " = " + s.Placeholder(3)
			if _, err := s.SchemaDB().ExecContext(ctx, update, value.updatedAt, queueKind, value.workspaceID); err != nil {
				return fmt.Errorf("refresh %s worker queue scope: %w", queueKind, err)
			}
		}
	}
	return nil
}

func runtimeSchemaTableExists(ctx context.Context, s Store, table string) (bool, error) {
	table = strings.TrimSpace(table)
	var count int
	var err error
	switch s.Driver() {
	case "postgres":
		schema := strings.TrimSpace(s.DatabaseSchema())
		if schema == "" {
			schema = "public"
		}
		err = s.SchemaDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = "+s.Placeholder(1)+" AND table_name = "+s.Placeholder(2), schema, table).Scan(&count)
	case "mysql":
		err = s.SchemaDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = "+s.Placeholder(1), table).Scan(&count)
	default:
		err = s.SchemaDB().QueryRowContext(ctx, "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = "+s.Placeholder(1), table).Scan(&count)
	}
	if err != nil {
		return false, fmt.Errorf("inspect runtime schema table %s: %w", table, err)
	}
	return count > 0, nil
}
