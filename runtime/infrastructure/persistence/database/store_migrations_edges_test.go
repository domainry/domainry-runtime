package database

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/mysql"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func openMigrationEdgeStore(t *testing.T) *RuntimeStore {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+strings.ReplaceAll(t.Name(), "/", "-")+"?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = db.Close() })
	store := &RuntimeStore{db: db, engine: sqlite.NewEngine()}
	if err := store.ensureMigrationLedger(t.Context()); err != nil {
		t.Fatal(err)
	}
	return store
}

func writeMigrationEdgeFile(t *testing.T, name, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func insertMigrationEdgeLedger(t *testing.T, store *RuntimeStore, path, checksum string, dirty bool) {
	t.Helper()
	if _, err := store.db.ExecContext(t.Context(), `DELETE FROM _schema_migrations`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.ExecContext(t.Context(), `INSERT INTO _schema_migrations(path, checksum, dirty, applied_at) VALUES (?, ?, ?, ?)`, filepath.Base(path), checksum, dirty, time.Now().UTC().Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationVerificationAndPendingStateEdges(t *testing.T) {
	store := openMigrationEdgeStore(t)
	path := writeMigrationEdgeFile(t, "001_edge.sql", "CREATE TABLE edge_record (id TEXT PRIMARY KEY);")
	checksum, err := migrationChecksum(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.verifyMigration(t.Context(), path); err == nil || !strings.Contains(err.Error(), "migration.pending") {
		t.Fatalf("pending verification=%v", err)
	}
	if pending, err := store.migrationPending(t.Context(), path); err != nil || !pending {
		t.Fatalf("pending=%v error=%v", pending, err)
	}

	insertMigrationEdgeLedger(t, store, path, checksum, true)
	if err := store.verifyMigration(t.Context(), path); err == nil || !strings.Contains(err.Error(), "migration.dirty") {
		t.Fatalf("dirty verification=%v", err)
	}
	if _, err := store.migrationPending(t.Context(), path); err == nil || !strings.Contains(err.Error(), "migration.dirty") {
		t.Fatalf("dirty pending error=%v", err)
	}

	insertMigrationEdgeLedger(t, store, path, "", false)
	if err := store.verifyMigration(t.Context(), path); err == nil || !strings.Contains(err.Error(), "checksum missing") {
		t.Fatalf("missing checksum verification=%v", err)
	}
	if pending, err := store.migrationPending(t.Context(), path); err != nil || pending {
		t.Fatalf("checksum backfill pending=%v error=%v", pending, err)
	}
	var backfilled string
	if err := store.db.QueryRowContext(t.Context(), `SELECT checksum FROM _schema_migrations WHERE path = ?`, filepath.Base(path)).Scan(&backfilled); err != nil || backfilled != checksum {
		t.Fatalf("backfilled=%q error=%v", backfilled, err)
	}

	insertMigrationEdgeLedger(t, store, path, "drift", false)
	if err := store.verifyMigration(t.Context(), path); err == nil || !strings.Contains(err.Error(), "checksum_drift") {
		t.Fatalf("drift verification=%v", err)
	}
	if _, err := store.migrationPending(t.Context(), path); err == nil || !strings.Contains(err.Error(), "checksum_drift") {
		t.Fatalf("drift pending=%v", err)
	}

	insertMigrationEdgeLedger(t, store, path, checksum, false)
	if err := store.verifyMigration(t.Context(), path); err != nil {
		t.Fatalf("verified migration: %v", err)
	}
	if pending, err := store.migrationPending(t.Context(), path); err != nil || pending {
		t.Fatalf("applied pending=%v error=%v", pending, err)
	}
	if err := store.applyMigrationFile(t.Context(), path); err != nil {
		t.Fatalf("already applied migration: %v", err)
	}

	missing := filepath.Join(t.TempDir(), "missing.sql")
	if err := store.verifyMigration(t.Context(), missing); err == nil || !strings.Contains(err.Error(), "read migration checksum") {
		t.Fatalf("missing checksum error=%v", err)
	}
	if _, err := store.migrationPending(t.Context(), missing); err == nil || !strings.Contains(err.Error(), "read migration checksum") {
		t.Fatalf("missing pending checksum error=%v", err)
	}
}

func TestMigrationApplicationLedgerAndContextEdges(t *testing.T) {
	store := openMigrationEdgeStore(t)
	path := writeMigrationEdgeFile(t, "002_apply.sql", "-- comment\nCREATE TABLE applied_edge (id TEXT);\n\nINSERT INTO applied_edge(id) VALUES ('one');")
	if err := store.applyMigrationFile(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	if err := store.applyMigrationFile(t.Context(), path); err != nil {
		t.Fatalf("replay migration: %v", err)
	}
	var count int
	if err := store.db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM applied_edge`).Scan(&count); err != nil || count != 1 {
		t.Fatalf("applied rows=%d error=%v", count, err)
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := store.verifyMigration(cancelled, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled verify=%v", err)
	}
	if _, err := store.migrationPending(cancelled, path); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled pending=%v", err)
	}

	closedDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	closedStore := &RuntimeStore{db: closedDB, engine: sqlite.NewEngine()}
	_ = closedDB.Close()
	if err := closedStore.ensureMigrationLedger(t.Context()); err == nil {
		t.Fatal("closed database prepared a ledger")
	}
}

func TestMigrationPathIdentityAndSQLHelperEdges(t *testing.T) {
	root := t.TempDir()
	driverDir := filepath.Join(root, "sqlite")
	if err := os.MkdirAll(driverDir, 0o755); err != nil {
		t.Fatal(err)
	}
	paths := []string{
		writeNamedMigrationEdgeFile(t, driverDir, "010_second.sql", "SELECT 2;"),
		writeNamedMigrationEdgeFile(t, driverDir, "001_first.sql", "SELECT 1;"),
	}
	if err := os.WriteFile(filepath.Join(driverDir, "ignored.txt"), []byte("ignored"), 0o600); err != nil {
		t.Fatal(err)
	}
	store := &RuntimeStore{engine: sqlite.NewEngine()}
	got, err := store.migrationPaths(config.Config{MigrationDir: root})
	if err != nil || !reflect.DeepEqual(got, []string{paths[1], paths[0]}) {
		t.Fatalf("driver paths=%v error=%v", got, err)
	}
	explicit := writeMigrationEdgeFile(t, "explicit.sql", "SELECT 1;")
	if got, err := store.migrationPaths(config.Config{MigrationSQL: " " + explicit + " "}); err != nil || !reflect.DeepEqual(got, []string{" " + explicit + " "}) {
		t.Fatalf("explicit paths=%v error=%v", got, err)
	}
	if err := store.setExpectedMigrations(paths); err != nil || len(store.expectedChecksums) != 2 {
		t.Fatalf("expected migrations=%v checksums=%v error=%v", store.expectedMigrations, store.expectedChecksums, err)
	}
	if got := migrationNames([]string{paths[0], paths[1]}); !reflect.DeepEqual(got, []string{"001_first.sql", "010_second.sql"}) {
		t.Fatalf("migration names=%v", got)
	}
	if version, name := migrationIdentity("003_data_seed.sql"); version != "003" || name != "data_seed" {
		t.Fatalf("identity=%q/%q", version, name)
	}
	for name, want := range map[string]string{"data_seed": "metadata_data", " metadata_seed ": "metadata_data", "manifest_seed": "metadata_data", "schema": "schema"} {
		if got := migrationKind(name); got != want {
			t.Fatalf("migration kind(%q)=%q", name, got)
		}
	}
	if migrationOperator(config.Config{MigrationOperator: " operator "}) != "operator" || migrationOperator(config.Config{}) != "runtime" {
		t.Fatal("migration operator normalization changed")
	}
	if migrationInstanceID(config.Config{MigrationInstanceID: " instance "}) != "instance" || migrationInstanceID(config.Config{}) == "" {
		t.Fatal("migration instance normalization changed")
	}
	statements := splitSQLStatements("-- comment\n SELECT 1;\n; SELECT 2 ;")
	if !reflect.DeepEqual(statements, []string{"SELECT 1", "SELECT 2"}) {
		t.Fatalf("statements=%v", statements)
	}
	if got, err := sqlPaths(root, nil); err != nil || got != nil {
		t.Fatalf("empty sql paths=%v error=%v", got, err)
	}
	rootOnly := t.TempDir()
	rootPath := writeNamedMigrationEdgeFile(t, rootOnly, "007_root.sql", "SELECT 7;")
	if got, err := store.migrationPaths(config.Config{MigrationDir: rootOnly}); err != nil || !reflect.DeepEqual(got, []string{rootPath}) {
		t.Fatalf("root paths=%v error=%v", got, err)
	}
	rootFailure := &RuntimeStore{engine: sqlite.NewEngine(), migrationReadDir: func(path string) ([]os.DirEntry, error) {
		if strings.HasSuffix(path, "sqlite") {
			return nil, os.ErrNotExist
		}
		return nil, errDatabaseSQL
	}}
	if _, err := rootFailure.migrationPaths(config.Config{MigrationDir: "migrations"}); !errors.Is(err, errDatabaseSQL) {
		t.Fatalf("root directory error=%v", err)
	}
}

func writeNamedMigrationEdgeFile(t *testing.T, directory, name, contents string) string {
	t.Helper()
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSQLiteMigrationLockShortCircuitSuccessAndTimeout(t *testing.T) {
	profile := sqlite.NewEngine()
	renderer := sqlite.NewEngine().SQLDialect().WithSchema("")
	for _, path := range []string{"", ":memory:", "file:memory"} {
		lock, err := profile.AcquireMigrationLock(t.Context(), nil, renderer, persistencedriver.MigrationLockOptions{DatabasePath: path})
		if err != nil {
			t.Fatalf("short-circuit path %q: %v", path, err)
		}
		lock.Release()
	}
	databasePath := filepath.Join(t.TempDir(), "runtime.db")
	lock, err := profile.AcquireMigrationLock(t.Context(), nil, renderer, persistencedriver.MigrationLockOptions{DatabasePath: databasePath, Owner: "first", LockTimeout: 20 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := profile.AcquireMigrationLock(t.Context(), nil, renderer, persistencedriver.MigrationLockOptions{DatabasePath: databasePath, Owner: "second", LockTimeout: 20 * time.Millisecond}); err == nil || !strings.Contains(err.Error(), "migration.lock_timeout") {
		t.Fatalf("contended lock error=%v", err)
	}
	lock.Release()
	if _, err := profile.AcquireMigrationLock(t.Context(), nil, renderer, persistencedriver.MigrationLockOptions{DatabasePath: filepath.Join("/dev/null", "runtime.db")}); err == nil {
		t.Fatal("invalid lock directory accepted")
	}
	openFailurePath := filepath.Join(t.TempDir(), "runtime.db")
	if err := os.Mkdir(openFailurePath+".migration.lock", 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := profile.AcquireMigrationLock(t.Context(), nil, renderer, persistencedriver.MigrationLockOptions{DatabasePath: openFailurePath}); err == nil || !strings.Contains(err.Error(), "open sqlite migration lock") {
		t.Fatalf("open lock error=%v", err)
	}
}

func TestMigrationScriptedSQLFailureEdges(t *testing.T) {
	path := writeMigrationEdgeFile(t, "008_scripted.sql", "SELECT 8;")
	store := runtimeSchemaStore(t, &databaseSQLState{
		querySteps: []databaseSQLQueryStep{{columns: []string{"checksum", "dirty"}, rows: [][]driver.Value{{"", false}}}},
		execSteps:  []databaseSQLExecStep{{err: errDatabaseSQL}},
	})
	if _, err := store.migrationPending(t.Context(), path); !errors.Is(err, errDatabaseSQL) {
		t.Fatalf("backfill error=%v", err)
	}
	queryFailure := runtimeSchemaStore(t, &databaseSQLState{querySteps: []databaseSQLQueryStep{{err: errDatabaseSQL}}})
	if err := queryFailure.applyMigrationFile(t.Context(), path); !errors.Is(err, errDatabaseSQL) {
		t.Fatalf("pending query error=%v", err)
	}

	removed := writeMigrationEdgeFile(t, "009_removed.sql", "SELECT 9;")
	state := &databaseSQLState{queryHook: func() { _ = os.Remove(removed) }}
	store = runtimeSchemaStore(t, state)
	if err := store.applyMigrationFile(t.Context(), removed); err == nil || !strings.Contains(err.Error(), "read:") {
		t.Fatalf("removed migration error=%v", err)
	}

	mysqlStore := runtimeSchemaStore(t, &databaseSQLState{querySteps: make([]databaseSQLQueryStep, 10)})
	mysqlStore.engine = mysql.NewEngine()
	if err := mysqlStore.ensureMigrationLedger(t.Context()); err != nil {
		t.Fatalf("mysql ledger error=%v", err)
	}
}

func TestMigrationBackupDiscoveryAndFailureEdges(t *testing.T) {
	store := openMigrationEdgeStore(t)
	if _, err := store.db.ExecContext(t.Context(), `CREATE TABLE _schema_materializations(id TEXT); CREATE TABLE customer(id TEXT); INSERT INTO customer(id) VALUES ('one')`); err != nil {
		t.Fatal(err)
	}
	tables, err := store.applicationTables(t.Context())
	if err != nil || !reflect.DeepEqual(tables, []string{"customer"}) {
		t.Fatalf("tables=%v error=%v", tables, err)
	}
	if hasData, err := store.hasExistingApplicationData(t.Context()); err != nil || !hasData {
		t.Fatalf("hasData=%v error=%v", hasData, err)
	}
	if !isMigrationSystemTable("") || !isMigrationSystemTable(" _schema_migrations ") || isMigrationSystemTable("customer") {
		t.Fatal("migration system table classification changed")
	}

	closedDB, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	closedStore := &RuntimeStore{db: closedDB, engine: sqlite.NewEngine()}
	_ = closedDB.Close()
	if _, err := closedStore.applicationTables(t.Context()); err == nil {
		t.Fatal("closed database listed tables")
	}
	if _, err := closedStore.hasExistingApplicationData(t.Context()); err == nil {
		t.Fatal("closed database checked data")
	}

	root := t.TempDir()
	databasePath := filepath.Join(root, "runtime.db")
	if err := os.WriteFile(databasePath, []byte("database"), 0o600); err != nil {
		t.Fatal(err)
	}
	blockedBackupDir := filepath.Join(root, "blocked")
	if err := os.WriteFile(blockedBackupDir, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := store.createSQLiteMigrationBackup(t.Context(), config.Config{DatabaseDSN: databasePath, MigrationBackupDir: blockedBackupDir}); err == nil || !strings.Contains(err.Error(), "create migration backup directory") {
		t.Fatalf("blocked backup error=%v", err)
	}
	store.migrationBackupReady = false
	if err := store.ensureMigrationBackupForExistingData(t.Context(), config.Config{DBPath: ":memory:"}); err == nil {
		t.Fatal("existing in-memory database backup accepted")
	}
}

func TestApplyMigrationsOrchestrationErrorEdges(t *testing.T) {
	t.Run("lock", func(t *testing.T) {
		store := openMigrationEdgeStore(t)
		err := store.applyMigrations(t.Context(), config.Config{DBPath: filepath.Join("/dev/null", "runtime.db")})
		if err == nil {
			t.Fatal("invalid lock directory accepted")
		}
	})
	t.Run("ledger", func(t *testing.T) {
		db, err := sql.Open("sqlite", ":memory:")
		if err != nil {
			t.Fatal(err)
		}
		store := &RuntimeStore{db: db, engine: sqlite.NewEngine()}
		_ = db.Close()
		if err := store.applyMigrations(t.Context(), config.Config{DBPath: ":memory:"}); err == nil || !strings.Contains(err.Error(), "prepare schema migration table") {
			t.Fatalf("closed ledger error=%v", err)
		}
	})
	t.Run("migration directory", func(t *testing.T) {
		store := openMigrationEdgeStore(t)
		blocked := writeMigrationEdgeFile(t, "not-a-directory", "blocked")
		if err := store.applyMigrations(t.Context(), config.Config{DBPath: ":memory:", MigrationDir: blocked}); err == nil || !strings.Contains(err.Error(), "list migrations") {
			t.Fatalf("directory error=%v", err)
		}
	})
	t.Run("expected checksum", func(t *testing.T) {
		store := openMigrationEdgeStore(t)
		missing := filepath.Join(t.TempDir(), "missing.sql")
		if err := store.applyMigrations(t.Context(), config.Config{DBPath: ":memory:", MigrationSQL: missing}); err == nil || !strings.Contains(err.Error(), "read migration checksum") {
			t.Fatalf("expected checksum error=%v", err)
		}
	})
	t.Run("status query", func(t *testing.T) {
		queries := append(make([]databaseSQLQueryStep, 10), databaseSQLQueryStep{err: errDatabaseSQL})
		store := runtimeSchemaStore(t, &databaseSQLState{querySteps: queries})
		if err := store.applyMigrations(t.Context(), config.Config{DBPath: ":memory:", MigrationDir: filepath.Join(t.TempDir(), "missing")}); !errors.Is(err, errDatabaseSQL) {
			t.Fatalf("status query error=%v", err)
		}
	})
	t.Run("backup", func(t *testing.T) {
		store := openMigrationEdgeStore(t)
		if _, err := store.db.ExecContext(t.Context(), `CREATE TABLE customer_edge(id TEXT); INSERT INTO customer_edge(id) VALUES ('one')`); err != nil {
			t.Fatal(err)
		}
		path := writeMigrationEdgeFile(t, "003_backup.sql", "CREATE TABLE after_backup_edge(id TEXT);")
		if err := store.applyMigrations(t.Context(), config.Config{DBPath: ":memory:", MigrationSQL: path}); err == nil || !strings.Contains(err.Error(), "not a copyable file path") {
			t.Fatalf("backup error=%v", err)
		}
	})
	t.Run("statement", func(t *testing.T) {
		store := openMigrationEdgeStore(t)
		path := writeMigrationEdgeFile(t, "004_invalid.sql", "CREATE TABLE")
		if err := store.applyMigrations(t.Context(), config.Config{DBPath: ":memory:", MigrationSQL: path}); err == nil || !strings.Contains(err.Error(), "migration.failed") {
			t.Fatalf("statement error=%v", err)
		}
	})
	t.Run("non-current status", func(t *testing.T) {
		store := openMigrationEdgeStore(t)
		if _, err := store.db.ExecContext(t.Context(), `INSERT INTO _schema_migrations(path, checksum, dirty, applied_at) VALUES ('999_unknown.sql', 'unknown', FALSE, 'now')`); err != nil {
			t.Fatal(err)
		}
		path := writeMigrationEdgeFile(t, "005_current.sql", "CREATE TABLE current_edge(id TEXT);")
		err := store.applyMigrations(t.Context(), config.Config{DBPath: ":memory:", MigrationSQL: path})
		if err == nil || !strings.Contains(err.Error(), "migration.schema_newer") {
			t.Fatalf("status error=%v", err)
		}
	})
}

func TestVerifyMigrationsOrchestrationEdges(t *testing.T) {
	store := openMigrationEdgeStore(t)
	blocked := writeMigrationEdgeFile(t, "blocked-directory", "blocked")
	if err := store.verifyMigrations(t.Context(), config.Config{MigrationDir: blocked}); err == nil || !strings.Contains(err.Error(), "list migrations") {
		t.Fatalf("directory error=%v", err)
	}
	missing := filepath.Join(t.TempDir(), "missing.sql")
	if err := store.verifyMigrations(t.Context(), config.Config{MigrationSQL: missing}); err == nil || !strings.Contains(err.Error(), "read migration checksum") {
		t.Fatalf("checksum error=%v", err)
	}
	path := writeMigrationEdgeFile(t, "006_verify.sql", "CREATE TABLE verify_edge(id TEXT);")
	if err := store.verifyMigrations(t.Context(), config.Config{MigrationSQL: path}); err == nil || !strings.Contains(err.Error(), "migration.pending") {
		t.Fatalf("pending error=%v", err)
	}
	if err := store.applyMigrationFile(t.Context(), path); err != nil {
		t.Fatal(err)
	}
	if err := store.verifyMigrations(t.Context(), config.Config{MigrationSQL: path}); err != nil {
		t.Fatalf("current verification=%v", err)
	}
	if _, err := store.db.ExecContext(t.Context(), `INSERT INTO _schema_migrations(path, checksum, dirty, applied_at) VALUES ('999_unknown.sql', 'unknown', FALSE, 'now')`); err != nil {
		t.Fatal(err)
	}
	if err := store.verifyMigrations(t.Context(), config.Config{MigrationSQL: path}); err == nil || !strings.Contains(err.Error(), "migration.schema_newer") {
		t.Fatalf("non-current verification=%v", err)
	}
	statusFailure := runtimeSchemaStore(t, &databaseSQLState{querySteps: []databaseSQLQueryStep{{err: errDatabaseSQL}}})
	if err := statusFailure.verifyMigrations(t.Context(), config.Config{MigrationDir: filepath.Join(t.TempDir(), "missing")}); !errors.Is(err, errDatabaseSQL) {
		t.Fatalf("status query error=%v", err)
	}
}
