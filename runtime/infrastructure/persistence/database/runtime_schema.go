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
	sharedartifact "github.com/domainry/domainry-foundation/artifact"
	sharedoperation "github.com/domainry/domainry-foundation/operation"
	sharedworkerscope "github.com/domainry/domainry-foundation/workerscope"
	ormmigration "github.com/domainry/domainry-orm/migration"
	"github.com/domainry/domainry-orm/query"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/base"
	runtimeschema "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/schema"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

const CurrentRuntimeSchemaVersion = "033_foundation_worker_scope_kernel"

type RuntimeSchemaCapabilities struct {
	Workflow            bool
	Automation          bool
	Uploads             bool
	Lifecycle           bool
	ReleaseCoordination bool
}

func (capabilities RuntimeSchemaCapabilities) IncludesTable(table string) bool {
	switch strings.TrimSpace(table) {
	case "_workflow_executions", "_workflow_process_instances", "_workflow_node_instances", "_workflow_tasks", "_workflow_process_events", "_workflow_route_steps":
		return capabilities.Workflow
	case "_automation_runs":
		return capabilities.Automation
	case "_release_cohorts", "_release_instances":
		return capabilities.ReleaseCoordination
	default:
		return true
	}
}

func FullRuntimeSchemaCapabilities() RuntimeSchemaCapabilities {
	return RuntimeSchemaCapabilities{Workflow: true, Automation: true, Uploads: true, Lifecycle: true, ReleaseCoordination: true}
}

const (
	managedDatabaseCohortTable           = "_domainry_managed_runtime_database_cohort"
	managedDatabaseCohortContractVersion = "domainry-managed-runtime-database-cohort-v1"
)

func SupportedRuntimeSchemaUpgradeVersions() []string {
	return []string{"001_connector_runtime_lifecycle", "002_data_lifecycle_governance", "003_operations_reliability", "004_runtime_release_cohort", "007_identity_global_names", "008_identity_account_projection", "009_managed_database_cohort", "010_external_identity_ownership", "011_notification_service_publication_outbox", "012_rate_limit_schema_owner", "013_agent_schema_owner", "021_workspace_only_foundation", "024_application_time_zone", "026_workflow_route_steps", "028_subject_execution_evidence"}
}

func (s *RuntimeStore) EnsureRuntimeSchema(ctx context.Context) error {
	return s.EnsureRuntimeSchemaFor(ctx, FullRuntimeSchemaCapabilities())
}

func (s *RuntimeStore) EnsureRuntimeSchemaFor(ctx context.Context, capabilities RuntimeSchemaCapabilities) error {
	s.runtimeCapabilities = capabilities
	s.runtimeCapabilitiesSelected = true
	if s.migrationDB != nil {
		migrationStore := s.runtimeMigrationStore()
		if err := migrationStore.EnsureRuntimeSchemaFor(ctx, capabilities); err != nil {
			return err
		}
		return nil
	}
	if s.config.EffectiveDatabaseMigrationMode() == "verify" {
		if err := s.verifyRuntimeSchemaFor(ctx, capabilities); err != nil {
			return err
		}
		if err := s.verifyManagedDatabaseCohortMarker(ctx); err != nil {
			return err
		}
		if err := s.ensureOperationsKernel(ctx); err != nil {
			return err
		}
		if err := s.ensureArtifactKernel(ctx); err != nil {
			return err
		}
		return s.ensureWorkerScopeKernel(ctx)
	}
	if s.schemaAssembler == nil {
		if err := s.ensureWorkerScopeKernel(ctx); err != nil {
			return err
		}
		if err := s.ensureOperationsKernel(ctx); err != nil {
			return err
		}
		if err := s.ensureArtifactKernel(ctx); err != nil {
			return err
		}
	}
	release, err := s.acquireMigrationLock(ctx, s.config)
	if err != nil {
		return err
	}
	defer release()
	startedAt := time.Now()
	checksum := currentRuntimeSchemaChecksum(capabilities)
	pending, err := s.runtimeSchemaMigrationPendingFor(ctx, CurrentRuntimeSchemaVersion, checksum)
	if err != nil {
		return err
	}
	// A clean receipt for the current schema version is the authority that the
	// Runtime-owned schema was fully materialized. Re-running every table,
	// column, and index probe on each process start turns remote-database latency
	// into minutes of serialized startup work and bypasses the migration ledger's
	// purpose. A new schema contract changes the version/checksum and takes the
	// full path below.
	if !pending {
		return nil
	}
	if err := s.ValidateLegacyWorkspaceScopes(ctx); err != nil {
		return err
	}
	if err := s.ensureMigrationBackupForExistingData(ctx, s.runtimeMigrationConfig()); err != nil {
		return err
	}
	if err := s.startRuntimeSchemaMigrationFor(ctx, CurrentRuntimeSchemaVersion, checksum); err != nil {
		return err
	}
	if err := s.ensureManagedDatabaseCohortMarker(ctx); err != nil {
		return err
	}
	if err := s.ensureWorkspaceV3AuthorityColumns(ctx); err != nil {
		return err
	}
	if err := runtimeschema.EnsureWorkspaceProvisioningSchema(ctx, s); err != nil {
		return err
	}
	if err := s.EnsureApplicationSchemaFor(ctx, capabilities); err != nil {
		return err
	}
	if err := s.ensureEvidenceSchemaFor(ctx, capabilities); err != nil {
		return err
	}
	if err := s.ensureAuditModuleSchemaLocked(ctx); err != nil {
		return err
	}
	// The MySQL evidence profile normalizes cursor columns owned by the Audit
	// module, so the source-owned Audit table must exist before normalization.
	if err := s.RuntimeProfile().NormalizeEvidenceSchema(ctx, s.schemaDatabase(), s.RuntimeRenderer()); err != nil {
		return err
	}
	if capabilities.Workflow {
		if err := s.EnsureWorkflowProcessSchema(ctx); err != nil {
			return err
		}
	}
	if err := s.EnsureRateLimitSchema(ctx); err != nil {
		return err
	}
	if err := s.recordRuntimeSchemaMigration(ctx, CurrentRuntimeSchemaVersion, time.Since(startedAt)); err != nil {
		return err
	}
	return nil
}

