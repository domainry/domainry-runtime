package database

import (
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/mysql"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/postgres"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestMigrationStatusReadFailures(t *testing.T) {
	for _, step := range []databaseSQLQueryStep{
		{err: errDatabaseSQL},
		{columns: []string{"bad"}, rows: [][]driver.Value{{"bad"}}},
		{columns: []string{"path", "checksum", "dirty", "at"}, nextErr: errDatabaseSQL},
	} {
		store := runtimeSchemaStore(t, &databaseSQLState{querySteps: []databaseSQLQueryStep{step}})
		if status, err := store.MigrationStatus(t.Context()); err == nil || status.Current {
			t.Fatalf("status=%#v err=%v", status, err)
		}
	}
}

func TestMigrationStatusClassificationsAndVersionBounds(t *testing.T) {
	tests := []struct {
		name      string
		expected  []string
		checksums map[string]string
		rows      [][]driver.Value
		cfg       config.Config
		state     string
	}{
		{"current", []string{"001_base.sql"}, map[string]string{"001_base.sql": "sum"}, [][]driver.Value{{"001_base.sql", "sum", false, "2026-01-01"}}, config.Config{}, "current"},
		{"dirty", []string{"001_base.sql"}, map[string]string{"001_base.sql": "sum"}, [][]driver.Value{{"001_base.sql", "sum", true, "2026-01-01"}}, config.Config{}, "dirty"},
		{"drift", []string{"001_base.sql"}, map[string]string{"001_base.sql": "sum"}, [][]driver.Value{{"001_base.sql", "other", false, "2026-01-01"}}, config.Config{}, "drift"},
		{"newer", []string{"001_base.sql"}, map[string]string{"001_base.sql": "sum"}, [][]driver.Value{{"002_future.sql", "sum", false, "2026-01-01"}}, config.Config{}, "newer"},
		{"unknown", nil, nil, [][]driver.Value{{"001_unknown.sql", "sum", false, "2026-01-01"}}, config.Config{}, "unknown"},
		{"pending", []string{"001_base.sql"}, map[string]string{"001_base.sql": "sum"}, nil, config.Config{}, "pending"},
		{"minimum", []string{"001_base.sql"}, map[string]string{"001_base.sql": "sum"}, [][]driver.Value{{"001_base.sql", "sum", false, "2026-01-01"}}, config.Config{DatabaseMinSchemaVersion: "002", DatabaseMaxSchemaVersion: "003"}, "pending"},
		{"maximum", []string{"001_base.sql"}, map[string]string{"001_base.sql": "sum"}, [][]driver.Value{{"003_future.sql", "sum", false, "2026-01-01"}}, config.Config{DatabaseMinSchemaVersion: "001", DatabaseMaxSchemaVersion: "002"}, "newer"},
		{"descending tracked", []string{"001_base.sql", "002_next.sql"}, map[string]string{"001_base.sql": "one", "002_next.sql": "two"}, [][]driver.Value{{"002_next.sql", "two", false, "2026-01-02"}, {"001_base.sql", "one", false, "2026-01-01"}}, config.Config{}, "current"},
		{"tracked without bounds", nil, map[string]string{"001_base.sql": "sum"}, [][]driver.Value{{"001_base.sql", "sum", false, "2026-01-01"}}, config.Config{}, "current"},
		{"minimum with existing pending", []string{"002_expected.sql"}, map[string]string{"001_applied.sql": "sum", "002_expected.sql": "expected"}, [][]driver.Value{{"001_applied.sql", "sum", false, "2026-01-01"}}, config.Config{}, "pending"},
		{"maximum already newer", nil, map[string]string{"003_current.sql": "sum"}, [][]driver.Value{{"003_current.sql", "sum", false, "2026-01-01"}, {"004_newer.sql", "sum", false, "2026-01-02"}}, config.Config{DatabaseMaxSchemaVersion: "002"}, "newer"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := runtimeSchemaStore(t, &databaseSQLState{querySteps: []databaseSQLQueryStep{{columns: []string{"path", "checksum", "dirty", "at"}, rows: test.rows}}})
			store.expectedMigrations, store.expectedChecksums, store.config = test.expected, test.checksums, test.cfg
			status, err := store.MigrationStatus(t.Context())
			if err != nil || !strings.Contains(string(status.State), test.state) {
				t.Fatalf("status=%#v err=%v", status, err)
			}
		})
	}
}

