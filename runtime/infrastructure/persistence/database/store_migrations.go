package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/filelock"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func (s *RuntimeStore) applyMigrations(ctx context.Context, cfg config.Config) error {
	release, err := s.acquireMigrationLock(ctx, cfg)
	if err != nil {
		return err
	}
	defer release()
	if err := s.ensureMigrationLedger(ctx); err != nil {
		return fmt.Errorf("prepare schema migration table: %w", err)
	}
	paths, err := s.migrationPaths(cfg)
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	if err := s.setExpectedMigrations(paths); err != nil {
		return err
	}
	backupChecked := false
	for _, path := range paths {
		pending, err := s.migrationPending(ctx, path)
		if err != nil {
			return fmt.Errorf("check migration %s: %w", path, err)
		}
		if pending && !backupChecked {
			if err := s.ensureMigrationBackupForExistingData(ctx, cfg); err != nil {
				return err
			}
			backupChecked = true
		}
		if err := s.applyMigrationFile(ctx, path); err != nil {
			return fmt.Errorf("apply migration %s: %w", path, err)
		}
	}
	status, err := s.MigrationStatus(ctx)
	if err != nil {
		return err
	}
	if !status.Current {
		return fmt.Errorf("%s: database schema is not compatible with runtime %s", status.ErrorCode, strings.TrimSpace(s.config.RuntimeVersion))
	}
	return nil
}

func (s *RuntimeStore) verifyMigrations(ctx context.Context, cfg config.Config) error {
	paths, err := s.migrationPaths(cfg)
	if err != nil {
		return fmt.Errorf("list migrations: %w", err)
	}
	if err := s.setExpectedMigrations(paths); err != nil {
		return err
	}
	for _, path := range paths {
		if err := s.verifyMigration(ctx, path); err != nil {
			return fmt.Errorf("verify migration %s: %w", filepath.Base(path), err)
		}
	}
	status, err := s.MigrationStatus(ctx)
	if err != nil {
		return err
	}
	if !status.Current {
		return fmt.Errorf("%s: database schema is not compatible with runtime %s", status.ErrorCode, strings.TrimSpace(s.config.RuntimeVersion))
	}
	return nil
}

func (s *RuntimeStore) verifyMigration(ctx context.Context, path string) error {
	name := filepath.Base(path)
	expected, err := migrationChecksum(path)
	if err != nil {
		return err
	}
	var applied string
	var dirty bool
	query := "SELECT " + s.identifier("checksum") + ", " + s.identifier("dirty") + " FROM " + s.tableIdentifier("_schema_migrations") + " WHERE " + s.identifier("path") + " = " + s.placeholder(1)
	err = s.db.QueryRowContext(ctx, query, name).Scan(&applied, &dirty)
	if err == sql.ErrNoRows {
		return fmt.Errorf("migration.pending: %s", name)
	}
	if err != nil {
		return err
	}
	if dirty {
		return fmt.Errorf("migration.dirty: %s", name)
	}
	if strings.TrimSpace(applied) == "" {
		return fmt.Errorf("migration.checksum_drift: checksum missing for %s", name)
	}
	if applied != expected {
		return fmt.Errorf("migration.checksum_drift: %s", name)
	}
	return nil
}