func (s *RuntimeStore) ensureWorkspaceV3AuthorityColumns(ctx context.Context) error {
	exists, err := s.RuntimeTableExists(ctx, "_workspaces")
	if errors.Is(err, sql.ErrNoRows) {
		exists, err = false, nil
	}
	if err != nil || !exists {
		return err
	}
	if err := s.ensureRuntimeColumn(ctx, "_workspaces", "initial_installation_identity", "TEXT NULL"); err != nil {
		return err
	}
	return s.ensureRuntimeColumn(ctx, "_workspaces", "revision", "INTEGER NOT NULL DEFAULT 1")
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
		SQLDatabase:                 base.NewSQLDatabase(s.migrationDB, s.engine, s.databaseSchema),
		db:                          s.migrationDB,
		engine:                      s.engine,
		config:                      s.config,
		databaseSchema:              s.databaseSchema,
		postgresProfile:             s.postgresProfile,
		postgresCapabilities:        s.postgresCapabilities,
		migratorCapabilities:        s.migratorCapabilities,
		expectedMigrations:          s.expectedMigrations,
		expectedChecksums:           s.expectedChecksums,
		secretMaterialKey:           s.secretMaterialKey,
		secretKeyProvider:           s.secretKeyProvider,
		migrationBackupReady:        s.migrationBackupReady,
		migrationCompatible:         s.migrationCompatible,
		migrationBackupID:           s.migrationBackupID,
		idempotencyMetrics:          s.idempotencyMetrics,
		sqlMetrics:                  s.sqlMetrics,
		operationalMetrics:          s.operationalMetrics,
		workerScopeCursor:           s.workerScopeCursor,
		workerScopes:                sharedworkerscope.NewStore(s.migrationDB, s.RuntimeRenderer()),
		workerWakeups:               s.workerWakeups,
		schemaAssembler:             s.schemaAssembler,
		backupChecksum:              s.backupChecksum,
		migrationReadDir:            s.migrationReadDir,
		runtimeCapabilities:         s.runtimeCapabilities,
		runtimeCapabilitiesSelected: s.runtimeCapabilitiesSelected,
	}
}

func (s *RuntimeStore) RuntimeSchemaCapabilities() RuntimeSchemaCapabilities {
	if s == nil || !s.runtimeCapabilitiesSelected {
		return FullRuntimeSchemaCapabilities()
	}
	return s.runtimeCapabilities
}

func (s *RuntimeStore) verifyRuntimeSchema(ctx context.Context) error {
	return s.verifyRuntimeSchemaFor(ctx, FullRuntimeSchemaCapabilities())
}

