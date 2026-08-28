package database

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/postgres"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestApplyMigrationFileSQLFailures(t *testing.T) {
	path := writeMigrationEdgeFile(t, "010_failure.sql", "CREATE TABLE failure_probe (id TEXT)")
	tests := []struct {
		name    string
		state   databaseSQLState
		prepare func(*RuntimeStore)
		match   string
	}{
		{"dirty receipt", databaseSQLState{execSteps: []databaseSQLExecStep{{err: errDatabaseSQL}}}, nil, "record dirty"},
		{"begin", databaseSQLState{execSteps: []databaseSQLExecStep{{rows: 1}}, beginErr: errDatabaseSQL}, nil, "begin transaction"},
		{"statement", databaseSQLState{execSteps: []databaseSQLExecStep{{rows: 1}, {err: errDatabaseSQL}}}, nil, "migration.failed"},
		{"complete", databaseSQLState{execSteps: []databaseSQLExecStep{{rows: 1}, {rows: 1}, {err: errDatabaseSQL}}}, nil, "record migration"},
		{"commit", databaseSQLState{execSteps: []databaseSQLExecStep{{rows: 1}, {rows: 1}, {rows: 1}}, commitErr: errDatabaseSQL}, nil, "commit transaction"},
		{"postgres search path", databaseSQLState{execSteps: []databaseSQLExecStep{{rows: 1}, {err: errDatabaseSQL}}}, func(store *RuntimeStore) { store.dialect = postgres.Dialect{} }, "search path"},
		{"postgres lock timeout", databaseSQLState{execSteps: []databaseSQLExecStep{{rows: 1}, {rows: 1}, {err: errDatabaseSQL}}}, func(store *RuntimeStore) {
			store.dialect = postgres.Dialect{}
			store.config.DatabaseLockTimeout = time.Second
		}, "lock timeout"},
		{"postgres statement timeout", databaseSQLState{execSteps: []databaseSQLExecStep{{rows: 1}, {rows: 1}, {err: errDatabaseSQL}}}, func(store *RuntimeStore) {
			store.dialect = postgres.Dialect{}
			store.config.DatabaseStatementTimeout = time.Second
		}, "statement timeout"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store := runtimeSchemaStore(t, &test.state)
			if test.prepare != nil {
				test.prepare(store)
			}
			if err := store.applyMigrationFile(t.Context(), path); err == nil || !strings.Contains(err.Error(), test.match) {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestApplyMigrationFilePostgresTimeoutSuccess(t *testing.T) {
	path := writeMigrationEdgeFile(t, "011_postgres_success.sql", "CREATE TABLE postgres_success (id TEXT)")
	for _, cfg := range []config.Config{
		{DatabaseLockTimeout: time.Second},
		{DatabaseStatementTimeout: time.Second},
	} {
		store := runtimeSchemaStore(t, &databaseSQLState{})
		store.dialect = postgres.Dialect{}
		store.config = cfg
		if err := store.applyMigrationFile(t.Context(), path); err != nil {
			t.Fatalf("config=%+v error=%v", cfg, err)
		}
	}
}

func TestEnsureMigrationLedgerSQLFailures(t *testing.T) {
	store := runtimeSchemaStore(t, &databaseSQLState{execSteps: []databaseSQLExecStep{{err: errDatabaseSQL}}})
	store.dialect, store.databaseSchema = postgres.Dialect{}, "runtime"
	if err := store.ensureMigrationLedger(t.Context()); !errors.Is(err, errDatabaseSQL) {
		t.Fatalf("schema error=%v", err)
	}
	store = runtimeSchemaStore(t, &databaseSQLState{execSteps: []databaseSQLExecStep{{err: errDatabaseSQL}}})
	if err := store.ensureMigrationLedger(t.Context()); !errors.Is(err, errDatabaseSQL) {
		t.Fatalf("table error=%v", err)
	}
	store = runtimeSchemaStore(t, &databaseSQLState{
		execSteps:  []databaseSQLExecStep{{rows: 1}, {err: errDatabaseSQL}},
		querySteps: []databaseSQLQueryStep{{err: errDatabaseSQL}},
	})
	if err := store.ensureMigrationLedger(t.Context()); !errors.Is(err, errDatabaseSQL) {
		t.Fatalf("alter error=%v", err)
	}
}

func TestEnsureMigrationLedgerPostgresConditionOutcomes(t *testing.T) {
	for _, schema := range []string{"", "public", "runtime"} {
		store := runtimeSchemaStore(t, &databaseSQLState{querySteps: make([]databaseSQLQueryStep, 10)})
		store.dialect, store.databaseSchema = postgres.Dialect{}, schema
		if err := store.ensureMigrationLedger(t.Context()); err != nil {
			t.Fatalf("schema=%q error=%v", schema, err)
		}
	}
	store := runtimeSchemaStore(t, &databaseSQLState{querySteps: append([]databaseSQLQueryStep{{err: errDatabaseSQL}}, make([]databaseSQLQueryStep, 9)...)})
	if err := store.ensureMigrationLedger(t.Context()); err != nil {
		t.Fatalf("alter success=%v", err)
	}
}

func TestMigrationTopLevelPathAndStatusFailures(t *testing.T) {
	store := runtimeSchemaStore(t, &databaseSQLState{})
	blocked := writeMigrationEdgeFile(t, "not-a-directory", "x")
	if err := store.applyMigrations(t.Context(), config.Config{MigrationDir: blocked}); err == nil || !strings.Contains(err.Error(), "list migrations") {
		t.Fatalf("apply path error=%v", err)
	}
	store = runtimeSchemaStore(t, &databaseSQLState{})
	if err := store.verifyMigrations(t.Context(), config.Config{MigrationDir: blocked}); err == nil || !strings.Contains(err.Error(), "list migrations") {
		t.Fatalf("verify path error=%v", err)
	}
}
