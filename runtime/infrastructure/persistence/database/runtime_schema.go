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

	sharedartifact "github.com/domainry/domainry-foundation/artifact"
	sharedoperation "github.com/domainry/domainry-foundation/operation"
	sharedworkerscope "github.com/domainry/domainry-foundation/workerscope"
	"github.com/domainry/domainry-orm/query"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/base"
	runtimeschema "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/schema"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

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
	managedDatabaseCohortContractVersion = "domainry-managed-runtime-database-cohort-v1"
)

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
	definition, err := runtimeSchemaDDL(ctx, s, capabilities)
	if err != nil {
		return err
	}
	checksum := runtimeSchemaChecksum(definition)
	pending, err := s.runtimeSchemaMigrationPendingFor(ctx, checksum)
	if err != nil {
		return err
	}
	// A clean receipt for this exact DDL checksum is the authority that the
	// Runtime-owned schema was fully materialized. Re-running every table,
	// column, and index probe on each process start turns remote-database latency
	// into minutes of serialized startup work and bypasses the migration ledger's
	// purpose. Changed canonical DDL produces a new content-addressed receipt and
	// takes the full path below.
	if !pending {
		return nil
	}
	if err := s.startRuntimeSchemaMigration(ctx, checksum); err != nil {
		return err
	}
	if err := s.ensureManagedDatabaseCohortMarker(ctx); err != nil {
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
	if capabilities.Workflow {
		if err := s.EnsureWorkflowProcessSchema(ctx); err != nil {
			return err
		}
	}
	if err := s.EnsureRateLimitSchema(ctx); err != nil {
		return err
	}
	if err := s.recordRuntimeSchemaMigration(ctx, checksum, time.Since(startedAt)); err != nil {
		return err
	}
	return nil
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
	definition, err := runtimeSchemaDDL(ctx, s, capabilities)
	if err != nil {
		return err
	}
	expectedChecksum := runtimeSchemaChecksum(definition)
	pending, err := s.runtimeSchemaReceiptPending(ctx, s.db, expectedChecksum)
	if err != nil {
		return fmt.Errorf("verify runtime schema compatibility: %w", err)
	}
	if pending {
		return fmt.Errorf("migration.pending: runtime schema %s", expectedChecksum)
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

func (s *RuntimeStore) runtimeSchemaMigrationPending(ctx context.Context, expectedChecksum string) (bool, error) {
	return s.runtimeSchemaMigrationPendingFor(ctx, expectedChecksum)
}

func (s *RuntimeStore) runtimeSchemaMigrationPendingFor(ctx context.Context, expectedChecksum string) (bool, error) {
	db := s.schemaDatabase()
	if err := s.ensureMigrationLedger(ctx); err != nil {
		return false, fmt.Errorf("prepare runtime schema migration ledger: %w", err)
	}
	return s.runtimeSchemaReceiptPending(ctx, db, expectedChecksum)
}

func (s *RuntimeStore) runtimeSchemaReceiptPending(ctx context.Context, db schemaDatabase, expectedChecksum string) (bool, error) {
	expectedPath := runtimeSchemaMigrationPath(expectedChecksum)
	rows, err := db.QueryContext(ctx, "SELECT "+s.identifier("path")+", "+s.identifier("checksum")+", "+s.identifier("dirty")+" FROM "+s.tableIdentifier("_schema_migrations")+" WHERE "+s.identifier("kind")+" = "+s.placeholder(1), "runtime_schema")
	if err != nil {
		return false, fmt.Errorf("check runtime schema migration: %w", err)
	}
	defer rows.Close()
	found := false
	for rows.Next() {
		var path, checksum string
		var dirty bool
		if err := rows.Scan(&path, &checksum, &dirty); err != nil {
			return false, fmt.Errorf("scan runtime schema migration: %w", err)
		}
		if dirty {
			return false, fmt.Errorf("migration.dirty: runtime schema %s", path)
		}
		if path != expectedPath {
			continue
		}
		if checksum != expectedChecksum {
			return false, fmt.Errorf("migration.checksum_drift: runtime schema %s", path)
		}
		found = true
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("read runtime schema migration: %w", err)
	}
	return !found, nil
}

func (s *RuntimeStore) startRuntimeSchemaMigration(ctx context.Context, checksum string) error {
	columns := []string{"path", "version", "name", "kind", "checksum", "dirty", "applied_at", "runtime_version", "duration_ms", "operator", "instance_id", "backup_id"}
	queryValue := "INSERT INTO " + s.tableIdentifier("_schema_migrations") + " (" + strings.Join(quotedColumns(s, columns), ", ") + ") VALUES (" + strings.Join(placeholders(s, len(columns)), ", ") + ")"
	_, err := s.schemaDatabase().ExecContext(ctx, queryValue, runtimeSchemaMigrationPath(checksum), "", "runtime_schema", "runtime_schema", checksum, true, time.Now().UTC().UnixMilli(), s.config.RuntimeVersion, 0, migrationOperator(s.config), migrationInstanceID(s.config), s.migrationBackupID)
	return err
}

func (s *RuntimeStore) recordRuntimeSchemaMigration(ctx context.Context, checksum string, duration time.Duration) error {
	_, err := s.schemaDatabase().ExecContext(ctx, "UPDATE "+s.tableIdentifier("_schema_migrations")+" SET "+s.identifier("dirty")+" = FALSE, "+s.identifier("duration_ms")+" = "+s.placeholder(1)+", "+s.identifier("applied_at")+" = "+s.placeholder(2)+" WHERE "+s.identifier("path")+" = "+s.placeholder(3)+" AND "+s.identifier("checksum")+" = "+s.placeholder(4), duration.Milliseconds(), time.Now().UTC().UnixMilli(), runtimeSchemaMigrationPath(checksum), checksum)
	if err != nil {
		return fmt.Errorf("record runtime schema migration: %w", err)
	}
	return nil
}

func (s *RuntimeStore) ensureManagedDatabaseCohortMarker(ctx context.Context) error {
	if !s.sqlBase().RuntimeEngine.ManagedDatabaseMarkerEnabled() {
		return nil
	}
	database := s.schemaDatabase()
	statement, err := runtimeschema.ManagedDatabaseCohortCreateStatement(s.sqlBase().SQLRenderer)
	if err != nil {
		return fmt.Errorf("build managed database cohort marker: %w", err)
	}
	if _, err := database.ExecContext(ctx, statement); err != nil {
		return fmt.Errorf("prepare managed database cohort marker: %w", err)
	}
	seed := make([]byte, 32)
	if _, err := rand.Read(seed); err != nil {
		return fmt.Errorf("generate managed database cohort marker: %w", err)
	}
	identity := sha256.Sum256(seed)
	insert, arguments, err := query.NewInsertBuilder(s.sqlBase().SQLRenderer, runtimeschema.ManagedDatabaseCohortTable).
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
	queryValue := "SELECT " + s.identifier("contract_version") + ", " + s.identifier("database_identity_sha256") + " FROM " + s.tableIdentifier(runtimeschema.ManagedDatabaseCohortTable) + " WHERE " + s.identifier("marker_id") + " = " + s.placeholder(1)
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
	statement, arguments, err := query.NewSelectBuilder(s.sqlBase().SQLRenderer, runtimeschema.ManagedDatabaseCohortTable).
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

func (s *RuntimeStore) runtimeColumnDefinition(definition string) string {
	return s.sqlBase().RuntimeEngine.ColumnDefinition(definition)
}
