package database_test

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestFreshRuntimeSchemaUsesOwnedBusinessNamesAndOneHostLedger(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{
		DatabaseDriver: "sqlite",
		DBPath:         filepath.Join(t.TempDir(), "internal-table-naming.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}

	rows, err := store.DB().QueryContext(t.Context(), `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var actual []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		actual = append(actual, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	expected := []string{
		"_action_assurance_grants",
		"_action_executions",
		"_application_schema_automation_rule_definitions",
		"_application_schema_connector_requirements",
		"_application_schema_exact_decimal_migration_receipts",
		"_application_schema_integration_event_mapping_requirements",
		"_application_schema_localized_texts",
		"_application_schema_projection",
		"_application_schema_seed_checkpoints",
		"_application_schema_workflow_definitions",
		"_audit_events",
		"_audit_export_artifacts",
		"_automation_instruction_executions",
		"_automation_rule_executions",
		"_idempotency_cleanup_leases",
		"_lifecycle_archive_entries",
		"_lifecycle_audit_evidence",
		"_lifecycle_cleanup_jobs",
		"_lifecycle_deletion_registry",
		"_lifecycle_external_erasure_requests",
		"_lifecycle_file_artifacts",
		"_lifecycle_legal_holds",
		"_lifecycle_policy_versions",
		"_lifecycle_subject_requests",
		"_metadata_action_definitions",
		"_metadata_definition_versions",
		"_metadata_dictionary_definitions",
		"_metadata_field_definitions",
		"_metadata_object_definitions",
		"_metadata_role_definitions",
		"_metadata_validation_definitions",
		"_operation_break_glass_grants",
		"_operation_controls",
		"_operation_database_retirements",
		"_operation_requests",
		"_publication_outbox",
		"_rate_limit_buckets",
		"_record_localized_values",
		"_record_mutation_executions",
		"_release_cohorts",
		"_release_instances",
		"_schema_migrations",
		"_tenant_registry",
		"_transaction_boundary_intents",
		"_worker_queue_scopes",
		"_workflow_definition_versions",
		"_workflow_definitions",
		"_workflow_execution_receipts",
		"_workflow_executions",
		"_workflow_node_instances",
		"_workflow_process_events",
		"_workflow_process_instances",
		"_workflow_tasks",
		"_workspace_configuration",
		"_workspace_provisioning_receipts",
		"_workspaces",
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("fresh Runtime table inventory changed\nactual:   %q\nexpected: %q", actual, expected)
	}

	ledgerCount := 0
	for _, name := range actual {
		if !strings.HasPrefix(name, "_") {
			t.Errorf("built-in table %q must begin with an ownership underscore", name)
		}
		if strings.HasPrefix(name, "_runtime_") {
			t.Errorf("Runtime table %q must use its concrete business owner", name)
		}
		if strings.HasSuffix(name, "schema_migrations") {
			ledgerCount++
		}
	}
	if ledgerCount != 1 {
		t.Fatalf("fresh Runtime database has %d migration ledgers, want only _schema_migrations", ledgerCount)
	}
}
