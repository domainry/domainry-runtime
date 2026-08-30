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
	ormmigration "github.com/domainry/domainry-orm/migration"
)

var moduleMigrationIdentityPattern = regexp.MustCompile(`^[a-z][a-z0-9_-]*$`)
var moduleSchemaIdentityPattern = regexp.MustCompile(`^_?[a-z][a-z0-9_]*$`)

// Schema exposes the trusted physical schema to source-owned Module migration
// renderers. It is empty for SQLite and MySQL.
func (s *RuntimeStore) Schema() string { return s.DatabaseSchema() }

// ApplyOwnedMigrations applies source-owned DDL under Runtime's project
// migration lock and checksum ledger. The owner-qualified path prevents
// collisions with Runtime file migrations and other embedded modules.
func (s *RuntimeStore) ApplyOwnedMigrations(ctx context.Context, owner string, migrations []modulehost.SchemaMigration) error {
	values := make([]ormmigration.Migration, len(migrations))
	for index, migration := range migrations {
		values[index] = ormmigration.Migration{Version: migration.Version, Name: migration.Name, Statements: append([]string(nil), migration.Statements...)}
		if migration.Baseline != nil {
			baseline := ormmigration.Baseline{Tables: make([]ormmigration.Table, len(migration.Baseline.Tables))}
			for tableIndex, table := range migration.Baseline.Tables {
				baseline.Tables[tableIndex] = ormmigration.Table{Name: table.Name, Columns: make([]ormmigration.Column, len(table.Columns)), Indexes: make([]ormmigration.Index, len(table.Indexes))}
				for columnIndex, column := range table.Columns {
					baseline.Tables[tableIndex].Columns[columnIndex] = ormmigration.Column{Name: column.Name, Type: column.Type, Nullable: column.Nullable, PrimaryKey: column.PrimaryKey}
				}
				for itemIndex, item := range table.Indexes {
					baseline.Tables[tableIndex].Indexes[itemIndex] = ormmigration.Index{Name: item.Name, Unique: item.Unique, Columns: append([]string(nil), item.Columns...)}
				}
			}
			values[index].Baseline = &baseline
		}
	}
	return s.ApplyORMOwnedMigrations(ctx, owner, values)
}

// ApplyORMOwnedMigrations is the host-native registrar for modules which use
// domainry-orm's migration contract directly.
func (s *RuntimeStore) ApplyORMOwnedMigrations(ctx context.Context, owner string, migrations []ormmigration.Migration) error {
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
	return s.applyOwnedMigrationsLocked(ctx, owner, migrations)
}

