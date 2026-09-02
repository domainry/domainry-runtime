package query_test

import (
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"os"
	"path/filepath"
	"strings"
	"testing"

	. "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestRuntimeOperationalQueriesUseWorkflowAndHierarchyIndexes(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatalf("ensure runtime schema: %v", err)
	}

	cases := []struct {
		name  string
		query string
		args  []any
		index string
	}{
		{name: "approval inbox", query: `SELECT id FROM _workflow_tasks WHERE workspace_id = ? AND assignee_user_id = ? AND status = ? ORDER BY created_at DESC LIMIT 100`, args: []any{"workspace-primary", "manager", "open"}, index: "idx_workflow_task_assignee"},
		{name: "process by domain record", query: `SELECT id FROM _workflow_process_instances WHERE workspace_id = ? AND object_key = ? AND record_id = ? ORDER BY created_at DESC LIMIT 100`, args: []any{"workspace-primary", "leave_request", "leave-1"}, index: "idx_workflow_process_business_record"},
		{name: "process nodes", query: `SELECT id FROM _workflow_node_instances WHERE workspace_id = ? AND process_id = ? ORDER BY node_id`, args: []any{"workspace-primary", "process-1"}, index: "idx_workflow_node_process"},
		{name: "process timeline", query: `SELECT id FROM _workflow_process_events WHERE workspace_id = ? AND process_id = ? ORDER BY created_at`, args: []any{"workspace-primary", "process-1"}, index: "idx_workflow_event_process"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			plan := sqliteExplainQueryPlan(t, store, testCase.query, testCase.args...)
			if !strings.Contains(plan, testCase.index) {
				t.Fatalf("expected %s in query plan:\n%s", testCase.index, plan)
			}
		})
	}
}

func TestTenantQueryBuilderRejectsMissingWorkspaceAndOwnsWorkspaceFilter(t *testing.T) {
	store := openStoreForGeneratedListTest(t)
	defer store.Close()
	if _, _, err := store.TenantListWhereClause("", recordmodel.RecordListQuery{}); err == nil {
		t.Fatal("tenant query builder must reject a missing workspace")
	}
	for _, injectedWorkspace := range []string{"workspace-b", "*"} {
		whereSQL, args, err := store.TenantListWhereClause("workspace-a", recordmodel.RecordListQuery{Filters: map[string]any{"workspace_id": injectedWorkspace, "status": "open"}})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Count(whereSQL, "workspace_id") != 1 || len(args) != 2 || args[0] != "workspace-a" || args[1] != "open" {
			t.Fatalf("tenant builder allowed workspace override %q: sql=%s args=%#v", injectedWorkspace, whereSQL, args)
		}
	}
}

func openStoreForGeneratedListTest(t *testing.T) *RuntimeStore {
	t.Helper()
	tempDir := t.TempDir()
	migrationPath := filepath.Join(tempDir, "001_empty.sql")
	if err := os.WriteFile(migrationPath, []byte("-- query plan test migration\n"), 0644); err != nil {
		t.Fatal(err)
	}
	store, err := OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(tempDir, "app.db"), MigrationSQL: migrationPath})
	if err != nil {
		t.Fatal(err)
	}
	return store
}

func sqliteExplainQueryPlan(t *testing.T, store *RuntimeStore, query string, args ...any) string {
	t.Helper()
	rows, err := store.DB().Query("EXPLAIN QUERY PLAN "+query, args...)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	parts := []string{}
	for rows.Next() {
		var id, parent, unused int
		var detail string
		if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		parts = append(parts, detail)
	}
	return strings.Join(parts, "\n")
}
