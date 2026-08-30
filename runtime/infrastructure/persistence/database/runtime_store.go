package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/secrets"
	"github.com/domainry/domainry-foundation/telemetry"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	metadatarepository "github.com/domainry/domainry-metadata-sdk/repository"
	"github.com/domainry/domainry-notification-sdk/modulehost"
	ormbuilder "github.com/domainry/domainry-orm/query"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/base"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/postgres"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

// RuntimeStore owns the Runtime database connection and selected engine.
type RuntimeStore struct {
	*base.SQLDatabase
	db                   *sql.DB
	migrationDB          *sql.DB
	migrationConn        *sql.Conn
	engine               databaseEngine
	config               config.Config
	databaseSchema       string
	postgresProfile      *postgres.ConnectionProfile
	postgresCapabilities postgres.Capabilities
	migratorCapabilities postgres.Capabilities
	expectedMigrations   []string
	expectedChecksums    map[string]string
	secretMaterialKey    [32]byte
	secretKeyProvider    secrets.KeyProvider
	migrationBackupReady bool
	migrationCompatible  bool
	migrationBackupID    string
	idempotencyMetrics   *idempotency.MemoryMetricsCollector
	sqlMetrics           *telemetry.SQLMetrics
	operationalMetrics   *RuntimeOperationalMetrics
	workspaceRLS         WorkspaceRLSStatus
	workerScopeCursor    *runtimeWorkerScopeCursor
	workerWakeupsMu      sync.Mutex
	workerWakeups        *workerplatform.WakeupBroker
	notificationMu       sync.RWMutex
	notificationTx       modulehost.TransactionalPublisher
	notificationSaaS     *NotificationSaaSPublicationScope
	schemaAssembler      runtimeSchemaAssembler
	backupChecksum       func(string) (string, error)
	migrationReadDir     func(string) ([]os.DirEntry, error)
	metadataDefinitions  metadatarepository.DefinitionRepository
}

func (s *RuntimeStore) MetadataDefinitions() metadatarepository.DefinitionRepository {
	if s == nil {
		return nil
	}
	return s.metadataDefinitions
}

type NotificationSaaSPublicationScope struct {
	TenantID, WorkspaceID, ApplicationKey string
}

func (s *RuntimeStore) BindNotificationSaaSPublications(scope NotificationSaaSPublicationScope) error {
	if s == nil || strings.TrimSpace(scope.TenantID) == "" || strings.TrimSpace(scope.WorkspaceID) == "" || strings.TrimSpace(scope.ApplicationKey) == "" {
		return fmt.Errorf("Notification SaaS publication scope is required")
	}
	s.notificationMu.Lock()
	defer s.notificationMu.Unlock()
	if s.notificationTx != nil || s.notificationSaaS != nil {
		return fmt.Errorf("Notification publication transaction boundary is already bound")
	}
	value := scope
	s.notificationSaaS = &value
	return nil
}

func (s *RuntimeStore) NotificationSaaSPublications() (NotificationSaaSPublicationScope, bool) {
	if s == nil {
		return NotificationSaaSPublicationScope{}, false
	}
	s.notificationMu.RLock()
	defer s.notificationMu.RUnlock()
	if s.notificationSaaS == nil {
		return NotificationSaaSPublicationScope{}, false
	}
	return *s.notificationSaaS, true
}

// BindNotificationTransactions installs the embedded Notification transaction
// capability after the Module Binding is opened. The store owns neither the
// capability nor its lifecycle; it only makes the exact publisher available
// to producer-owned persistence adapters that already share this database.
func (s *RuntimeStore) BindNotificationTransactions(publisher modulehost.TransactionalPublisher) error {
	if s == nil || publisher == nil {
		return fmt.Errorf("Notification transaction publisher is required")
	}
	s.notificationMu.Lock()
	defer s.notificationMu.Unlock()
	if s.notificationTx != nil || s.notificationSaaS != nil {
		return fmt.Errorf("Notification transaction publisher is already bound")
	}
	s.notificationTx = publisher
	return nil
}

func (s *RuntimeStore) NotificationTransactions() modulehost.TransactionalPublisher {
	if s == nil {
		return nil
	}
	s.notificationMu.RLock()
	defer s.notificationMu.RUnlock()
	return s.notificationTx
}

func (s *RuntimeStore) WorkerWakeups() *workerplatform.WakeupBroker {
	if s == nil {
		return nil
	}
	s.workerWakeupsMu.Lock()
	defer s.workerWakeupsMu.Unlock()
	if s.workerWakeups == nil {
		s.workerWakeups = workerplatform.NewWakeupBroker()
	}
	return s.workerWakeups
}

