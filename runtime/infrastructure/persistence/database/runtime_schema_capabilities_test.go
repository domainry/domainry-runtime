package database

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestRuntimeSchemaInstallsOnlySelectedNativeCapabilities(t *testing.T) {
	open := func(name string, capabilities RuntimeSchemaCapabilities) *RuntimeStore {
		t.Helper()
		store, err := OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), name+".db")})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.EnsureRuntimeSchemaFor(t.Context(), capabilities); err != nil {
			_ = store.Close()
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.Close() })
		return store
	}

	optionalTables := []string{
		"_workflow_executions", "_workflow_process_instances",
		"_workflow_node_instances",
		"_workflow_tasks", "_workflow_process_events", "_workflow_route_steps",
		"_automation_runs",
		"_release_cohorts", "_release_instances",
	}
	minimal := open("minimal", RuntimeSchemaCapabilities{})
	for _, table := range optionalTables {
		if runtimeSchemaTablePresent(t, minimal, table) {
			t.Fatalf("minimal Runtime schema installed unselected table %q", table)
		}
	}
	var lifecycleTables int
	if err := minimal.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name LIKE '_lifecycle_%'`).Scan(&lifecycleTables); err != nil {
		t.Fatal(err)
	}
	if lifecycleTables != 0 {
		t.Fatalf("minimal Runtime schema installed %d Lifecycle module tables", lifecycleTables)
	}
	if _, err := minimal.DB().ExecContext(t.Context(), `CREATE TABLE customer (workspace_id TEXT NOT NULL, id TEXT NOT NULL, PRIMARY KEY (workspace_id, id))`); err != nil {
		t.Fatal(err)
	}
	insert, err := minimal.SubjectEvidenceInsertBuilder("workspace-a", "customer", []string{"id"}, []any{"customer-1"})
	if err != nil {
		t.Fatal(err)
	}
	statement, args, err := insert.Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := minimal.DB().ExecContext(t.Context(), statement, args...); err != nil {
		t.Fatalf("minimal CRUD write referenced unselected Lifecycle fencing: %v", err)
	}
	for _, table := range []string{"_project_model_state", "_worker_scopes", "_rate_limit_buckets"} {
		if !runtimeSchemaTablePresent(t, minimal, table) {
			t.Fatalf("minimal Runtime schema omitted core table %q", table)
		}
	}

	full := open("full", FullRuntimeSchemaCapabilities())
	for _, table := range optionalTables {
		if !runtimeSchemaTablePresent(t, full, table) {
			t.Fatalf("full Runtime schema omitted selected table %q", table)
		}
	}
	if runtimeSchemaTablePresent(t, full, "_report_export_prepare_receipts") {
		t.Fatal("full Runtime schema retained dedicated report export receipt storage")
	}
	if runtimeSchemaTablePresent(t, full, "_upload_subject_bindings") {
		t.Fatal("full Runtime schema retained dedicated upload subject binding storage")
	}
	for _, retired := range []string{"_subject_evidence_erasure_fences", "_subject_evidence_erasure_receipts", "_workflow_definitions", "_workflow_definition_versions"} {
		if runtimeSchemaTablePresent(t, full, retired) {
			t.Fatalf("full Runtime schema retained dedicated subject evidence table %q", retired)
		}
	}
}

func TestRuntimeSchemaCapabilitySetIsPartOfMigrationContract(t *testing.T) {
	store, err := OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "runtime.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchemaFor(t.Context(), RuntimeSchemaCapabilities{}); err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureRuntimeSchemaFor(t.Context(), FullRuntimeSchemaCapabilities()); err == nil || !strings.Contains(err.Error(), "migration.checksum_drift") {
		t.Fatalf("changed Runtime capability contract error=%v", err)
	}
}

func runtimeSchemaTablePresent(t *testing.T, store *RuntimeStore, table string) bool {
	t.Helper()
	var count int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count == 1
}
