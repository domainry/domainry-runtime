package database

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/base"
	runtimeschema "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/schema"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

const CurrentRuntimeSchemaVersion = "011_notification_service_publication_outbox"

const (
	managedDatabaseCohortTable           = "_domainry_managed_runtime_database_cohort"
	managedDatabaseCohortContractVersion = "domainry-managed-runtime-database-cohort-v1"
)

func SupportedRuntimeSchemaUpgradeVersions() []string {
	return []string{"001_connector_runtime_lifecycle", "002_data_lifecycle_governance", "003_operations_reliability", "004_runtime_release_cohort", "005_identity_workforce_separation", "006_party_foundation", "007_identity_global_names", "008_identity_account_directory", "009_managed_database_cohort", "010_external_identity_ownership"}
}

func (s *RuntimeStore) EnsureRuntimeSchema(ctx context.Context) error {
	if s.config.EffectiveDatabaseMigrationMode() == "verify" {
		if err := s.verifyRuntimeSchema(ctx); err != nil {
			return err
		}
		if err := s.verifyManagedDatabaseCohortMarker(ctx); err != nil {
			return err
		}
		return s.EnsureWorkspaceRLS(ctx)
	}
	if s.migrationDB != nil {
		migrationStore := s.runtimeMigrationStore()
		migrationStore.config.DatabaseRLSEnabled = false
		if err := migrationStore.EnsureRuntimeSchema(ctx); err != nil {
			return err
		}
		return s.EnsureWorkspaceRLS(ctx)
	}
	release, err := s.acquireMigrationLock(ctx, s.config)
	if err != nil {
		return err
	}
	defer release()
	startedAt := time.Now()
	pending, err := s.runtimeSchemaMigrationPending(ctx, CurrentRuntimeSchemaVersion)
	if err != nil {
		return err
	}
	if pending {
		if err := s.ValidateLegacyWorkspaceScopes(ctx); err != nil {
			return err
		}
		if err := s.ensureMigrationBackupForExistingData(ctx, s.runtimeMigrationConfig()); err != nil {
			return err
		}
		if err := s.startRuntimeSchemaMigration(ctx, CurrentRuntimeSchemaVersion); err != nil {
			return err
		}
	}
	if err := s.ensureManagedDatabaseCohortMarker(ctx); err != nil {
		return err
	}
	if err := s.EnsureMetadataSchema(ctx); err != nil {
		return err
	}
	if err := s.EnsureEvidenceSchema(ctx); err != nil {
		return err
	}
	if err := s.EnsureWorkflowProcessSchema(ctx); err != nil {
		return err
	}
	if err := s.EnsureLifecycleSchema(ctx); err != nil {
		return err
	}
	if err := s.recordRuntimeSchemaMigrationIfPending(ctx, pending, startedAt); err != nil {
		return err
	}
	return s.EnsureWorkspaceRLS(ctx)
}

func (s *RuntimeStore) runtimeMigrationStore() *RuntimeStore {
	return &RuntimeStore{
		SQLDatabase:          base.NewSQLDatabase(s.migrationDB, s.engine, s.databaseSchema),
		db:                   s.migrationDB,
		engine:               s.engine,
		config:               s.config,
		databaseSchema:       s.databaseSchema,
		postgresProfile:      s.postgresProfile,
		postgresCapabilities: s.postgresCapabilities,
		migratorCapabilities: s.migratorCapabilities,
		expectedMigrations:   s.expectedMigrations,
		expectedChecksums:    s.expectedChecksums,
		secretMaterialKey:    s.secretMaterialKey,
		secretKeyProvider:    s.secretKeyProvider,
		migrationBackupReady: s.migrationBackupReady,
		migrationCompatible:  s.migrationCompatible,
		migrationBackupID:    s.migrationBackupID,
		idempotencyMetrics:   s.idempotencyMetrics,
		sqlMetrics:           s.sqlMetrics,
		operationalMetrics:   s.operationalMetrics,
		workspaceRLS:         s.workspaceRLS,
		workerScopeCursor:    s.workerScopeCursor,
		workerWakeups:        s.workerWakeups,
		schemaAssembler:      s.schemaAssembler,
		backupChecksum:       s.backupChecksum,
		migrationReadDir:     s.migrationReadDir,
	}
}

func (s *RuntimeStore) recordRuntimeSchemaMigrationIfPending(ctx context.Context, pending bool, startedAt time.Time) error {
	if !pending {
		return nil
	}
	return s.recordRuntimeSchemaMigration(ctx, CurrentRuntimeSchemaVersion, time.Since(startedAt))
}

