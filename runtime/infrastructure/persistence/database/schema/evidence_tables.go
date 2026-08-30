package schema

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"

	ormbuilder "github.com/domainry/domainry-orm/builder"
)

func ensureEvidenceTables(ctx context.Context, s Store, tables map[string][]string, text string) error {
	workspaceIdentities := prepareWorkspaceScopedIdentities(tables)
	for _, table := range sortedRuntimeSchemaTables(tables) {
		if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier(table)+" ("+quotedColumnDefinitions(s, tables[table])+")"); err != nil {
			return fmt.Errorf("create %s: %w", table, err)
		}
	}
	if err := s.RuntimeProfile().NormalizeEvidenceSchema(ctx, s.SchemaDB(), s.RuntimeRenderer()); err != nil {
		return err
	}
	if err := ensureWorkspaceScopedIdentities(ctx, s, workspaceIdentities); err != nil {
		return err
	}
	// Backfill uses an engine-native upsert on this business key.
	if err := s.CreateIndexIfMissing(ctx, "runtime_worker_queue_scopes", "uniq_runtime_worker_queue_scope", true, "queue_kind", "scope_key"); err != nil {
		return fmt.Errorf("create uniq_runtime_worker_queue_scope: %w", err)
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
		"lease_owner": text + " NOT NULL DEFAULT ''", "lease_expires_at": text + " NOT NULL DEFAULT ''", "fencing_token": "BIGINT NOT NULL DEFAULT 0",
	} {
		if err := s.EnsureRuntimeColumn(ctx, "report_snapshots", column, definition); err != nil {
			return err
		}
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
		{name: "idx_notification_service_publication_due", table: "notification_publication_outbox", columns: []string{"status", "next_attempt_at", "lease_expires_at", "created_at"}},
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
		{name: "uniq_workflow_execution_workspace_id", table: "_workflow_executions", columns: []string{"workspace_id", "id"}, unique: true},
		{name: "idx_workflow_execution_process", table: "_workflow_executions", columns: []string{"workspace_id", "process_id", "node_id", "status"}},
		{name: "uniq_workflow_execution_receipt_scope", table: "workflow_execution_receipts", columns: []string{"workspace_id", "workflow_key", "idempotency_key"}, unique: true},
		{name: "idx_workflow_execution_receipt_lease", table: "workflow_execution_receipts", columns: []string{"status", "lease_expires_at"}},
		{name: "uniq_runtime_operation_key", table: "runtime_operations", columns: []string{"workspace_id", "system_purpose", "kind", "idempotency_key"}, unique: true},
		{name: "idx_runtime_operation_status", table: "runtime_operations", columns: []string{"workspace_id", "status", "created_at"}},
		{name: "uniq_report_snapshot_idempotency", table: "report_snapshots", columns: []string{"workspace_id", "report_key", "access_scope_hash", "idempotency_key"}, unique: true},
		{name: "idx_report_snapshot_latest", table: "report_snapshots", columns: []string{"workspace_id", "report_key", "access_scope_hash", "status", "refreshed_at"}},
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
		insert := ormbuilder.NewInsertBuilder(s.RuntimeRenderer(), "runtime_worker_queue_scopes").
			Columns("id", "queue_kind", "scope_key", "updated_at").
			Values(id, queueKind, value.workspaceID, value.updatedAt)
		insert, err = s.RuntimeProfile().ApplyUpsert(insert, []string{"queue_kind", "scope_key"}, ormbuilder.Assign("updated_at", value.updatedAt))
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
	if err != nil {
		return false, fmt.Errorf("inspect runtime schema table %s: %w", table, err)
	}
	return exists, nil
}
