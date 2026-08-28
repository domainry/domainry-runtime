package database

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-runtime/runtime/platform/filelock"
)

func TestMigrationChecksumRejectsEditedAppliedFile(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "runtime.db")
	migrationPath := filepath.Join(dir, "001_create_sample.sql")
	writeMigrationFixture(t, migrationPath, `CREATE TABLE sample (id TEXT PRIMARY KEY);`)
	cfg := config.Config{DatabaseDriver: "sqlite", DBPath: dbPath, MigrationSQL: migrationPath, DatabaseMigrationMode: "apply", MigrationBackupDir: filepath.Join(dir, "backups")}
	store, err := OpenContext(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	writeMigrationFixture(t, migrationPath, `CREATE TABLE sample (id TEXT PRIMARY KEY, changed TEXT);`)
	if _, err := OpenContext(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), "migration.checksum_drift") {
		t.Fatalf("expected checksum mismatch, got %v", err)
	}
}

func TestTwoSQLiteInstancesExecuteMigrationOnce(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "runtime.db")
	migrationPath := filepath.Join(dir, "001_concurrent.sql")
	writeMigrationFixture(t, migrationPath, `CREATE TABLE concurrent_once (id TEXT PRIMARY KEY);`)
	cfg := config.Config{DatabaseDriver: "sqlite", DBPath: dbPath, MigrationSQL: migrationPath, DatabaseMigrationMode: "apply", DatabaseLockTimeout: 5 * time.Second}
	var wait sync.WaitGroup
	errors := make(chan error, 2)
	for range 2 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			store, err := OpenContext(t.Context(), cfg)
			if err == nil {
				err = store.Close()
			}
			errors <- err
		}()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		if err != nil {
			t.Fatal(err)
		}
	}
	store, err := OpenContext(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var count int
	if err := store.DB().QueryRow(`SELECT COUNT(*) FROM _schema_migrations WHERE path = ?`, filepath.Base(migrationPath)).Scan(&count); err != nil || count != 1 {
		t.Fatalf("ledger count=%d err=%v", count, err)
	}
}

func TestSQLiteMigrationLockTimeoutHasStableCode(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "runtime.db")
	lock, err := os.OpenFile(dbPath+".migration.lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := filelock.TryExclusive(lock); err != nil {
		t.Fatal(err)
	}
	defer filelock.Unlock(lock)
	_, err = OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: dbPath, DatabaseLockTimeout: 100 * time.Millisecond})
	if err == nil || !strings.Contains(err.Error(), "migration.lock_timeout") {
		t.Fatalf("lock timeout error=%v", err)
	}
}

func TestMigrationLedgerRecordsStableReleaseIdentity(t *testing.T) {
	dir := t.TempDir()
	migrationPath := filepath.Join(dir, "004_expand_customer.sql")
	writeMigrationFixture(t, migrationPath, `CREATE TABLE customer_release_identity (id TEXT PRIMARY KEY);`)
	cfg := config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(dir, "runtime.db"), MigrationSQL: migrationPath, DatabaseMigrationMode: "apply", RuntimeVersion: "2.4.0", MigrationOperator: "release-bot", MigrationInstanceID: "runtime-2"}
	store, err := OpenContext(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	var version, name, kind, checksum, runtimeVersion, operator, instance, backupID string
	var duration int64
	if err := store.DB().QueryRowContext(t.Context(), `SELECT version, name, kind, checksum, runtime_version, duration_ms, operator, instance_id, backup_id FROM _schema_migrations WHERE path = ?`, filepath.Base(migrationPath)).Scan(&version, &name, &kind, &checksum, &runtimeVersion, &duration, &operator, &instance, &backupID); err != nil {
		t.Fatal(err)
	}
	if version != "004" || name != "expand_customer" || kind != "schema" || len(checksum) != 64 || runtimeVersion != "2.4.0" || duration < 0 || operator != "release-bot" || instance != "runtime-2" || backupID == "" {
		t.Fatalf("ledger identity version=%q name=%q kind=%q checksum=%d runtime=%q duration=%d operator=%q instance=%q", version, name, kind, len(checksum), runtimeVersion, duration, operator, instance)
	}
}

func TestSchemaAndMetadataMigrationsShareOrderedLedger(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sqlite"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeMigrationFixture(t, filepath.Join(dir, "sqlite", "010_expand_customer.sql"), `CREATE TABLE ordered_customer (id TEXT PRIMARY KEY, label TEXT);`)
	writeMigrationFixture(t, filepath.Join(dir, "sqlite", "011_metadata_customer.sql"), `INSERT INTO ordered_customer (id, label) VALUES ('seed', 'metadata');`)
	store, err := OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(dir, "runtime.db"), MigrationDir: dir})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	rows, err := store.DB().Query(`SELECT version, kind FROM _schema_migrations ORDER BY path`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var versions, kinds []string
	for rows.Next() {
		var version, kind string
		if err := rows.Scan(&version, &kind); err != nil {
			t.Fatal(err)
		}
		versions, kinds = append(versions, version), append(kinds, kind)
	}
	if strings.Join(versions, ",") != "010,011" || strings.Join(kinds, ",") != "schema,metadata_data" {
		t.Fatalf("versions=%v kinds=%v", versions, kinds)
	}
}