func (s *RuntimeStore) verifyRuntimeSchema(ctx context.Context) error {
	var checksum string
	var dirty bool
	query := "SELECT " + s.identifier("checksum") + ", " + s.identifier("dirty") + " FROM " + s.tableIdentifier("_schema_materializations") + " WHERE " + s.identifier("version") + " = " + s.placeholder(1)
	if err := s.db.QueryRowContext(ctx, query, CurrentRuntimeSchemaVersion).Scan(&checksum, &dirty); err != nil {
		return fmt.Errorf("verify runtime schema compatibility: %w", err)
	}
	if dirty {
		return fmt.Errorf("migration.dirty: runtime schema %s", CurrentRuntimeSchemaVersion)
	}
	if checksum != currentRuntimeSchemaChecksum() {
		return fmt.Errorf("migration.checksum_drift: runtime schema %s", CurrentRuntimeSchemaVersion)
	}
	return nil
}

type schemaDatabase = runtimeschema.SQLDatabase

type runtimeSchemaAssembler interface {
	EnsureMetadataSchema(context.Context, runtimeschema.Store) error
	EnsureEvidenceSchema(context.Context, runtimeschema.Store) error
	EnsureWorkflowProcessSchema(context.Context, runtimeschema.Store) error
	EnsureLifecycleSchema(context.Context, runtimeschema.Store) error
}

func (s *RuntimeStore) schemaDatabase() schemaDatabase {
	if s.migrationConn != nil {
		return s.migrationConn
	}
	if s.migrationDB != nil {
		return s.migrationDB
	}
	return s.db
}

// SchemaDB returns the advisory-lock-owning migration connection when schema
// assembly is running, so a one-connection migrator pool cannot self-deadlock.
func (s *RuntimeStore) SchemaDB() runtimeschema.SQLDatabase {
	return s.schemaDatabase()
}

func (s *RuntimeStore) EnsureMetadataSchema(ctx context.Context) error {
	if s.schemaAssembler != nil {
		return s.schemaAssembler.EnsureMetadataSchema(ctx, s)
	}
	return runtimeschema.EnsureMetadataSchema(ctx, s)
}

func (s *RuntimeStore) EnsureEvidenceSchema(ctx context.Context) error {
	if s.schemaAssembler != nil {
		return s.schemaAssembler.EnsureEvidenceSchema(ctx, s)
	}
	return runtimeschema.EnsureEvidenceSchema(ctx, s)
}

func (s *RuntimeStore) EnsureWorkflowProcessSchema(ctx context.Context) error {
	if s.schemaAssembler != nil {
		return s.schemaAssembler.EnsureWorkflowProcessSchema(ctx, s)
	}
	return runtimeschema.EnsureWorkflowProcessSchema(ctx, s)
}

func (s *RuntimeStore) EnsureLifecycleSchema(ctx context.Context) error {
	if s.schemaAssembler != nil {
		return s.schemaAssembler.EnsureLifecycleSchema(ctx, s)
	}
	return runtimeschema.EnsureLifecycleSchema(ctx, s)
}

func (s *RuntimeStore) runtimeMigrationConfig() config.Config {
	cfg := s.config
	if strings.TrimSpace(cfg.MigrationBackupDir) == "" {
		dbPath := s.sqlBase().RuntimeEngine.MigrationDatabasePath(cfg)
		if dbPath != "" && dbPath != ":memory:" {
			cfg.MigrationBackupDir = filepath.Join(filepath.Dir(dbPath), "migration-backups")
		}
	}
	return cfg
}

