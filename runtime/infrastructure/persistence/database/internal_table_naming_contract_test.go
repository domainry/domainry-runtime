package database_test

import (
	"path/filepath"
	"reflect"
	"slices"
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
		"_artifact_bindings",
		"_artifacts",
		"_automation_runs",
		"_operation_break_glass_grants",
		"_operation_controls",
		"_operations",
		"_project_model_state",
		"_publication_outbox",
		"_rate_limit_buckets",
		"_record_localized_values",
		"_release_cohorts",
		"_release_instances",
		"_schema_migrations",
		"_worker_scopes",
		"_workflow_executions",
		"_workflow_node_instances",
		"_workflow_process_events",
		"_workflow_process_instances",
		"_workflow_route_steps",
		"_workflow_tasks",
		"_workspaces",
	}
	if !reflect.DeepEqual(actual, expected) {
		t.Fatalf("fresh Runtime table inventory changed\nactual:   %q\nexpected: %q", actual, expected)
	}
	for _, retired := range []string{"_automation_rule_executions", "_automation_instruction_executions", "_subject_evidence_erasure_fences", "_subject_evidence_erasure_receipts", "_workflow_definitions", "_workflow_definition_versions"} {
		if slices.Contains(actual, retired) {
			t.Fatalf("fresh Runtime schema retained retired automation table %q", retired)
		}
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
