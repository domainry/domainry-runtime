package database

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	auditmodulehost "github.com/domainry/domainry-audit-sdk/modulehost"
	auditmodule "github.com/domainry/domainry-audit/module"
	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	lifecyclemodulehost "github.com/domainry/domainry-lifecycle-sdk/modulehost"
	lifecyclemodule "github.com/domainry/domainry-lifecycle/module"
	metadatamodule "github.com/domainry/domainry-metadata/module"
	ormmigration "github.com/domainry/domainry-orm/migration"
	"github.com/domainry/domainry-orm/query"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/base"
	runtimeschema "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/schema"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

const CurrentRuntimeSchemaVersion = "020_tenant_initialization"

const (
	managedDatabaseCohortTable           = "_domainry_managed_runtime_database_cohort"
	managedDatabaseCohortContractVersion = "domainry-managed-runtime-database-cohort-v1"
)

func SupportedRuntimeSchemaUpgradeVersions() []string {
	return []string{"001_connector_runtime_lifecycle", "002_data_lifecycle_governance", "003_operations_reliability", "004_runtime_release_cohort", "005_identity_workforce_separation", "006_party_foundation", "007_identity_global_names", "008_identity_account_directory", "009_managed_database_cohort", "010_external_identity_ownership", "011_notification_service_publication_outbox", "012_rate_limit_schema_owner", "013_agent_schema_owner"}
}

func (s *RuntimeStore) EnsureRuntimeSchema(ctx context.Context) error {
	if s.config.EffectiveDatabaseMigrationMode() == "verify" {
		if err := s.verifyRuntimeSchema(ctx); err != nil {
			return err
		}
		if err := s.verifyManagedDatabaseCohortMarker(ctx); err != nil {
			return err
		}
		if err := s.EnsureLifecycleSchema(ctx); err != nil {
			return err
		}
		return nil
	}
	if s.migrationDB != nil {
		migrationStore := s.runtimeMigrationStore()
		if err := migrationStore.EnsureRuntimeSchema(ctx); err != nil {
			return err
		}
		return nil
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
	if err := runtimeschema.EnsureWorkspaceProvisioningSchema(ctx, s); err != nil {
		return err
	}
	metadataMigrations, err := metadatamodule.SchemaMigrationsForDialect(s.SQLRenderer)
	if err != nil {
		return err
	}
	ownedMetadataMigrations := make([]ormmigration.Migration, len(metadataMigrations))
	for index, migration := range metadataMigrations {
		ownedMetadataMigrations[index] = ormmigration.Migration{Version: migration.Version, Name: migration.Name, Statements: append([]string(nil), migration.Statements...)}
	}
	if err := s.applyOwnedMigrationsLocked(ctx, "metadata", ownedMetadataMigrations); err != nil {
		return err
	}
	s.metadataDefinitions = metadatamodule.NewDefinitionRepository(s.schemaDatabase(), s.SQLRenderer)
	if err := s.EnsureApplicationSchema(ctx); err != nil {
		return err
	}
	if err := s.EnsureEvidenceSchema(ctx); err != nil {
		return err
	}
	if err := s.ensureAuditModuleSchemaLocked(ctx); err != nil {
		return err
	}
	if err := s.EnsureWorkflowProcessSchema(ctx); err != nil {
		return err
	}
	if err := s.EnsureRateLimitSchema(ctx); err != nil {
		return err
	}
	if s.schemaAssembler != nil {
		if err := s.schemaAssembler.EnsureLifecycleSchema(ctx, s); err != nil {
			return err
		}
	} else if err := s.ensureLifecycleModuleLocked(ctx); err != nil {
		return err
	}
	if err := s.recordRuntimeSchemaMigrationIfPending(ctx, pending, startedAt); err != nil {
		return err
	}
	if err := s.removeObsoleteMigrationLedgers(ctx); err != nil {
		return err
	}
	return nil
}

func (s *RuntimeStore) ensureAuditModuleSchemaLocked(ctx context.Context) error {
	exists, err := s.RuntimeTableExists(ctx, "_audit_events")
	if errors.Is(err, sql.ErrNoRows) {
		exists, err = false, nil
	}
	if err != nil {
		return fmt.Errorf("inspect legacy Audit schema: %w", err)
	}
	if exists {
		rows, columnErr := s.schemaDatabase().QueryContext(ctx, "SELECT "+s.identifier("workspace_id")+" FROM "+s.tableIdentifier("_audit_events")+" WHERE 1 = 0")
		if columnErr != nil {
			return fmt.Errorf("legacy Audit workspace ownership is missing; initialize and adjudicate a real tenant before migration: %w", columnErr)
		}
		_ = rows.Close()
	}
	migrations, err := auditmodule.SchemaMigrations(s.RuntimeRenderer(), s.Driver())
	if err != nil {
		return err
	}
	if len(migrations) > 0 {
		if err := s.migrateLegacyAuditPrimaryKey(ctx, migrations[0]); err != nil {
			return err
		}
	}
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
				for indexIndex, item := range table.Indexes {
					baseline.Tables[tableIndex].Indexes[indexIndex] = ormmigration.Index{Name: item.Name, Unique: item.Unique, Columns: append([]string(nil), item.Columns...)}
				}
			}
			values[index].Baseline = &baseline
		}
	}
	return s.applyOwnedMigrationsLocked(ctx, "audit", values)
}