func (s *RuntimeStore) runtimeSchemaMigrationPending(ctx context.Context, version string) (bool, error) {
	db := s.schemaDatabase()
	text := s.metadataIDColumnType()
	if _, err := db.ExecContext(ctx, "CREATE TABLE IF NOT EXISTS "+s.tableIdentifier("_schema_materializations")+" ("+s.identifier("version")+" "+text+" PRIMARY KEY, "+s.identifier("name")+" "+text+" NOT NULL DEFAULT '', "+s.identifier("kind")+" "+text+" NOT NULL DEFAULT 'runtime_schema_data', "+s.identifier("checksum")+" "+text+" NOT NULL DEFAULT '', "+s.identifier("dirty")+" BOOLEAN NOT NULL DEFAULT FALSE, "+s.identifier("applied_at")+" "+text+" NOT NULL, "+s.identifier("runtime_version")+" "+text+" NOT NULL DEFAULT '', "+s.identifier("duration_ms")+" BIGINT NOT NULL DEFAULT 0, "+s.identifier("operator")+" "+text+" NOT NULL DEFAULT '', "+s.identifier("instance_id")+" "+text+" NOT NULL DEFAULT '', "+s.identifier("backup_id")+" "+text+" NOT NULL DEFAULT '')"); err != nil {
		return false, fmt.Errorf("prepare runtime schema migration ledger: %w", err)
	}
	columns := []struct{ name, definition string }{{"name", text + " NOT NULL DEFAULT ''"}, {"kind", text + " NOT NULL DEFAULT 'runtime_schema_data'"}, {"checksum", text + " NOT NULL DEFAULT ''"}, {"dirty", "BOOLEAN NOT NULL DEFAULT FALSE"}, {"runtime_version", text + " NOT NULL DEFAULT ''"}, {"duration_ms", "BIGINT NOT NULL DEFAULT 0"}, {"operator", text + " NOT NULL DEFAULT ''"}, {"instance_id", text + " NOT NULL DEFAULT ''"}, {"backup_id", text + " NOT NULL DEFAULT ''"}}
	for _, column := range columns {
		rows, queryErr := db.QueryContext(ctx, "SELECT "+s.identifier(column.name)+" FROM "+s.tableIdentifier("_schema_materializations")+" WHERE 1 = 0")
		if queryErr == nil {
			_ = rows.Close()
			continue
		}
		if _, alterErr := db.ExecContext(ctx, "ALTER TABLE "+s.tableIdentifier("_schema_materializations")+" ADD COLUMN "+s.identifier(column.name)+" "+column.definition); alterErr != nil {
			return false, alterErr
		}
	}
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+s.tableIdentifier("_schema_materializations")+" WHERE "+s.identifier("version")+" = "+s.placeholder(1), version).Scan(&count); err != nil {
		return false, fmt.Errorf("check runtime schema migration: %w", err)
	}
	if count == 0 {
		return true, nil
	}
	var checksum string
	var dirty bool
	if err := db.QueryRowContext(ctx, "SELECT "+s.identifier("checksum")+", "+s.identifier("dirty")+" FROM "+s.tableIdentifier("_schema_materializations")+" WHERE "+s.identifier("version")+" = "+s.placeholder(1), version).Scan(&checksum, &dirty); err != nil {
		return false, err
	}
	if dirty {
		return false, fmt.Errorf("migration.dirty: runtime schema %s", version)
	}
	if strings.TrimSpace(checksum) == "" {
		_, err := db.ExecContext(ctx, "UPDATE "+s.tableIdentifier("_schema_materializations")+" SET "+s.identifier("checksum")+" = "+s.placeholder(1)+" WHERE "+s.identifier("version")+" = "+s.placeholder(2), currentRuntimeSchemaChecksum(), version)
		return false, err
	}
	if checksum != currentRuntimeSchemaChecksum() {
		return false, fmt.Errorf("migration.checksum_drift: runtime schema %s", version)
	}
	return false, nil
}

func (s *RuntimeStore) startRuntimeSchemaMigration(ctx context.Context, version string) error {
	columns := []string{"version", "name", "kind", "checksum", "dirty", "applied_at", "runtime_version", "duration_ms", "operator", "instance_id", "backup_id"}
	query := "INSERT INTO " + s.tableIdentifier("_schema_materializations") + " (" + strings.Join(quotedColumns(s, columns), ", ") + ") VALUES (" + strings.Join(placeholders(s, len(columns)), ", ") + ")"
	_, err := s.schemaDatabase().ExecContext(ctx, query, version, "managed_database_cohort", "runtime_schema_data", currentRuntimeSchemaChecksum(), true, time.Now().UTC().Format(time.RFC3339), s.config.RuntimeVersion, 0, migrationOperator(s.config), migrationInstanceID(s.config), s.migrationBackupID)
	return err
}

func (s *RuntimeStore) recordRuntimeSchemaMigration(ctx context.Context, version string, duration time.Duration) error {
	_, err := s.schemaDatabase().ExecContext(ctx, "UPDATE "+s.tableIdentifier("_schema_materializations")+" SET "+s.identifier("dirty")+" = FALSE, "+s.identifier("duration_ms")+" = "+s.placeholder(1)+", "+s.identifier("applied_at")+" = "+s.placeholder(2)+" WHERE "+s.identifier("version")+" = "+s.placeholder(3), duration.Milliseconds(), time.Now().UTC().Format(time.RFC3339), version)
	if err != nil {
		return fmt.Errorf("record runtime schema migration: %w", err)
	}
	return nil
}

