package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/domainry/domainry-notification-sdk/modulehost"
)

var moduleMigrationIdentityPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)

// Schema exposes the trusted physical schema to source-owned Module migration
// renderers. It is empty for SQLite and MySQL.
func (s *RuntimeStore) Schema() string { return s.DatabaseSchema() }

// ApplyOwnedMigrations applies source-owned DDL under Runtime's project
// migration lock and checksum ledger. The owner-qualified path prevents
// collisions with Runtime file migrations and other embedded modules.
func (s *RuntimeStore) ApplyOwnedMigrations(ctx context.Context, owner string, migrations []modulehost.SchemaMigration) error {
	owner = strings.TrimSpace(owner)
	if s == nil || !moduleMigrationIdentityPattern.MatchString(owner) {
		return fmt.Errorf("module migration owner is invalid")
	}
	for index, migration := range migrations {
		if migration.Version == 0 || !moduleMigrationIdentityPattern.MatchString(strings.TrimSpace(migration.Name)) || len(migration.Statements) == 0 {
			return fmt.Errorf("module migration %s[%d] is invalid", owner, index)
		}
		if index > 0 && migrations[index-1].Version >= migration.Version {
			return fmt.Errorf("module migrations for %s are not strictly ordered", owner)
		}
	}
	release, err := s.acquireMigrationLock(ctx, s.config)
	if err != nil {
		return err
	}
	defer release()
	if err := s.ensureMigrationLedger(ctx); err != nil {
		return fmt.Errorf("prepare module migration ledger: %w", err)
	}
	for _, migration := range migrations {
		if err := s.applyOwnedMigration(ctx, owner, migration); err != nil {
			return err
		}
	}
	return nil
}

func (s *RuntimeStore) applyOwnedMigration(ctx context.Context, owner string, migration modulehost.SchemaMigration) error {
	path := moduleMigrationPath(owner, migration)
	checksum := moduleMigrationChecksum(migration)
	var applied string
	var dirty bool
	query := "SELECT " + s.identifier("checksum") + "," + s.identifier("dirty") + " FROM " + s.tableIdentifier("_schema_migrations") + " WHERE " + s.identifier("path") + "=" + s.placeholder(1)
	err := s.schemaDatabase().QueryRowContext(ctx, query, path).Scan(&applied, &dirty)
	if err == nil {
		if dirty {
			return fmt.Errorf("migration.dirty: %s", path)
		}
		if applied != checksum {
			return fmt.Errorf("migration.checksum_drift: %s", path)
		}
		return nil
	}
	if err != sql.ErrNoRows {
		return fmt.Errorf("inspect module migration %s: %w", path, err)
	}
	if s.config.EffectiveDatabaseMigrationMode() == "verify" {
		return fmt.Errorf("migration.pending: %s", path)
	}
	baseline, err := s.moduleMigrationBaselineState(ctx, migration)
	if err != nil {
		return fmt.Errorf("inspect module migration baseline %s: %w", path, err)
	}
	if baseline < 0 {
		return fmt.Errorf("migration.partial_baseline: %s", path)
	}
	started := time.Now()
	if err := s.insertOwnedMigration(ctx, path, owner, migration, checksum, baseline == 1); err != nil {
		return err
	}
	if baseline == 1 {
		return nil
	}
	tx, err := s.schemaDatabase().BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin module migration %s: %w", path, err)
	}
	defer tx.Rollback()
	for _, statement := range migration.Statements {
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("migration.failed: execute %s: %w", path, err)
		}
	}
	complete := "UPDATE " + s.tableIdentifier("_schema_migrations") + " SET " + s.identifier("dirty") + "=FALSE," + s.identifier("duration_ms") + "=" + s.placeholder(1) + "," + s.identifier("applied_at") + "=" + s.placeholder(2) + " WHERE " + s.identifier("path") + "=" + s.placeholder(3) + " AND " + s.identifier("checksum") + "=" + s.placeholder(4)
	if _, err := tx.ExecContext(ctx, complete, time.Since(started).Milliseconds(), time.Now().UTC().Format(time.RFC3339), path, checksum); err != nil {
		return fmt.Errorf("record module migration %s: %w", path, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit module migration %s: %w", path, err)
	}
	return nil
}

// moduleMigrationBaselineState returns 1 when every legacy table exists, 0
// when none exists, and -1 for an unsafe partial legacy installation.
func (s *RuntimeStore) moduleMigrationBaselineState(ctx context.Context, migration modulehost.SchemaMigration) (int, error) {
	if migration.Version != 1 || len(migration.BaselineTables) == 0 {
		return 0, nil
	}
	found := 0
	for _, table := range migration.BaselineTables {
		if !moduleMigrationIdentityPattern.MatchString(table) {
			return 0, fmt.Errorf("baseline table %q is invalid", table)
		}
		rows, err := s.schemaDatabase().QueryContext(ctx, "SELECT 1 FROM "+s.tableIdentifier(table)+" WHERE 1=0")
		if err == nil {
			found++
			_ = rows.Close()
		}
	}
	if found == 0 {
		return 0, nil
	}
	if found == len(migration.BaselineTables) {
		return 1, nil
	}
	return -1, nil
}

func (s *RuntimeStore) insertOwnedMigration(ctx context.Context, path, owner string, migration modulehost.SchemaMigration, checksum string, complete bool) error {
	dirty := !complete
	query := "INSERT INTO " + s.tableIdentifier("_schema_migrations") + " (" + migrationColumns(s) + ") VALUES (" + strings.Join(placeholders(s, 12), ", ") + ")"
	if _, err := s.schemaDatabase().ExecContext(ctx, query, path, strconv.FormatUint(uint64(migration.Version), 10), migration.Name, "module:"+owner, checksum, dirty, time.Now().UTC().Format(time.RFC3339), strings.TrimSpace(s.config.RuntimeVersion), 0, migrationOperator(s.config), migrationInstanceID(s.config), strings.TrimSpace(s.migrationBackupID)); err != nil {
		return fmt.Errorf("record module migration %s: %w", path, err)
	}
	return nil
}

func moduleMigrationPath(owner string, migration modulehost.SchemaMigration) string {
	return fmt.Sprintf("module_%s_%06d_%s", owner, migration.Version, strings.TrimSpace(migration.Name))
}

func moduleMigrationChecksum(migration modulehost.SchemaMigration) string {
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "%d\x00%s\x00", migration.Version, strings.TrimSpace(migration.Name))
	for _, statement := range migration.Statements {
		_, _ = fmt.Fprintf(hash, "%s\x00", statement)
	}
	return hex.EncodeToString(hash.Sum(nil))
}

var _ modulehost.MigrationRegistrar = (*RuntimeStore)(nil)