func (s *RuntimeStore) migrateLegacyAuditPrimaryKey(ctx context.Context, migration auditmodulehost.SchemaMigration) error {
	if migration.Baseline == nil || len(migration.Baseline.Tables) != 1 || len(migration.Statements) == 0 {
		return nil
	}
	actual, exists, err := s.inspectModuleSchemaTable(ctx, "_audit_events")
	if err != nil || !exists {
		return err
	}
	primary := map[string]bool{}
	for _, column := range actual.columns {
		primary[column.name] = column.primaryKey
	}
	if primary["workspace_id"] || !primary["id"] {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin legacy Audit primary-key migration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	table := s.TableIdentifier("_audit_events")
	workspace := s.Identifier("workspace_id")
	id := s.Identifier("id")
	switch strings.ToLower(strings.TrimSpace(s.Driver())) {
	case "sqlite", "sqlite3":
		legacyName := "_audit_events_legacy_global_key"
		legacy := s.TableIdentifier(legacyName)
		if _, err := tx.ExecContext(ctx, "ALTER TABLE "+table+" RENAME TO "+s.Identifier(legacyName)); err != nil {
			return fmt.Errorf("rename legacy Audit table: %w", err)
		}
		if _, err := tx.ExecContext(ctx, migration.Statements[0]); err != nil {
			return fmt.Errorf("create source-owned Audit table: %w", err)
		}
		columns := make([]string, 0, len(migration.Baseline.Tables[0].Columns))
		for _, column := range migration.Baseline.Tables[0].Columns {
			columns = append(columns, s.Identifier(column.Name))
		}
		projection := strings.Join(columns, ", ")
		if _, err := tx.ExecContext(ctx, "INSERT INTO "+table+" ("+projection+") SELECT "+projection+" FROM "+legacy); err != nil {
			return fmt.Errorf("copy legacy Audit rows: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "DROP TABLE "+legacy); err != nil {
			return fmt.Errorf("drop retired Audit table: %w", err)
		}
	case "postgres", "postgresql", "pgx":
		var constraint string
		queryValue := "SELECT constraint_name FROM information_schema.table_constraints WHERE table_schema = current_schema() AND table_name = '_audit_events' AND constraint_type = 'PRIMARY KEY'"
		if err := tx.QueryRowContext(ctx, queryValue).Scan(&constraint); err != nil {
			return fmt.Errorf("inspect legacy Audit primary key: %w", err)
		}
		statement, _ := legacyAuditPrimaryKeyReplacementSQL(s.Driver(), table, workspace, id, s.Identifier(constraint))
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("replace legacy Audit primary key: %w", err)
		}
	case "mysql":
		statement, _ := legacyAuditPrimaryKeyReplacementSQL(s.Driver(), table, workspace, id, "")
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("replace legacy Audit primary key: %w", err)
		}
	default:
		return fmt.Errorf("unsupported legacy Audit migration driver %q", s.Driver())
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit legacy Audit primary-key migration: %w", err)
	}
	return nil
}