func currentRuntimeSchemaChecksum() string {
	sum := sha256.Sum256([]byte(CurrentRuntimeSchemaVersion + ":metadata,object_fields,record_data,evidence,party,lifecycle,operations,indexes,report_snapshots,runtime_release_cohorts,runtime_release_instances,managed_database_cohort,external_identity_ownership"))
	return hex.EncodeToString(sum[:])
}

func (s *RuntimeStore) ensureManagedDatabaseCohortMarker(ctx context.Context) error {
	if !s.sqlBase().RuntimeEngine.ManagedDatabaseMarkerEnabled() {
		return nil
	}
	database := s.schemaDatabase()
	table := s.tableIdentifier(managedDatabaseCohortTable)
	statement := "CREATE TABLE IF NOT EXISTS " + table + " (" + s.identifier("marker_id") + " SMALLINT NOT NULL PRIMARY KEY, " + s.identifier("contract_version") + " VARCHAR(128) NOT NULL, " + s.identifier("database_identity_sha256") + " CHAR(64) NOT NULL)"
	if _, err := database.ExecContext(ctx, statement); err != nil {
		return fmt.Errorf("prepare managed database cohort marker: %w", err)
	}
	seed := make([]byte, 32)
	if _, err := rand.Read(seed); err != nil {
		return fmt.Errorf("generate managed database cohort marker: %w", err)
	}
	identity := sha256.Sum256(seed)
	insert, arguments, err := ormbuilder.NewInsertBuilder(s.sqlBase().SQLRenderer, managedDatabaseCohortTable).
		Columns("marker_id", "contract_version", "database_identity_sha256").
		Values(1, managedDatabaseCohortContractVersion, hex.EncodeToString(identity[:])).
		OnConflictDoNothing("marker_id").Build()
	if err != nil {
		return fmt.Errorf("build managed database cohort marker: %w", err)
	}
	if _, err := database.ExecContext(ctx, insert, arguments...); err != nil {
		return fmt.Errorf("initialize managed database cohort marker: %w", err)
	}
	return s.verifyManagedDatabaseCohortMarkerWith(ctx, database)
}

func (s *RuntimeStore) verifyManagedDatabaseCohortMarker(ctx context.Context) error {
	if !s.sqlBase().RuntimeEngine.ManagedDatabaseMarkerEnabled() {
		return nil
	}
	return s.verifyManagedDatabaseCohortMarkerWith(ctx, s.db)
}

func (s *RuntimeStore) verifyManagedDatabaseCohortMarkerWith(ctx context.Context, database schemaDatabase) error {
	var contractVersion, identity string
	query := "SELECT " + s.identifier("contract_version") + ", " + s.identifier("database_identity_sha256") + " FROM " + s.tableIdentifier(managedDatabaseCohortTable) + " WHERE " + s.identifier("marker_id") + " = " + s.placeholder(1)
	if err := database.QueryRowContext(ctx, query, 1).Scan(&contractVersion, &identity); err != nil {
		return fmt.Errorf("verify managed database cohort marker: %w", err)
	}
	if contractVersion != managedDatabaseCohortContractVersion || len(identity) != 64 {
		return fmt.Errorf("verify managed database cohort marker: invalid marker identity")
	}
	if _, err := hex.DecodeString(identity); err != nil {
		return fmt.Errorf("verify managed database cohort marker: invalid marker identity")
	}
	return nil
}

func (s *RuntimeStore) ensureRuntimeColumn(ctx context.Context, table, column, definition string) error {
	db := s.schemaDatabase()
	rows, err := db.QueryContext(ctx, "SELECT "+column+" FROM "+s.tableIdentifier(table)+" WHERE 1 = 0")
	if err == nil {
		return rows.Close()
	}
	definition = s.runtimeColumnDefinition(definition)
	if _, alterErr := db.ExecContext(ctx, "ALTER TABLE "+s.tableIdentifier(table)+" ADD COLUMN "+s.identifier(column)+" "+definition); alterErr != nil {
		return fmt.Errorf("add %s.%s: %w", table, column, alterErr)
	}
	return nil
}

func (s *RuntimeStore) runtimeColumnDefinition(definition string) string {
	return s.sqlBase().RuntimeEngine.ColumnDefinition(definition)
}