// ApplyOwnedMigration runs source-owned schema assembly under Runtime's lock
// and sole migration ledger. It exists for mature embedded modules whose DDL
// assembler predates the declarative statement contract; new modules should
// prefer ApplyOwnedMigrations.
func (s *RuntimeStore) ApplyOwnedMigration(ctx context.Context, owner string, version uint, name, checksum string, apply func(context.Context) error) error {
	owner, name, checksum = strings.TrimSpace(owner), strings.TrimSpace(name), strings.TrimSpace(checksum)
	if s == nil || !moduleMigrationIdentityPattern.MatchString(owner) || version == 0 || !moduleMigrationIdentityPattern.MatchString(name) || checksum == "" || apply == nil {
		return fmt.Errorf("module migration callback is invalid")
	}
	release, err := s.acquireMigrationLock(ctx, s.config)
	if err != nil {
		return err
	}
	defer release()
	if err := s.ensureMigrationLedger(ctx); err != nil {
		return fmt.Errorf("prepare module migration ledger: %w", err)
	}
	migration := ormmigration.Migration{Version: version, Name: name, Statements: []string{"source-owned-schema:" + checksum}}
	path := moduleMigrationPath(owner, migration)
	ledgerChecksum := moduleMigrationChecksum(migration)
	var appliedChecksum string
	var dirty bool
	query := "SELECT " + s.identifier("checksum") + "," + s.identifier("dirty") + " FROM " + s.tableIdentifier("_schema_migrations") + " WHERE " + s.identifier("path") + "=" + s.placeholder(1)
	err = s.schemaDatabase().QueryRowContext(ctx, query, path).Scan(&appliedChecksum, &dirty)
	if err == nil {
		if dirty {
			return fmt.Errorf("migration.dirty: %s", path)
		}
		if appliedChecksum != ledgerChecksum {
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
	started := time.Now()
	if err := s.insertOwnedMigration(ctx, path, owner, migration, ledgerChecksum, false); err != nil {
		return err
	}
	if err := apply(ctx); err != nil {
		return fmt.Errorf("migration.failed: execute %s: %w", path, err)
	}
	complete := "UPDATE " + s.tableIdentifier("_schema_migrations") + " SET " + s.identifier("dirty") + "=FALSE," + s.identifier("duration_ms") + "=" + s.placeholder(1) + "," + s.identifier("applied_at") + "=" + s.placeholder(2) + " WHERE " + s.identifier("path") + "=" + s.placeholder(3) + " AND " + s.identifier("checksum") + "=" + s.placeholder(4)
	if _, err := s.schemaDatabase().ExecContext(ctx, complete, time.Since(started).Milliseconds(), time.Now().UTC().Format(time.RFC3339), path, ledgerChecksum); err != nil {
		return fmt.Errorf("record module migration %s: %w", path, err)
	}
	return nil
}

// applyOwnedMigrationsLocked is used by the Runtime schema coordinator while
// it already owns the project migration lock.
func (s *RuntimeStore) applyOwnedMigrationsLocked(ctx context.Context, owner string, migrations []ormmigration.Migration) error {
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

func (s *RuntimeStore) applyOwnedMigration(ctx context.Context, owner string, migration ormmigration.Migration) error {
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
	baseline, err := s.proveModuleMigrationBaseline(ctx, migration.Baseline)
	if err != nil {
		return fmt.Errorf("migration.baseline_mismatch: %s: %w", path, err)
	}
	started := time.Now()
	if err := s.insertOwnedMigration(ctx, path, owner, migration, checksum, baseline); err != nil {
		return err
	}
	if baseline {
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

func (s *RuntimeStore) insertOwnedMigration(ctx context.Context, path, owner string, migration ormmigration.Migration, checksum string, complete bool) error {
	dirty := !complete
	query := "INSERT INTO " + s.tableIdentifier("_schema_migrations") + " (" + migrationColumns(s) + ") VALUES (" + strings.Join(placeholders(s, 12), ", ") + ")"
	if _, err := s.schemaDatabase().ExecContext(ctx, query, path, strconv.FormatUint(uint64(migration.Version), 10), migration.Name, "module:"+owner, checksum, dirty, time.Now().UTC().Format(time.RFC3339), strings.TrimSpace(s.config.RuntimeVersion), 0, migrationOperator(s.config), migrationInstanceID(s.config), strings.TrimSpace(s.migrationBackupID)); err != nil {
		return fmt.Errorf("record module migration %s: %w", path, err)
	}
	return nil
}

func moduleMigrationPath(owner string, migration ormmigration.Migration) string {
	return fmt.Sprintf("module_%s_%06d_%s", owner, migration.Version, strings.TrimSpace(migration.Name))
}

func moduleMigrationChecksum(migration ormmigration.Migration) string {
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "%d\x00%s\x00", migration.Version, strings.TrimSpace(migration.Name))
	for _, statement := range migration.Statements {
		_, _ = fmt.Fprintf(hash, "%s\x00", statement)
	}
	if migration.Baseline != nil {
		for _, table := range migration.Baseline.Tables {
			_, _ = fmt.Fprintf(hash, "table\x00%s\x00", table.Name)
			for _, column := range table.Columns {
				_, _ = fmt.Fprintf(hash, "column\x00%s\x00%s\x00%t\x00%t\x00", column.Name, column.Type, column.Nullable, column.PrimaryKey)
			}
			for _, index := range table.Indexes {
				_, _ = fmt.Fprintf(hash, "index\x00%s\x00%t\x00%s\x00", index.Name, index.Unique, strings.Join(index.Columns, ","))
			}
		}
	}
	return hex.EncodeToString(hash.Sum(nil))
}

var _ modulehost.MigrationRegistrar = (*RuntimeStore)(nil)