func legacyAuditPrimaryKeyReplacementSQL(driver, table, workspace, id, constraint string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "postgres", "postgresql", "pgx":
		if strings.TrimSpace(constraint) == "" {
			return "", fmt.Errorf("Postgres legacy Audit primary-key constraint is empty")
		}
		return "ALTER TABLE " + table + " DROP CONSTRAINT " + constraint + ", ADD PRIMARY KEY (" + workspace + ", " + id + ")", nil
	case "mysql":
		return "ALTER TABLE " + table + " DROP PRIMARY KEY, ADD PRIMARY KEY (" + workspace + ", " + id + ")", nil
	default:
		return "", fmt.Errorf("unsupported legacy Audit primary-key replacement driver %q", driver)
	}
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
	queryValue := "SELECT " + s.identifier("checksum") + ", " + s.identifier("dirty") + " FROM " + s.tableIdentifier("_schema_migrations") + " WHERE " + s.identifier("path") + " = " + s.placeholder(1)
	if err := s.db.QueryRowContext(ctx, queryValue, runtimeSchemaMigrationPath(CurrentRuntimeSchemaVersion)).Scan(&checksum, &dirty); err != nil {
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
	EnsureApplicationSchema(context.Context, runtimeschema.Store) error
	EnsureEvidenceSchema(context.Context, runtimeschema.Store) error
	EnsureWorkflowProcessSchema(context.Context, runtimeschema.Store) error
	EnsureRateLimitSchema(context.Context, runtimeschema.Store) error
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

func (s *RuntimeStore) SchemaDB() runtimeschema.SQLDatabase {
	return s.schemaDatabase()
}

func (s *RuntimeStore) EnsureApplicationSchema(ctx context.Context) error {
	if s.schemaAssembler != nil {
		return s.schemaAssembler.EnsureApplicationSchema(ctx, s)
	}
	return runtimeschema.EnsureApplicationSchema(ctx, s)
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

func (s *RuntimeStore) EnsureRateLimitSchema(ctx context.Context) error {
	if s.schemaAssembler != nil {
		return s.schemaAssembler.EnsureRateLimitSchema(ctx, s)
	}
	return runtimeschema.EnsureRateLimitSchema(ctx, s)
}

func (s *RuntimeStore) EnsureLifecycleSchema(ctx context.Context) error {
	if s.schemaAssembler != nil {
		return s.schemaAssembler.EnsureLifecycleSchema(ctx, s)
	}
	return s.ensureLifecycleModule(ctx)
}

func (s *RuntimeStore) ensureLifecycleModule(ctx context.Context) error {
	binding, err := lifecyclemodule.NewFactory().OpenModule(ctx, lifecyclesdk.ApplicationRef{RuntimeID: "domainry-runtime-schema"}, runtimeLifecycleModuleHost{store: s})
	if err != nil {
		return err
	}
	return binding.Close(context.WithoutCancel(ctx))
}

func (s *RuntimeStore) ensureLifecycleModuleLocked(ctx context.Context) error {
	binding, err := lifecyclemodule.NewFactory().OpenModule(ctx, lifecyclesdk.ApplicationRef{RuntimeID: "domainry-runtime-schema"}, runtimeLifecycleModuleHost{store: s, locked: true})
	if err != nil {
		return err
	}
	return binding.Close(context.WithoutCancel(ctx))
}

type runtimeLifecycleModuleHost struct {
	store  *RuntimeStore
	locked bool
}

func (h runtimeLifecycleModuleHost) Database() lifecyclemodulehost.Database { return h.store.DB() }
func (h runtimeLifecycleModuleHost) Dialect() lifecyclemodulehost.Dialect {
	return h.store.RuntimeRenderer()
}
func (h runtimeLifecycleModuleHost) Migrations() lifecyclemodulehost.MigrationRegistrar {
	return runtimeLifecycleMigrationRegistrar{store: h.store, locked: h.locked}
}
func (h runtimeLifecycleModuleHost) Transactions() lifecyclemodulehost.Transactor {
	return runtimeLifecycleTransactor{database: h.store.DB()}
}

type runtimeLifecycleMigrationRegistrar struct {
	store  *RuntimeStore
	locked bool
}

func (r runtimeLifecycleMigrationRegistrar) ApplyOwnedMigrations(ctx context.Context, owner string, values []lifecyclemodulehost.SchemaMigration) error {
	if r.locked {
		return r.store.applyOwnedMigrationsLocked(ctx, owner, values)
	}
	return r.store.ApplyORMOwnedMigrations(ctx, owner, values)
}

type runtimeLifecycleTransactor struct{ database *sql.DB }

func (t runtimeLifecycleTransactor) WithinTransaction(ctx context.Context, operation func(context.Context, lifecyclemodulehost.DBTX) error) error {
	if t.database == nil || operation == nil {
		return fmt.Errorf("Lifecycle transaction requires database and operation")
	}
	tx, err := t.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := operation(lifecyclemodulehost.WithExecutor(ctx, tx), tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
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
	if err := s.ensureMigrationLedger(ctx); err != nil {
		return false, fmt.Errorf("prepare runtime schema migration ledger: %w", err)
	}
	path := runtimeSchemaMigrationPath(version)
	var count int
	if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+s.tableIdentifier("_schema_migrations")+" WHERE "+s.identifier("path")+" = "+s.placeholder(1), path).Scan(&count); err != nil {
		return false, fmt.Errorf("check runtime schema migration: %w", err)
	}
	if count == 0 {
		return true, nil
	}
	var checksum string
	var dirty bool
	if err := db.QueryRowContext(ctx, "SELECT "+s.identifier("checksum")+", "+s.identifier("dirty")+" FROM "+s.tableIdentifier("_schema_migrations")+" WHERE "+s.identifier("path")+" = "+s.placeholder(1), path).Scan(&checksum, &dirty); err != nil {
		return false, err
	}
	if dirty {
		return false, fmt.Errorf("migration.dirty: runtime schema %s", version)
	}
	if strings.TrimSpace(checksum) == "" {
		_, err := db.ExecContext(ctx, "UPDATE "+s.tableIdentifier("_schema_migrations")+" SET "+s.identifier("checksum")+" = "+s.placeholder(1)+" WHERE "+s.identifier("path")+" = "+s.placeholder(2), currentRuntimeSchemaChecksum(), path)
		return false, err
	}
	if checksum != currentRuntimeSchemaChecksum() {
		return false, fmt.Errorf("migration.checksum_drift: runtime schema %s", version)
	}
	return false, nil
}

func (s *RuntimeStore) startRuntimeSchemaMigration(ctx context.Context, version string) error {
	columns := []string{"path", "version", "name", "kind", "checksum", "dirty", "applied_at", "runtime_version", "duration_ms", "operator", "instance_id", "backup_id"}
	queryValue := "INSERT INTO " + s.tableIdentifier("_schema_migrations") + " (" + strings.Join(quotedColumns(s, columns), ", ") + ") VALUES (" + strings.Join(placeholders(s, len(columns)), ", ") + ")"
	_, err := s.schemaDatabase().ExecContext(ctx, queryValue, runtimeSchemaMigrationPath(version), version, "managed_database_cohort", "runtime_schema", currentRuntimeSchemaChecksum(), true, time.Now().UTC().Format(time.RFC3339), s.config.RuntimeVersion, 0, migrationOperator(s.config), migrationInstanceID(s.config), s.migrationBackupID)
	return err
}

func (s *RuntimeStore) recordRuntimeSchemaMigration(ctx context.Context, version string, duration time.Duration) error {
	_, err := s.schemaDatabase().ExecContext(ctx, "UPDATE "+s.tableIdentifier("_schema_migrations")+" SET "+s.identifier("dirty")+" = FALSE, "+s.identifier("duration_ms")+" = "+s.placeholder(1)+", "+s.identifier("applied_at")+" = "+s.placeholder(2)+" WHERE "+s.identifier("path")+" = "+s.placeholder(3), duration.Milliseconds(), time.Now().UTC().Format(time.RFC3339), runtimeSchemaMigrationPath(version))
	if err != nil {
		return fmt.Errorf("record runtime schema migration: %w", err)
	}
	return nil
}

func runtimeSchemaMigrationPath(version string) string {
	return "runtime_schema_" + strings.TrimSpace(version)
}

func (s *RuntimeStore) removeObsoleteMigrationLedgers(ctx context.Context) error {

	for _, table := range []string{"_schema_materializations", "_runtime_schema_migrations", "_party_schema_migrations"} {
		if _, err := s.schemaDatabase().ExecContext(ctx, "DROP TABLE IF EXISTS "+s.tableIdentifier(table)); err != nil {
			return fmt.Errorf("remove obsolete migration ledger %s: %w", table, err)
		}
	}
	return nil
}

func currentRuntimeSchemaChecksum() string {
	sum := sha256.Sum256([]byte(CurrentRuntimeSchemaVersion + ":metadata_projection,object_fields,record_data,evidence,party,lifecycle,operations,indexes,_release_cohorts,_release_instances,managed_database_cohort,external_identity_ownership,rate_limit,module_migrations,workspace_provisioning,tenant_initialization"))
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
	insert, arguments, err := query.NewInsertBuilder(s.sqlBase().SQLRenderer, managedDatabaseCohortTable).
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
	queryValue := "SELECT " + s.identifier("contract_version") + ", " + s.identifier("database_identity_sha256") + " FROM " + s.tableIdentifier(managedDatabaseCohortTable) + " WHERE " + s.identifier("marker_id") + " = " + s.placeholder(1)
	if err := database.QueryRowContext(ctx, queryValue, 1).Scan(&contractVersion, &identity); err != nil {
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
