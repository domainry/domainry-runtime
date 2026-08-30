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
	notificationmodulehost "github.com/domainry/domainry-notification-sdk/modulehost"
	ormbuilder "github.com/domainry/domainry-orm/builder"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/base"
	runtimeschema "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/schema"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

const CurrentRuntimeSchemaVersion = "013_agent_schema_owner"

const (
	managedDatabaseCohortTable           = "_domainry_managed_runtime_database_cohort"
	managedDatabaseCohortContractVersion = "domainry-managed-runtime-database-cohort-v1"
)

func SupportedRuntimeSchemaUpgradeVersions() []string {
	return []string{"001_connector_runtime_lifecycle", "002_data_lifecycle_governance", "003_operations_reliability", "004_runtime_release_cohort", "005_identity_workforce_separation", "006_party_foundation", "007_identity_global_names", "008_identity_account_directory", "009_managed_database_cohort", "010_external_identity_ownership", "011_notification_service_publication_outbox", "012_rate_limit_schema_owner"}
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
	if err := s.EnsureAgentSchema(ctx); err != nil {
		return err
	}
	if err := s.EnsureRateLimitSchema(ctx); err != nil {
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

func (s *RuntimeStore) ensureAuditModuleSchemaLocked(ctx context.Context) error {
	exists, err := s.RuntimeTableExists(ctx, "_audit_events")
	if errors.Is(err, sql.ErrNoRows) {
		exists, err = false, nil
	}
	if err != nil {
		return fmt.Errorf("inspect legacy Audit schema: %w", err)
	}
	if exists {
		if err := s.EnsureRuntimeColumn(ctx, "_audit_events", "workspace_id", s.ApplicationSchemaIDColumnType()+" NOT NULL DEFAULT 'default'"); err != nil {
			return fmt.Errorf("prepare legacy Audit workspace ownership: %w", err)
		}
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
	values := make([]notificationmodulehost.SchemaMigration, len(migrations))
	for index, migration := range migrations {
		values[index] = notificationmodulehost.SchemaMigration{Version: migration.Version, Name: migration.Name, Statements: append([]string(nil), migration.Statements...)}
		if migration.Baseline != nil {
			baseline := notificationmodulehost.SchemaBaseline{Tables: make([]notificationmodulehost.SchemaTable, len(migration.Baseline.Tables))}
			for tableIndex, table := range migration.Baseline.Tables {
				baseline.Tables[tableIndex] = notificationmodulehost.SchemaTable{Name: table.Name, Columns: make([]notificationmodulehost.SchemaColumn, len(table.Columns)), Indexes: make([]notificationmodulehost.SchemaIndex, len(table.Indexes))}
				for columnIndex, column := range table.Columns {
					baseline.Tables[tableIndex].Columns[columnIndex] = notificationmodulehost.SchemaColumn{Name: column.Name, Type: column.Type, Nullable: column.Nullable, PrimaryKey: column.PrimaryKey}
				}
				for indexIndex, item := range table.Indexes {
					baseline.Tables[tableIndex].Indexes[indexIndex] = notificationmodulehost.SchemaIndex{Name: item.Name, Unique: item.Unique, Columns: append([]string(nil), item.Columns...)}
				}
			}
			values[index].Baseline = &baseline
		}
	}
	return s.applyOwnedMigrationsLocked(ctx, "audit", values)
}

// migrateLegacyAuditPrimaryKey retires the pre-module global Audit key before
// the source-owned migration registrar proves the Audit baseline. The ORM has
// no cross-dialect primary-key alteration or INSERT...SELECT schema-rebuild
// equivalent, so this bounded DDL is intentionally local to migration code.
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
		query := "SELECT constraint_name FROM information_schema.table_constraints WHERE table_schema = current_schema() AND table_name = '_audit_events' AND constraint_type = 'PRIMARY KEY'"
		if err := tx.QueryRowContext(ctx, query).Scan(&constraint); err != nil {
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
	EnsureApplicationSchema(context.Context, runtimeschema.Store) error
	EnsureEvidenceSchema(context.Context, runtimeschema.Store) error
	EnsureWorkflowProcessSchema(context.Context, runtimeschema.Store) error
	EnsureAgentSchema(context.Context, runtimeschema.Store) error
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

// SchemaDB returns the advisory-lock-owning migration connection when schema
// assembly is running, so a one-connection migrator pool cannot self-deadlock.
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

func (s *RuntimeStore) EnsureAgentSchema(ctx context.Context) error {
	if s.schemaAssembler != nil {
		return s.schemaAssembler.EnsureAgentSchema(ctx, s)
	}
	return runtimeschema.EnsureAgentSchema(ctx, s)
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
	sum := sha256.Sum256([]byte(CurrentRuntimeSchemaVersion + ":metadata,object_fields,record_data,evidence,party,lifecycle,operations,indexes,report_snapshots,runtime_release_cohorts,runtime_release_instances,managed_database_cohort,external_identity_ownership,rate_limit,agent"))
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
