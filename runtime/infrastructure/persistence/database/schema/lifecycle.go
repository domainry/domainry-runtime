package schema

import (
	"context"
	"fmt"
)

func EnsureLifecycleSchema(ctx context.Context, s Store) error {
	text := s.MetadataIDColumnType()
	tables := map[string][]string{
		"lifecycle_policy_versions": {
			"workspace_id " + text + " NOT NULL", "policy_key " + text + " NOT NULL", "version " + text + " NOT NULL", "revision BIGINT NOT NULL", "status " + text + " NOT NULL", "payload_json TEXT NOT NULL", "published_at " + text + " NOT NULL",
		},
		"lifecycle_legal_holds": {
			"id " + text + " NOT NULL",
			"workspace_id " + text + " NOT NULL",
			"owner " + text + " NOT NULL DEFAULT ''", "resource_type " + text + " NOT NULL DEFAULT ''", "resource_id " + text + " NOT NULL DEFAULT ''", "starts_at " + text + " NOT NULL", "ends_at " + text + " NOT NULL DEFAULT ''", "review_at " + text + " NOT NULL", "payload_json TEXT NOT NULL",
		},
		"lifecycle_cleanup_jobs": {
			"id " + text + " NOT NULL",
			"workspace_id " + text + " NOT NULL",
			"policy_key " + text + " NOT NULL", "policy_version " + text + " NOT NULL", "status " + text + " NOT NULL", "checkpoint_value " + text + " NOT NULL DEFAULT ''", "lease_owner " + text + " NOT NULL DEFAULT ''", "lease_expires_at " + text + " NOT NULL DEFAULT ''", "fencing_token BIGINT NOT NULL DEFAULT 0", "updated_at " + text + " NOT NULL", "payload_json TEXT NOT NULL",
		},
		"lifecycle_subject_requests": {
			"id " + text + " NOT NULL",
			"workspace_id " + text + " NOT NULL",
			"kind " + text + " NOT NULL", "status " + text + " NOT NULL", "subject_id " + text + " NOT NULL", "resolved_identity " + text + " NOT NULL DEFAULT ''", "download_expires_at " + text + " NOT NULL DEFAULT ''", "updated_at " + text + " NOT NULL", "payload_json TEXT NOT NULL",
		},
		"lifecycle_external_erasures": {
			"id " + text + " NOT NULL", "request_id " + text + " NOT NULL",
			"workspace_id " + text + " NOT NULL",
			"status " + text + " NOT NULL", "payload_json TEXT NOT NULL",
		},
		"lifecycle_audit_evidence": {
			"id " + text + " NOT NULL",
			"workspace_id " + text + " NOT NULL",
			"event " + text + " NOT NULL", "resource_id " + text + " NOT NULL", "policy_key " + text + " NOT NULL DEFAULT ''", "created_at " + text + " NOT NULL", "payload_json TEXT NOT NULL",
		},
		"lifecycle_archive_entries": {
			"id " + text + " NOT NULL",
			"workspace_id " + text + " NOT NULL",
			"owner " + text + " NOT NULL", "source_table " + text + " NOT NULL", "resource_id " + text + " NOT NULL", "policy_key " + text + " NOT NULL", "policy_version " + text + " NOT NULL", "job_id " + text + " NOT NULL", "payload_hash " + text + " NOT NULL", "payload_json TEXT NOT NULL", "archived_at " + text + " NOT NULL",
		},
		"lifecycle_deletion_registry": {
			"request_id " + text + " NOT NULL",
			"workspace_id " + text + " NOT NULL",
			"resolved_identity " + text + " NOT NULL", "backup_pending BOOLEAN NOT NULL DEFAULT TRUE", "evidence " + text + " NOT NULL", "updated_at " + text + " NOT NULL",
		},
		"lifecycle_file_artifacts": {
			"id " + text + " NOT NULL", "workspace_id " + text + " NOT NULL", "object_key " + text + " NOT NULL", "field_key " + text + " NOT NULL", "filename " + text + " NOT NULL", "content_type " + text + " NOT NULL", "sha256 " + text + " NOT NULL", "size_bytes BIGINT NOT NULL", "status " + text + " NOT NULL", "scan_status " + text + " NOT NULL DEFAULT 'pending'", "scan_provider " + text + " NOT NULL DEFAULT ''", "scan_evidence_ref " + text + " NOT NULL DEFAULT ''", "scanned_at " + text + " NOT NULL DEFAULT ''", "created_at " + text + " NOT NULL", "last_referenced_at " + text + " NOT NULL DEFAULT ''", "delete_after " + text + " NOT NULL DEFAULT ''", "deleted_at " + text + " NOT NULL DEFAULT ''",
		},
	}
	for _, table := range sortedRuntimeSchemaTables(tables) {
		if _, err := s.SchemaDB().ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.TableIdentifier(table)+" ("+quotedColumnDefinitions(s, tables[table])+")"); err != nil {
			return fmt.Errorf("create %s: %w", table, err)
		}
	}
	for _, migration := range []struct{ column, definition string }{
		{"scan_status", text + " NOT NULL DEFAULT 'pending'"}, {"scan_provider", text + " NOT NULL DEFAULT ''"}, {"scan_evidence_ref", text + " NOT NULL DEFAULT ''"}, {"scanned_at", text + " NOT NULL DEFAULT ''"},
	} {
		column, definition := migration.column, migration.definition
		if err := s.EnsureRuntimeColumn(ctx, "lifecycle_file_artifacts", column, definition); err != nil {
			return fmt.Errorf("ensure lifecycle_file_artifacts.%s: %w", column, err)
		}
	}
	indexes := []struct {
		table, name string
		unique      bool
		columns     []string
	}{
		{"lifecycle_policy_versions", "uniq_lifecycle_policy_version", true, []string{"workspace_id", "policy_key", "version"}},
		{"lifecycle_policy_versions", "uniq_lifecycle_policy_revision", true, []string{"workspace_id", "policy_key", "revision"}},
		{"lifecycle_legal_holds", "uniq_lifecycle_hold_workspace_identity", true, []string{"workspace_id", "id"}},
		{"lifecycle_legal_holds", "idx_lifecycle_hold_scope", false, []string{"workspace_id", "owner", "resource_type", "resource_id"}},
		{"lifecycle_cleanup_jobs", "uniq_lifecycle_cleanup_workspace_identity", true, []string{"workspace_id", "id"}},
		{"lifecycle_cleanup_jobs", "idx_lifecycle_cleanup_claim", false, []string{"workspace_id", "status", "lease_expires_at", "updated_at"}},
		{"lifecycle_subject_requests", "uniq_lifecycle_subject_workspace_identity", true, []string{"workspace_id", "id"}},
		{"lifecycle_subject_requests", "idx_lifecycle_subject_identity", false, []string{"workspace_id", "subject_id", "status", "updated_at"}},
		{"lifecycle_external_erasures", "uniq_lifecycle_external_workspace_identity", true, []string{"workspace_id", "id"}},
		{"lifecycle_external_erasures", "idx_lifecycle_external_request", false, []string{"workspace_id", "request_id", "status"}},
		{"lifecycle_audit_evidence", "uniq_lifecycle_audit_workspace_identity", true, []string{"workspace_id", "id"}},
		{"lifecycle_audit_evidence", "idx_lifecycle_audit_workspace", false, []string{"workspace_id", "created_at"}},
		{"lifecycle_archive_entries", "uniq_lifecycle_archive_workspace_identity", true, []string{"workspace_id", "id"}},
		{"lifecycle_archive_entries", "idx_lifecycle_archive_source", false, []string{"workspace_id", "source_table", "resource_id", "archived_at"}},
		{"lifecycle_deletion_registry", "uniq_lifecycle_deletion_workspace_identity", true, []string{"workspace_id", "request_id"}},
		{"lifecycle_file_artifacts", "uniq_lifecycle_file_workspace_name", true, []string{"workspace_id", "filename"}},
		{"lifecycle_file_artifacts", "uniq_lifecycle_file_workspace_identity", true, []string{"workspace_id", "id"}},
		{"lifecycle_file_artifacts", "idx_lifecycle_file_cleanup", false, []string{"status", "delete_after", "created_at"}},
	}
	for _, index := range indexes {
		if err := s.CreateIndexIfMissing(ctx, index.table, index.name, index.unique, index.columns...); err != nil {
			return fmt.Errorf("create %s: %w", index.name, err)
		}
	}
	return nil
}