func TestWorkspaceScopeInventoryAndValidationFailures(t *testing.T) {
	for _, dialect := range []dialect{sqlite.Dialect{}, mysql.Dialect{}, postgres.Dialect{}} {
		store := runtimeSchemaStore(t, &databaseSQLState{})
		store.dialect = dialect
		if tables, err := store.inventoryWorkspaceTables(t.Context(), store.db); err != nil || len(tables) != 0 {
			t.Fatalf("dialect=%s tables=%#v err=%v", dialect.Name(), tables, err)
		}
	}
	for _, step := range []databaseSQLQueryStep{
		{err: errDatabaseSQL},
		{columns: []string{"table"}, rows: [][]driver.Value{{nil}}},
		{columns: []string{"table"}, nextErr: errDatabaseSQL},
	} {
		store := runtimeSchemaStore(t, &databaseSQLState{querySteps: []databaseSQLQueryStep{step}})
		if _, err := store.inventoryWorkspaceTables(t.Context(), store.db); err == nil {
			t.Fatal("expected inventory error")
		}
	}
	store := runtimeSchemaStore(t, &databaseSQLState{querySteps: []databaseSQLQueryStep{
		{columns: []string{"table"}, rows: [][]driver.Value{{"records"}}},
		{err: errDatabaseSQL},
	}})
	if err := store.ValidateLegacyWorkspaceScopes(t.Context()); !errors.Is(err, errDatabaseSQL) {
		t.Fatalf("query error=%v", err)
	}
	store = runtimeSchemaStore(t, &databaseSQLState{querySteps: []databaseSQLQueryStep{
		{columns: []string{"table"}, rows: [][]driver.Value{{"records"}}},
		{columns: []string{"workspace", "count"}, rows: [][]driver.Value{{nil, int64(1)}}},
	}})
	if err := store.ValidateLegacyWorkspaceScopes(t.Context()); err == nil {
		t.Fatal("scan error was ignored")
	}
	store = runtimeSchemaStore(t, &databaseSQLState{querySteps: []databaseSQLQueryStep{
		{columns: []string{"table"}, rows: [][]driver.Value{{"records"}}},
		{columns: []string{"workspace", "count"}, nextErr: errDatabaseSQL},
	}})
	if err := store.ValidateLegacyWorkspaceScopes(t.Context()); !errors.Is(err, errDatabaseSQL) {
		t.Fatalf("close error=%v", err)
	}
	store = runtimeSchemaStore(t, &databaseSQLState{querySteps: []databaseSQLQueryStep{
		{columns: []string{"table"}, rows: [][]driver.Value{{"records"}}},
		{columns: []string{"workspace", "count"}, rows: [][]driver.Value{{"", int64(2)}}},
	}})
	if err := store.ValidateLegacyWorkspaceScopes(t.Context()); err == nil || !strings.Contains(err.Error(), "row_count=2") {
		t.Fatalf("finding error=%v", err)
	}
}

func TestInsertSystemRowFailure(t *testing.T) {
	store := runtimeSchemaStore(t, &databaseSQLState{execSteps: []databaseSQLExecStep{{err: errDatabaseSQL}}})
	if err := store.insertSystemRowContext(t.Context(), "records", []string{"id"}, []any{"id"}); !errors.Is(err, errDatabaseSQL) {
		t.Fatalf("error=%v", err)
	}
}