type runtimeWorkerScopeCursor struct {
	mu     sync.Mutex
	queues map[string]*runtimeWorkerQueueCursor
}

type runtimeWorkerQueueCursor struct {
	mu    sync.Mutex
	after string
}

type WorkerScopeQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type WorkerScopeExecutor interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}

// RegisterWorkerQueueScope records a payload-free Workspace discovery hint.
// The owner task remains the source of truth and all claims stay scoped.
func (s *RuntimeStore) RegisterWorkerQueueScope(ctx context.Context, executor WorkerScopeExecutor, queueKind, workspaceID, updatedAt string) error {
	if s == nil || executor == nil {
		return fmt.Errorf("worker queue scope store is required")
	}
	queueKind, workspaceID, updatedAt = strings.TrimSpace(queueKind), strings.TrimSpace(workspaceID), strings.TrimSpace(updatedAt)
	if queueKind == "" {
		return fmt.Errorf("worker queue kind and workspace are required")
	}
	if len(workspaceID) == 0 {
		return fmt.Errorf("worker queue kind and workspace are required")
	}
	if updatedAt == "" {
		updatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	digest := sha256.Sum256([]byte(queueKind + "\x00" + workspaceID))
	id := "worker_scope:" + hex.EncodeToString(digest[:12])
	insert := ormbuilder.NewInsertBuilder(s.SQLRenderer, "runtime_worker_queue_scopes").
		Columns("id", "queue_kind", "scope_key", "updated_at").Values(id, queueKind, workspaceID, updatedAt)
	insert, err := s.Engine.ApplyUpsert(insert, []string{"id"},
		ormbuilder.AssignExpression("updated_at", ormbuilder.InsertedValue("updated_at")),
	)
	if err != nil {
		return fmt.Errorf("build worker queue scope upsert: %w", err)
	}
	statement, args, err := insert.Build()
	if err != nil {
		return fmt.Errorf("build worker queue scope upsert: %w", err)
	}
	_, err = executor.ExecContext(ctx, statement, args...)
	return err
}

// WorkerQueueScopePage returns a bounded, round-robin page of active workspace
// scopes. Queue payload reads remain workspace-scoped; this registry is the
// Runtime-global discovery boundary.
func (s *RuntimeStore) WorkerQueueScopePage(ctx context.Context, queryer WorkerScopeQueryer, queueKind string, limit int) ([]string, error) {
	if s == nil || queryer == nil {
		return nil, fmt.Errorf("worker queue scope store is required")
	}
	queueKind = strings.TrimSpace(queueKind)
	if queueKind == "" {
		return nil, fmt.Errorf("worker queue kind is required")
	}
	if limit <= 0 {
		limit = 32
	}
	if limit > 256 {
		limit = 256
	}
	cursors := s.workerScopeCursor
	if cursors == nil {
		cursors = &runtimeWorkerScopeCursor{}
		s.workerScopeCursor = cursors
	}
	cursors.mu.Lock()
	if cursors.queues == nil {
		cursors.queues = map[string]*runtimeWorkerQueueCursor{}
	}
	cursor := cursors.queues[queueKind]
	if cursor == nil {
		cursor = &runtimeWorkerQueueCursor{}
		cursors.queues[queueKind] = cursor
	}
	cursors.mu.Unlock()
	cursor.mu.Lock()
	defer cursor.mu.Unlock()
	after := cursor.after
	selectBuilder := ormbuilder.NewSelectBuilder(s.SQLRenderer, "runtime_worker_queue_scopes").
		Columns("scope_key").
		Where(ormbuilder.And(ormbuilder.Equal("queue_kind", queueKind), ormbuilder.GreaterThan("scope_key", after))).
		OrderBy(ormbuilder.Ascending("scope_key")).Limit(limit + 1)
	statement, args, err := selectBuilder.Build()
	if err != nil {
		return nil, fmt.Errorf("build worker queue scope page: %w", err)
	}
	rows, err := queryer.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := make([]string, 0, limit+1)
	for rows.Next() {
		var workspaceID string
		if err := rows.Scan(&workspaceID); err != nil {
			return nil, err
		}
		workspaceID = strings.TrimSpace(workspaceID)
		if len(workspaceID) > 0 {
			values = append(values, workspaceID)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(values) > limit {
		values = values[:limit]
		cursor.after = values[len(values)-1]
		return values, nil
	}
	cursor.after = ""
	return values, nil
}

func OpenContext(ctx context.Context, cfg config.Config) (*RuntimeStore, error) {
	return openContextWithDependencies(ctx, cfg, defaultRuntimeOpenDependencies())
}

func openContextWithDependencies(ctx context.Context, cfg config.Config, dependencies runtimeOpenDependencies) (*RuntimeStore, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	engine, err := dependencies.engine(cfg.DatabaseDriver)
	if err != nil {
		return nil, err
	}
	sqlMetrics := telemetry.NewSQLMetricsWithNamespace("domainry_runtime")
	operationalMetrics := NewRuntimeOperationalMetrics(cfg.MigrationBackupLastSuccessAt, cfg.MigrationRestoreDrillSuccessAt)
	connection, err := openRuntimeConnection(ctx, engine, cfg, dependencies, sqlMetrics)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db := connection.database
	migrationDB := connection.migrationDatabase
	if err := engine.Configure(ctx, db, cfg); err != nil {
		connection.close()
		return nil, err
	}
	activeMaterial := sha256.Sum256([]byte(cfg.IntegrationSecretKey))
	activeID := strings.TrimSpace(cfg.IntegrationActiveKeyID)
	if activeID == "" {
		activeID = "legacy-v1"
	}
	decryptOnly := make([]secrets.Key, 0, len(cfg.IntegrationDecryptOnlyKeys))
	for id, value := range cfg.IntegrationDecryptOnlyKeys {
		material := sha256.Sum256([]byte(value))
		decryptOnly = append(decryptOnly, secrets.Key{ID: id, Material: material[:]})
	}
	keyRing, err := dependencies.keyRing(secrets.Key{ID: activeID, Material: activeMaterial[:]}, decryptOnly...)
	if err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("initialize integration key ring: %w", err)
	}
	databaseSchema := connection.databaseSchema
	store := &RuntimeStore{SQLDatabase: base.NewSQLDatabase(db, engine, databaseSchema), db: db, migrationDB: migrationDB, engine: engine, config: cfg, databaseSchema: databaseSchema, postgresProfile: connection.postgresProfile, postgresCapabilities: connection.postgresCapabilities, migratorCapabilities: connection.migratorCapabilities, secretMaterialKey: activeMaterial, secretKeyProvider: keyRing, idempotencyMetrics: idempotency.NewMemoryMetricsCollector(4096), sqlMetrics: sqlMetrics, operationalMetrics: operationalMetrics, workerScopeCursor: &runtimeWorkerScopeCursor{}, workerWakeups: workerplatform.NewWakeupBroker()}
	var migrationErr error
	migrationStarted := time.Now()
	if cfg.EffectiveDatabaseMigrationMode() == "verify" {
		migrationErr = store.verifyMigrations(ctx, cfg)
	} else {
		migrationErr = store.applyMigrations(ctx, cfg)
	}
	operationalMetrics.ObserveMigration(time.Since(migrationStarted), migrationErr)
	if migrationErr != nil {
		if migrationDB != nil {
			_ = migrationDB.Close()
		}
		_ = db.Close()
		return nil, migrationErr
	}
	store.migrationCompatible = true
	return store, nil
}

// OpenContextWithKeyProvider replaces the local env/file key ring with a
// production KMS, Vault, or Secret Manager adapter before secret access.
func OpenContextWithKeyProvider(ctx context.Context, cfg config.Config, provider secrets.KeyProvider) (*RuntimeStore, error) {
	if provider == nil {
		return nil, fmt.Errorf("secret key provider is required")
	}
	store, err := OpenContext(ctx, cfg)
	if err != nil {
		return nil, err
	}
	store.secretKeyProvider = provider
	return store, nil
}

func (s *RuntimeStore) Close() error {
	if s == nil {
		return nil
	}
	var first error
	if s.migrationConn != nil {
		first = s.migrationConn.Close()
		s.migrationConn = nil
	}
	if s.migrationDB != nil {
		first = s.migrationDB.Close()
	}
	if s.db != nil {
		if err := s.db.Close(); first == nil {
			first = err
		}
	}
	return first
}

// CloseContext stops accepting new work and lets database/sql drain operations
// already in flight, bounded by the caller's shutdown deadline.
func (s *RuntimeStore) CloseContext(ctx context.Context) error {
	if s == nil {
		return nil
	}
	done := make(chan error, 1)
	go func() {
		var first error
		if s.migrationDB != nil {
			first = s.migrationDB.Close()
		}
		if s.db != nil {
			if err := s.db.Close(); first == nil {
				first = err
			}
		}
		done <- first
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return fmt.Errorf("close runtime database: %w", ctx.Err())
	}
}

func (s *RuntimeStore) DB() *sql.DB {
	return s.db
}

func (s *RuntimeStore) SQLMetrics() *telemetry.SQLMetrics {
	if s == nil {
		return nil
	}
	return s.sqlMetrics
}

func (s *RuntimeStore) OperationalMetrics() *RuntimeOperationalMetrics {
	if s == nil {
		return nil
	}
	return s.operationalMetrics
}

func (s *RuntimeStore) Driver() string {
	return string(s.engine.Name())
}

func (s *RuntimeStore) DatabaseSchema() string {
	return s.databaseSchema
}

func (s *RuntimeStore) DatabaseStatus() (postgres.SafeStatus, bool) {
	if s == nil || s.postgresProfile == nil {
		return postgres.SafeStatus{}, false
	}
	return s.postgresProfile.SafeStatus(), true
}

type DatabaseReadiness struct {
	Ready                    bool   `json:"ready"`
	Failure                  string `json:"failure,omitempty"`
	ReadReady                bool   `json:"read_ready"`
	WriteReady               bool   `json:"write_ready"`
	MigrationCompatible      bool   `json:"migration_compatible"`
	PoolDegraded             bool   `json:"pool_degraded"`
	SchemaExists             bool   `json:"schema_exists"`
	SchemaUsage              bool   `json:"schema_usage"`
	ReadOnly                 bool   `json:"read_only"`
	TLSVerified              bool   `json:"tls_verified"`
	MigrationConnectionReady bool   `json:"migration_connection_ready"`
	RLSEnabled               bool   `json:"rls_enabled"`
	RLSPolicyVersion         string `json:"rls_policy_version,omitempty"`
	RLSCoveredTables         int    `json:"rls_covered_tables"`
	RLSMissingTables         int    `json:"rls_missing_tables"`
}

func (s *RuntimeStore) DatabaseReadiness() DatabaseReadiness {
	if s == nil || s.postgresProfile == nil {
		ready := s != nil && s.db != nil
		return DatabaseReadiness{Ready: ready, ReadReady: ready, WriteReady: ready, MigrationCompatible: ready}
	}
	capability := s.postgresCapabilities
	stats := s.db.Stats()
	result := DatabaseReadiness{
		SchemaExists:             capability.SchemaExists,
		SchemaUsage:              capability.SchemaUsage,
		ReadOnly:                 capability.ReadOnly || capability.InRecovery,
		TLSVerified:              capability.TLS == s.postgresProfile.TLS,
		MigrationConnectionReady: !s.postgresProfile.MigrationConfigured || s.migratorCapabilities.Database != "",
		RLSEnabled:               s.workspaceRLS.Enabled,
		RLSPolicyVersion:         s.workspaceRLS.PolicyVersion,
		RLSCoveredTables:         len(s.workspaceRLS.CoveredTables),
		RLSMissingTables:         len(s.workspaceRLS.MissingTables),
		ReadReady:                capability.SchemaExists && capability.SchemaUsage,
		WriteReady:               capability.SchemaExists && capability.SchemaUsage && !capability.ReadOnly && !capability.InRecovery,
		MigrationCompatible:      s.migrationCompatible,
		PoolDegraded:             stats.MaxOpenConnections > 0 && stats.InUse >= stats.MaxOpenConnections,
	}
	switch {
	case !result.SchemaExists || !result.SchemaUsage:
		result.Failure = postgres.FailureSchemaIncompatible
	case result.ReadOnly:
		result.Failure = "read_only"
	case !result.TLSVerified:
		result.Failure = postgres.FailureTLS
	case !result.MigrationConnectionReady:
		result.Failure = postgres.FailureServerUnavailable
	case !result.MigrationCompatible:
		result.Failure = "migration_incompatible"
	case result.PoolDegraded:
		result.Failure = "pool_degraded"
	case s.postgresProfile.RLSEnabled && (!result.RLSEnabled || result.RLSMissingTables > 0):
		result.Failure = "rls_incompatible"
	default:
		result.Ready = true
	}
	return result
}

func (s *RuntimeStore) ObserveIdempotency(_ context.Context, workspaceID, scope string, outcome idempotency.Outcome) {
	if s != nil && s.idempotencyMetrics != nil {
		s.idempotencyMetrics.Observe(workspaceID, scope, outcome)
	}
}

func (s *RuntimeStore) IdempotencyMetrics(_ context.Context) idempotency.MetricsCollector {
	if s == nil {
		return nil
	}
	return s.idempotencyMetrics
}