func (s *RuntimeStore) migrationPending(ctx context.Context, path string) (bool, error) {
	name := filepath.Base(path)
	expected, err := migrationChecksum(path)
	if err != nil {
		return false, err
	}
	var applied string
	var dirty bool
	query := "SELECT " + s.identifier("checksum") + ", " + s.identifier("dirty") + " FROM " + s.tableIdentifier("_schema_migrations") + " WHERE " + s.identifier("path") + " = " + s.placeholder(1)
	err = s.schemaDatabase().QueryRowContext(ctx, query, name).Scan(&applied, &dirty)
	if err == sql.ErrNoRows {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	if dirty {
		return false, fmt.Errorf("migration.dirty: %s", name)
	}
	if strings.TrimSpace(applied) == "" {
		update := "UPDATE " + s.tableIdentifier("_schema_migrations") + " SET " + s.identifier("checksum") + " = " + s.placeholder(1) + " WHERE " + s.identifier("path") + " = " + s.placeholder(2)
		if _, err := s.schemaDatabase().ExecContext(ctx, update, expected, name); err != nil {
			return false, fmt.Errorf("backfill migration checksum: %w", err)
		}
		return false, nil
	}
	if applied != expected {
		return false, fmt.Errorf("migration.checksum_drift: %s", name)
	}
	return false, nil
}

func (s *RuntimeStore) applyMigrationFile(ctx context.Context, path string) error {
	startedAt := time.Now()
	name := filepath.Base(path)
	pending, err := s.migrationPending(ctx, path)
	if err != nil {
		return err
	}
	if !pending {
		return nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read: %w", err)
	}
	checksumBytes := sha256.Sum256(raw)
	checksum := hex.EncodeToString(checksumBytes[:])
	version, migrationName := migrationIdentity(name)
	insertDirty := "INSERT INTO " + s.tableIdentifier("_schema_migrations") + " (" + migrationColumns(s) + ") VALUES (" + strings.Join(placeholders(s, 12), ", ") + ")"
	if _, err := s.schemaDatabase().ExecContext(ctx, insertDirty, name, version, migrationName, migrationKind(migrationName), checksum, true, time.Now().UTC().Format(time.RFC3339), strings.TrimSpace(s.config.RuntimeVersion), 0, migrationOperator(s.config), migrationInstanceID(s.config), strings.TrimSpace(s.migrationBackupID)); err != nil {
		return fmt.Errorf("record dirty migration: %w", err)
	}
	tx, err := s.schemaDatabase().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer tx.Rollback()
	if s.dialect.Name() == "postgres" {
		if _, err := tx.ExecContext(ctx, "SELECT set_config('search_path', "+s.placeholder(1)+", true)", s.DatabaseSchema()); err != nil {
			return fmt.Errorf("set migration schema search path: %w", err)
		}
		if s.config.DatabaseLockTimeout > 0 {
			if _, err := tx.ExecContext(ctx, "SELECT set_config('lock_timeout', "+s.placeholder(1)+", true)", durationMilliseconds(s.config.DatabaseLockTimeout)); err != nil {
				return fmt.Errorf("set migration lock timeout: %w", err)
			}
		}
		if s.config.DatabaseStatementTimeout > 0 {
			if _, err := tx.ExecContext(ctx, "SELECT set_config('statement_timeout', "+s.placeholder(1)+", true)", durationMilliseconds(s.config.DatabaseStatementTimeout)); err != nil {
				return fmt.Errorf("set migration statement timeout: %w", err)
			}
		}
	}
	for _, statement := range splitSQLStatements(string(raw)) {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migration.failed: execute %s: %w", name, err)
		}
	}
	duration := time.Since(startedAt)
	completeMigration := "UPDATE " + s.tableIdentifier("_schema_migrations") + " SET " + s.identifier("dirty") + " = FALSE, " + s.identifier("duration_ms") + " = " + s.placeholder(1) + ", " + s.identifier("applied_at") + " = " + s.placeholder(2) + " WHERE " + s.identifier("path") + " = " + s.placeholder(3) + " AND " + s.identifier("checksum") + " = " + s.placeholder(4)
	if _, err := tx.ExecContext(ctx, completeMigration, duration.Milliseconds(), time.Now().UTC().Format(time.RFC3339), name, checksum); err != nil {
		return fmt.Errorf("record migration: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func (s *RuntimeStore) schemaMigrationSQL() string {
	pathType := "TEXT"
	timeType := "TEXT"
	if s.dialect.Name() == "mysql" {
		pathType = "VARCHAR(255)"
		timeType = "VARCHAR(64)"
	}
	return "CREATE TABLE IF NOT EXISTS " + s.tableIdentifier("_schema_migrations") + " (" + s.identifier("path") + " " + pathType + " PRIMARY KEY, " + s.identifier("version") + " " + pathType + " NOT NULL DEFAULT '', " + s.identifier("name") + " " + pathType + " NOT NULL DEFAULT '', " + s.identifier("kind") + " " + pathType + " NOT NULL DEFAULT 'schema', " + s.identifier("checksum") + " " + pathType + " NOT NULL DEFAULT '', " + s.identifier("dirty") + " BOOLEAN NOT NULL DEFAULT FALSE, " + s.identifier("applied_at") + " " + timeType + " NOT NULL, " + s.identifier("runtime_version") + " " + pathType + " NOT NULL DEFAULT '', " + s.identifier("duration_ms") + " BIGINT NOT NULL DEFAULT 0, " + s.identifier("operator") + " " + pathType + " NOT NULL DEFAULT '', " + s.identifier("instance_id") + " " + pathType + " NOT NULL DEFAULT '', " + s.identifier("backup_id") + " " + pathType + " NOT NULL DEFAULT '')"
}

func (s *RuntimeStore) ensureMigrationLedger(ctx context.Context) error {
	db := s.schemaDatabase()
	if s.dialect.Name() == "postgres" && strings.TrimSpace(s.databaseSchema) != "" && !strings.EqualFold(s.databaseSchema, "public") {
		if _, err := db.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS "+s.identifier(s.databaseSchema)); err != nil {
			return fmt.Errorf("prepare PostgreSQL Runtime schema: %w", err)
		}
	}
	if _, err := db.ExecContext(ctx, s.schemaMigrationSQL()); err != nil {
		return err
	}
	textType := "TEXT NOT NULL DEFAULT ''"
	if s.dialect.Name() == "mysql" {
		textType = "VARCHAR(255) NOT NULL DEFAULT ''"
	}
	columns := []struct{ name, definition string }{
		{"version", textType}, {"name", textType}, {"kind", textType}, {"checksum", textType},
		{"dirty", "BOOLEAN NOT NULL DEFAULT FALSE"}, {"runtime_version", textType}, {"duration_ms", "BIGINT NOT NULL DEFAULT 0"},
		{"operator", textType}, {"instance_id", textType}, {"backup_id", textType},
	}
	for _, column := range columns {
		rows, queryErr := db.QueryContext(ctx, "SELECT "+s.identifier(column.name)+" FROM "+s.tableIdentifier("_schema_migrations")+" WHERE 1 = 0")
		if queryErr == nil {
			_ = rows.Close()
			continue
		}
		if _, alterErr := db.ExecContext(ctx, "ALTER TABLE "+s.tableIdentifier("_schema_migrations")+" ADD COLUMN "+s.identifier(column.name)+" "+column.definition); alterErr != nil {
			return alterErr
		}
	}
	return nil
}

func (s *RuntimeStore) acquireMigrationLock(ctx context.Context, cfg config.Config) (func(), error) {
	if s.dialect.Name() == "sqlite" {
		return s.acquireSQLiteMigrationLock(ctx, cfg)
	}
	lockDB := s.migrationDB
	if lockDB == nil {
		lockDB = s.db
	}
	conn, err := lockDB.Conn(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire migration connection: %w", err)
	}
	key := "domainry_runtime_migrations:" + s.DatabaseSchema()
	if s.dialect.Name() == "mysql" {
		key = "domainry_runtime_migrations"
	}
	started := time.Now()
	deadline := s.config.DatabaseLockTimeout
	if deadline <= 0 {
		deadline = 30 * time.Second
	}
	lockCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()
	locked := false
	for !locked {
		var result any
		if s.dialect.Name() == "postgres" {
			err = conn.QueryRowContext(lockCtx, "SELECT pg_try_advisory_lock(hashtextextended("+s.placeholder(1)+", 0))", key).Scan(&locked)
		} else {
			var mysqlResult sql.NullInt64
			err = conn.QueryRowContext(lockCtx, "SELECT GET_LOCK("+s.placeholder(1)+", 0)", key).Scan(&mysqlResult)
			locked = mysqlResult.Valid && mysqlResult.Int64 == 1
			result = mysqlResult
		}
		_ = result
		if err != nil {
			_ = conn.Close()
			return nil, fmt.Errorf("acquire migration lock: %w", err)
		}
		if !locked {
			select {
			case <-lockCtx.Done():
				_ = conn.Close()
				return nil, fmt.Errorf("migration.lock_timeout: owner=%s timeout=%s: %w", migrationInstanceID(cfg), deadline, lockCtx.Err())
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
	s.migrationConn = conn
	if s.operationalMetrics != nil {
		s.operationalMetrics.ObserveMigrationLock(time.Since(started), err)
	}
	return func() {
		timeout := s.config.DatabaseConnectTimeout
		if timeout <= 0 {
			timeout = 5 * time.Second
		}
		unlockCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
		defer cancel()
		if s.dialect.Name() == "postgres" {
			_, _ = conn.ExecContext(unlockCtx, "SELECT pg_advisory_unlock(hashtextextended("+s.placeholder(1)+", 0))", key)
		} else {
			_, _ = conn.ExecContext(unlockCtx, "SELECT RELEASE_LOCK("+s.placeholder(1)+")", key)
		}
		s.migrationConn = nil
		_ = conn.Close()
	}, nil
}

func (s *RuntimeStore) acquireSQLiteMigrationLock(ctx context.Context, cfg config.Config) (func(), error) {
	path := strings.TrimSpace(cfg.DBPath)
	if path == "" || path == ":memory:" || strings.HasPrefix(path, "file:") {
		return func() { _ = path }, nil
	}
	lockPath := path + ".migration.lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open sqlite migration lock: %w", err)
	}
	deadline := s.config.DatabaseLockTimeout
	if deadline <= 0 {
		deadline = 30 * time.Second
	}
	lockCtx, cancel := context.WithTimeout(ctx, deadline)
	for {
		err = filelock.TryExclusive(file)
		if err == nil {
			break
		}
		select {
		case <-lockCtx.Done():
			cancel()
			_ = file.Close()
			return nil, fmt.Errorf("migration.lock_timeout: owner=%s timeout=%s: %w", migrationInstanceID(cfg), deadline, lockCtx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
	cancel()
	owner := migrationInstanceID(cfg) + " acquired_at=" + time.Now().UTC().Format(time.RFC3339Nano)
	_ = file.Truncate(0)
	_, _ = file.WriteString(owner)
	return func() { _ = filelock.Unlock(file); _ = file.Close() }, nil
}

func migrationIdentity(path string) (string, string) {
	base := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	parts := strings.SplitN(base, "_", 2)
	if len(parts) == 1 {
		return parts[0], parts[0]
	}
	return parts[0], parts[1]
}

func migrationKind(name string) string {
	name = strings.ToLower(strings.TrimSpace(name))
	if strings.HasPrefix(name, "data_") || strings.HasPrefix(name, "metadata_") || strings.HasPrefix(name, "manifest_") {
		return "metadata_data"
	}
	return "schema"
}

func migrationOperator(cfg config.Config) string {
	if value := strings.TrimSpace(cfg.MigrationOperator); value != "" {
		return value
	}
	return "runtime"
}

func migrationInstanceID(cfg config.Config) string {
	if value := strings.TrimSpace(cfg.MigrationInstanceID); value != "" {
		return value
	}
	host, _ := os.Hostname()
	return strings.TrimSpace(host) + ":" + strconv.Itoa(os.Getpid())
}

func migrationColumns(s *RuntimeStore) string {
	columns := []string{"path", "version", "name", "kind", "checksum", "dirty", "applied_at", "runtime_version", "duration_ms", "operator", "instance_id", "backup_id"}
	return strings.Join(quotedColumns(s, columns), ", ")
}

func migrationChecksum(path string) (string, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read migration checksum: %w", err)
	}
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:]), nil
}

func durationMilliseconds(value time.Duration) string {
	return fmt.Sprintf("%dms", value.Milliseconds())
}

func (s *RuntimeStore) migrationPaths(cfg config.Config) ([]string, error) {
	if strings.TrimSpace(cfg.MigrationSQL) != "" {
		return []string{cfg.MigrationSQL}, nil
	}
	driverDir := filepath.Join(cfg.MigrationDir, s.dialect.Name())
	if entries, err := s.readMigrationDir(driverDir); err == nil {
		return sqlPaths(driverDir, entries)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	entries, err := s.readMigrationDir(cfg.MigrationDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	return sqlPaths(cfg.MigrationDir, entries)
}

func (s *RuntimeStore) readMigrationDir(path string) ([]os.DirEntry, error) {
	if s.migrationReadDir != nil {
		return s.migrationReadDir(path)
	}
	return os.ReadDir(path)
}

func sqlPaths(dir string, entries []os.DirEntry) ([]string, error) {
	paths := []string{}
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".sql" {
			continue
		}
		paths = append(paths, filepath.Join(dir, entry.Name()))
	}
	sort.Strings(paths)
	if len(paths) == 0 {
		return nil, nil
	}
	return paths, nil
}

func migrationNames(paths []string) []string {
	names := make([]string, 0, len(paths))
	for _, path := range paths {
		names = append(names, filepath.Base(path))
	}
	sort.Strings(names)
	return names
}

func (s *RuntimeStore) setExpectedMigrations(paths []string) error {
	s.expectedMigrations = migrationNames(paths)
	s.expectedChecksums = make(map[string]string, len(paths))
	for _, path := range paths {
		checksum, err := migrationChecksum(path)
		if err != nil {
			return err
		}
		s.expectedChecksums[filepath.Base(path)] = checksum
	}
	return nil
}

func splitSQLStatements(raw string) []string {
	sqlLines := []string{}
	for _, line := range strings.Split(raw, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "--") {
			continue
		}
		sqlLines = append(sqlLines, line)
	}
	parts := strings.Split(strings.Join(sqlLines, "\n"), ";")
	statements := make([]string, 0, len(parts))
	for _, part := range parts {
		statement := strings.TrimSpace(part)
		if statement == "" {
			continue
		}
		statements = append(statements, statement)
	}
	return statements
}