func (s *RuntimeStore) verifyRuntimeSchemaFor(ctx context.Context, capabilities RuntimeSchemaCapabilities) error {
	var checksum string
	var dirty bool
	queryValue := "SELECT " + s.identifier("checksum") + ", " + s.identifier("dirty") + " FROM " + s.tableIdentifier("_schema_migrations") + " WHERE " + s.identifier("path") + " = " + s.placeholder(1)
	if err := s.db.QueryRowContext(ctx, queryValue, runtimeSchemaMigrationPath(CurrentRuntimeSchemaVersion)).Scan(&checksum, &dirty); err != nil {
		return fmt.Errorf("verify runtime schema compatibility: %w", err)
	}
	if dirty {
		return fmt.Errorf("migration.dirty: runtime schema %s", CurrentRuntimeSchemaVersion)
	}
	if checksum != currentRuntimeSchemaChecksum(capabilities) {
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

func (s *RuntimeStore) EnsureApplicationSchemaFor(ctx context.Context, capabilities RuntimeSchemaCapabilities) error {
	if s.schemaAssembler != nil {
		if capabilities != FullRuntimeSchemaCapabilities() {
			return fmt.Errorf("capability-selected Runtime schema does not support a custom schema assembler")
		}
		return s.schemaAssembler.EnsureApplicationSchema(ctx, s)
	}
	return runtimeschema.EnsureApplicationSchemaFor(ctx, s, capabilities.Lifecycle)
}

func (s *RuntimeStore) EnsureEvidenceSchema(ctx context.Context) error {
	if s.schemaAssembler == nil {
		if err := s.ensureWorkerScopeKernel(ctx); err != nil {
			return err
		}
		if err := s.ensureOperationsKernel(ctx); err != nil {
			return err
		}
		if err := s.ensureArtifactKernel(ctx); err != nil {
			return err
		}
	}
	return s.ensureEvidenceSchemaFor(ctx, FullRuntimeSchemaCapabilities())
}

func (s *RuntimeStore) ensureArtifactKernel(ctx context.Context) error {
	if s == nil || s.DB() == nil {
		return fmt.Errorf("Runtime Artifact persistence host is incomplete")
	}
	if _, err := sharedartifact.Open(ctx, s.DB(), s.RuntimeRenderer(), s); err != nil {
		return fmt.Errorf("open Runtime Artifact persistence: %w", err)
	}
	return nil
}

func (s *RuntimeStore) ArtifactStore() *sharedartifact.SQLStore {
	if s == nil {
		return nil
	}
	return sharedartifact.NewSQLStore(s.DB(), s.RuntimeRenderer())
}

func (s *RuntimeStore) ensureOperationsKernel(ctx context.Context) error {
	if s == nil || s.DB() == nil {
		return fmt.Errorf("Runtime Operations persistence host is incomplete")
	}
	if _, err := sharedoperation.Open(ctx, s.DB(), s.RuntimeRenderer(), s); err != nil {
		return fmt.Errorf("open Runtime Operations persistence: %w", err)
	}
	return nil
}

func (s *RuntimeStore) ensureWorkerScopeKernel(ctx context.Context) error {
	if s == nil || s.DB() == nil {
		return fmt.Errorf("Runtime Worker Scope persistence host is incomplete")
	}
	store, err := sharedworkerscope.Open(ctx, s.DB(), s.RuntimeRenderer(), s)
	if err != nil {
		return fmt.Errorf("open Runtime Worker Scope persistence: %w", err)
	}
	s.workerScopes = store
	return nil
}

func (s *RuntimeStore) ensureEvidenceSchemaFor(ctx context.Context, capabilities RuntimeSchemaCapabilities) error {
	if s.schemaAssembler != nil {
		if capabilities != FullRuntimeSchemaCapabilities() {
			return fmt.Errorf("capability-selected Runtime schema does not support a custom schema assembler")
		}
		return s.schemaAssembler.EnsureEvidenceSchema(ctx, s)
	}
	return runtimeschema.EnsureEvidenceSchemaFor(ctx, s, runtimeschema.EvidenceSchemaCapabilities{
		Workflow: capabilities.Workflow, Automation: capabilities.Automation, Lifecycle: capabilities.Lifecycle, ReleaseCoordination: capabilities.ReleaseCoordination,
	})
}

func (s *RuntimeStore) EnsureEvidenceSchemaFor(ctx context.Context, capabilities RuntimeSchemaCapabilities) error {
	if s.schemaAssembler == nil {
		if err := s.ensureOperationsKernel(ctx); err != nil {
			return err
		}
		if err := s.ensureArtifactKernel(ctx); err != nil {
			return err
		}
	}
	return s.ensureEvidenceSchemaFor(ctx, capabilities)
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
	return s.runtimeSchemaMigrationPendingFor(ctx, version, currentRuntimeSchemaChecksum())
}

func (s *RuntimeStore) runtimeSchemaMigrationPendingFor(ctx context.Context, version, expectedChecksum string) (bool, error) {
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
		_, err := db.ExecContext(ctx, "UPDATE "+s.tableIdentifier("_schema_migrations")+" SET "+s.identifier("checksum")+" = "+s.placeholder(1)+" WHERE "+s.identifier("path")+" = "+s.placeholder(2), expectedChecksum, path)
		return false, err
	}
	if checksum != expectedChecksum {
		return false, fmt.Errorf("migration.checksum_drift: runtime schema %s", version)
	}
	return false, nil
}

func (s *RuntimeStore) startRuntimeSchemaMigration(ctx context.Context, version string) error {
	return s.startRuntimeSchemaMigrationFor(ctx, version, currentRuntimeSchemaChecksum())
}

func (s *RuntimeStore) startRuntimeSchemaMigrationFor(ctx context.Context, version, checksum string) error {
	columns := []string{"path", "version", "name", "kind", "checksum", "dirty", "applied_at", "runtime_version", "duration_ms", "operator", "instance_id", "backup_id"}
	queryValue := "INSERT INTO " + s.tableIdentifier("_schema_migrations") + " (" + strings.Join(quotedColumns(s, columns), ", ") + ") VALUES (" + strings.Join(placeholders(s, len(columns)), ", ") + ")"
	_, err := s.schemaDatabase().ExecContext(ctx, queryValue, runtimeSchemaMigrationPath(version), version, "managed_database_cohort", "runtime_schema", checksum, true, time.Now().UTC().Format(time.RFC3339), s.config.RuntimeVersion, 0, migrationOperator(s.config), migrationInstanceID(s.config), s.migrationBackupID)
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

func currentRuntimeSchemaChecksum(selected ...RuntimeSchemaCapabilities) string {
	capabilities := FullRuntimeSchemaCapabilities()
	if len(selected) != 0 {
		capabilities = selected[0]
	}
	capabilityIdentity := fmt.Sprintf("workflow=%t,automation=%t,uploads=%t,lifecycle=%t,release_coordination=%t", capabilities.Workflow, capabilities.Automation, capabilities.Uploads, capabilities.Lifecycle, capabilities.ReleaseCoordination)
	sum := sha256.Sum256([]byte(CurrentRuntimeSchemaVersion + ":project_model_projection,object_fields,record_data,evidence,lifecycle,operations,foundation_operations_kernel,foundation_artifact_kernel,indexes,_release_cohorts,_release_instances,managed_database_cohort,external_identity_ownership,rate_limit,module_migrations,workspace_only_provisioning,workspace_commercial_configuration_in_aggregate,workspace_provisioning_in_operations,workspace_administration_in_operations,report_export_prepare_in_operations,dispatch_callbacks_in_operations,upload_subject_bindings_in_artifact_bindings,database_retirements_in_operations,workflow_route_steps,subject_execution_evidence_fences_receipts,native_capabilities=" + capabilityIdentity))
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

// InstallationIdentity returns a stable identity for the host-owned database
// installation. It is neither a Workspace identifier nor legacy tenant
// registry state. Managed server databases use their persisted random cohort
// marker; SQLite installations use the stable absolute database location
// because the managed marker is intentionally disabled for that engine.
func (s *RuntimeStore) InstallationIdentity(ctx context.Context) (string, error) {
	if s == nil {
		return "", fmt.Errorf("Runtime installation identity store is required")
	}
	if !s.sqlBase().RuntimeEngine.ManagedDatabaseMarkerEnabled() {
		path := strings.TrimSpace(s.config.DBPath)
		if path == "" || path == ":memory:" {
			return "", fmt.Errorf("stable SQLite installation path is required")
		}
		absolute, err := filepath.Abs(path)
		if err != nil {
			return "", fmt.Errorf("resolve SQLite installation identity: %w", err)
		}
		sum := sha256.Sum256([]byte("domainry-runtime/sqlite-installation/v1\x00" + filepath.Clean(absolute)))
		return hex.EncodeToString(sum[:]), nil
	}
	statement, arguments, err := query.NewSelectBuilder(s.sqlBase().SQLRenderer, managedDatabaseCohortTable).
		Columns("contract_version", "database_identity_sha256").Where(query.Equal("marker_id", 1)).Build()
	if err != nil {
		return "", fmt.Errorf("build Runtime installation identity query: %w", err)
	}
	var contractVersion, identity string
	if err := s.db.QueryRowContext(ctx, statement, arguments...).Scan(&contractVersion, &identity); err != nil {
		return "", fmt.Errorf("load Runtime installation identity: %w", err)
	}
	identity = strings.TrimSpace(identity)
	if contractVersion != managedDatabaseCohortContractVersion || len(identity) != 64 {
		return "", fmt.Errorf("Runtime installation identity is invalid")
	}
	if _, err := hex.DecodeString(identity); err != nil {
		return "", fmt.Errorf("Runtime installation identity is invalid")
	}
	return identity, nil
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