func TestMigrationStatusReportsChecksumDrift(t *testing.T) {
	dir := t.TempDir()
	migrationPath := filepath.Join(dir, "001_status.sql")
	writeMigrationFixture(t, migrationPath, `CREATE TABLE migration_status_sample (id TEXT PRIMARY KEY);`)
	store, err := OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(dir, "runtime.db"), MigrationSQL: migrationPath, DatabaseMigrationMode: "apply"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.DB().ExecContext(t.Context(), `UPDATE _schema_migrations SET checksum = ? WHERE path = ?`, strings.Repeat("0", 64), filepath.Base(migrationPath)); err != nil {
		t.Fatal(err)
	}
	status, err := store.MigrationStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status.Current || status.Drift != 1 || len(status.DriftPaths) != 1 || status.DriftPaths[0] != filepath.Base(migrationPath) {
		t.Fatalf("migration drift not reported: %+v", status)
	}
}

func TestMigrationStatusAndVerifyRejectDirtyLedger(t *testing.T) {
	dir := t.TempDir()
	migrationPath := filepath.Join(dir, "001_dirty.sql")
	writeMigrationFixture(t, migrationPath, `CREATE TABLE dirty_sample (id TEXT PRIMARY KEY);`)
	cfg := config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(dir, "runtime.db"), MigrationSQL: migrationPath, DatabaseMigrationMode: "apply"}
	store, err := OpenContext(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `UPDATE _schema_migrations SET dirty = TRUE WHERE path = ?`, filepath.Base(migrationPath)); err != nil {
		t.Fatal(err)
	}
	status, err := store.MigrationStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status.Current || status.Dirty != 1 || len(status.DirtyPaths) != 1 {
		t.Fatalf("dirty migration not reported: %+v", status)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	cfg.DatabaseMigrationMode = "verify"
	if _, err := OpenContext(t.Context(), cfg); err == nil || !strings.Contains(err.Error(), "migration.dirty") {
		t.Fatalf("verify-only accepted dirty migration: %v", err)
	}
}

func TestMigrationStatusRejectsUnknownAndNewerAppliedVersions(t *testing.T) {
	dir := t.TempDir()
	migrationPath := filepath.Join(dir, "004_current.sql")
	writeMigrationFixture(t, migrationPath, `CREATE TABLE current_schema (id TEXT PRIMARY KEY);`)
	store, err := OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(dir, "runtime.db"), MigrationSQL: migrationPath})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, row := range []struct{ path, checksum string }{{"003_unpublished.sql", strings.Repeat("3", 64)}, {"005_future.sql", strings.Repeat("5", 64)}} {
		if _, err := store.DB().Exec(`INSERT INTO _schema_migrations (path,version,name,kind,checksum,dirty,applied_at,runtime_version,duration_ms,operator,instance_id,backup_id) VALUES (?,?,?,?,?,FALSE,?,?,?,?,?,?)`, row.path, strings.SplitN(row.path, "_", 2)[0], row.path, "schema", row.checksum, time.Now().UTC().Format(time.RFC3339), "future", 1, "test", "test", "backup"); err != nil {
			t.Fatal(err)
		}
	}
	status, err := store.MigrationStatus(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if status.Current || status.Unknown != 1 || status.Newer != 1 || status.ErrorCode != "migration.schema_newer" {
		t.Fatalf("status=%+v", status)
	}
}

func TestRuntimeSchemaCompatibilityRangeBlocksNewerRelease(t *testing.T) {
	dir := t.TempDir()
	migrationPath := filepath.Join(dir, "004_current.sql")
	writeMigrationFixture(t, migrationPath, `CREATE TABLE compatibility_range (id TEXT PRIMARY KEY);`)
	_, err := OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(dir, "runtime.db"), MigrationSQL: migrationPath, DatabaseMaxSchemaVersion: "003"})
	if err == nil || !strings.Contains(err.Error(), "migration.schema_newer") {
		t.Fatalf("compatibility range error=%v", err)
	}
}

func TestVerifyOnlyModeChecksFileAndRuntimeSchemaWithoutDDL(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "runtime.db")
	migrationPath := filepath.Join(dir, "001_verify.sql")
	writeMigrationFixture(t, migrationPath, `CREATE TABLE verify_sample (id TEXT PRIMARY KEY);`)
	applyConfig := config.Config{DatabaseDriver: "sqlite", DBPath: dbPath, MigrationSQL: migrationPath, DatabaseMigrationMode: "apply", MigrationBackupDir: filepath.Join(dir, "backups")}
	store, err := OpenContext(t.Context(), applyConfig)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	verifyConfig := applyConfig
	verifyConfig.DatabaseMigrationMode = "verify"
	verified, err := OpenContext(t.Context(), verifyConfig)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = verified.Close() })
	if err := verified.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatalf("verify-only runtime schema rejected current database: %v", err)
	}
}

func TestApplyModeUpgradesLegacyLedgerWithChecksum(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "runtime.db")
	migrationPath := filepath.Join(dir, "001_legacy.sql")
	writeMigrationFixture(t, migrationPath, `CREATE TABLE legacy_sample (id TEXT PRIMARY KEY);`)

	store, err := OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: dbPath})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `INSERT INTO _schema_migrations (path, checksum, applied_at) VALUES (?, ?, ?)`, filepath.Base(migrationPath), "", "2026-07-19T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: dbPath, MigrationSQL: migrationPath, DatabaseMigrationMode: "apply"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = upgraded.Close() })
	var checksum string
	if err := upgraded.DB().QueryRowContext(t.Context(), `SELECT checksum FROM _schema_migrations WHERE path = ?`, filepath.Base(migrationPath)).Scan(&checksum); err != nil {
		t.Fatal(err)
	}
	if len(checksum) != 64 {
		t.Fatalf("legacy migration checksum length = %d, want 64", len(checksum))
	}
}

func writeMigrationFixture(t *testing.T, path, sql string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(sql), 0o600); err != nil {
		t.Fatal(err)
	}
}
