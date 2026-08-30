package boundary_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLifecyclePersistenceDoesNotKnowSourceOwnedBusinessTables(t *testing.T) {
	root := filepath.Join("..", "..", "..", "domainry-lifecycle", "internal", "infrastructure", "persistence", "database", "lifecycle")
	foreignTables := []string{
		"_action_executions", "_record_mutation_executions", "_operation_requests", "_operation_break_glass_grants",
		"_workflow_definition_versions", "_workflow_process_instances", "_workflow_process_events", "_workflow_tasks", "_workflow_node_instances",
		"_automation_rule_executions", "_automation_instruction_executions", "_audit_events", "_rate_limit_buckets",
		"_publication_outbox", "download_task", "report_export_audit", "report_query_run",
	}
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		for _, table := range foreignTables {
			if strings.Contains(string(raw), `"`+table+`"`) {
				t.Errorf("Lifecycle persistence knows source-owned table %q in %s", table, filepath.ToSlash(path))
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestLifecycleModuleHasNoRuntimeOrDriverBranchDependency(t *testing.T) {
	root := filepath.Join("..", "..", "..", "domainry-lifecycle")
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		text := string(raw)
		if strings.Contains(text, "github.com/domainry/domainry-runtime/") {
			t.Errorf("Lifecycle module imports Runtime in %s", path)
		}
		for _, forbidden := range []string{`Driver()`, `driver ==`, `case "sqlite"`, `case "mysql"`, `case "postgres"`} {
			if strings.Contains(text, forbidden) {
				t.Errorf("Lifecycle module contains driver branching token %q in %s", forbidden, path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestRuntimeSchemaDoesNotOwnLifecycleTables(t *testing.T) {
	root := filepath.Join("..", "infrastructure", "persistence", "database", "schema")
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if strings.Contains(string(raw), `"lifecycle_`) {
			t.Errorf("Runtime schema still owns Lifecycle table in %s", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestOperationsTransportDoesNotImplementLifecycleHandlers(t *testing.T) {
	root := filepath.Join("..", "transport", "http", "operations")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") || strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(root, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "lifecycleapplication") || strings.Contains(string(raw), "func (h *OperationsHandler) lifecycle") {
			t.Errorf("Operations transport still owns Lifecycle behavior in %s", entry.Name())
		}
	}
}
