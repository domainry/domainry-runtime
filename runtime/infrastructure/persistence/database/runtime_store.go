package database

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/secrets"
	"github.com/domainry/domainry-foundation/telemetry"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	sharedworkerscope "github.com/domainry/domainry-foundation/workerscope"
	metadatasdk "github.com/domainry/domainry-metadata-sdk"
	"github.com/domainry/domainry-notification-sdk/modulehost"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/base"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/postgres"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

type RuntimeStore struct {
	*base.SQLDatabase
	db                          *sql.DB
	migrationDB                 *sql.DB
	migrationConn               *sql.Conn
	engine                      databaseEngine
	config                      config.Config
	databaseSchema              string
	postgresProfile             *postgres.ConnectionProfile
	postgresCapabilities        postgres.Capabilities
	migratorCapabilities        postgres.Capabilities
	expectedMigrations          []string
	expectedChecksums           map[string]string
	secretMaterialKey           [32]byte
	secretKeyProvider           secrets.KeyProvider
	migrationBackupReady        bool
	migrationCompatible         bool
	migrationBackupID           string
	idempotencyMetrics          *idempotency.MemoryMetricsCollector
	sqlMetrics                  *telemetry.SQLMetrics
	operationalMetrics          *RuntimeOperationalMetrics
	workerScopeCursor           *runtimeWorkerScopeCursor
	workerScopes                *sharedworkerscope.Store
	workerWakeupsMu             sync.Mutex
	workerWakeups               *workerplatform.WakeupBroker
	notificationMu              sync.RWMutex
	notificationTx              modulehost.TransactionalPublisher
	notificationSaaS            *NotificationSaaSPublicationScope
	schemaAssembler             runtimeSchemaAssembler
	backupChecksum              func(string) (string, error)
	migrationReadDir            func(string) ([]os.DirEntry, error)
	metadataBinding             metadatasdk.Binding
	runtimeCapabilities         RuntimeSchemaCapabilities
	runtimeCapabilitiesSelected bool
	subjectLifecycleBound       atomic.Bool
}

// BindSubjectLifecyclePersistence enables the write fences backed by the
// Lifecycle module's subject-erasure tables. Runtime schema setup intentionally
// does not create those source-owned tables, so the fences become active only
// after the Lifecycle Binding has installed and bound its persistence.
func (s *RuntimeStore) BindSubjectLifecyclePersistence() {
	if s != nil {
		s.subjectLifecycleBound.Store(true)
	}
}

func (s *RuntimeStore) SubjectLifecyclePersistenceBound() bool {
	return s != nil && s.subjectLifecycleBound.Load()
}

func (s *RuntimeStore) BindMetadata(binding metadatasdk.Binding) error {
	if s == nil || binding == nil || binding.Definitions() == nil || binding.Localization() == nil || binding.Dictionaries() == nil || binding.Projection() == nil {
		return fmt.Errorf("Metadata Binding is incomplete")
	}
	if s.metadataBinding != nil {
		return fmt.Errorf("Metadata Binding is already configured")
	}
	s.metadataBinding = binding
	return nil
}

func (s *RuntimeStore) Metadata() metadatasdk.Binding {
	if s == nil {
		return nil
	}
	return s.metadataBinding
}

type NotificationSaaSPublicationScope struct {
	WorkspaceID, ApplicationKey string
}

func (s *RuntimeStore) BindNotificationSaaSPublications(scope NotificationSaaSPublicationScope) error {
	if s == nil || strings.TrimSpace(scope.WorkspaceID) == "" || strings.TrimSpace(scope.ApplicationKey) == "" {
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

type WorkerScopeQueryer = sharedworkerscope.Queryer
type WorkerScopeExecutor = sharedworkerscope.Executor

func (s *RuntimeStore) WorkerScopes() *sharedworkerscope.Store {
	if s == nil {
		return nil
	}
	if s.workerScopes != nil {
		return s.workerScopes
	}
	if s.DB() == nil {
		return nil
	}
	return sharedworkerscope.NewStore(s.DB(), s.SQLRenderer)
}

func (s *RuntimeStore) RegisterWorkerQueueScope(ctx context.Context, executor WorkerScopeExecutor, queueKind, workspaceID, updatedAt string) error {
	if s == nil || executor == nil {
		return fmt.Errorf("worker queue scope store is required")
	}
	queueKind, workspaceID, updatedAt = strings.TrimSpace(queueKind), strings.TrimSpace(workspaceID), strings.TrimSpace(updatedAt)
	if queueKind == "" {
		return fmt.Errorf("worker queue kind and workspace are required")
	}
	if _, registered := WorkerScopeRegistrationFor(queueKind); !registered {
		return fmt.Errorf("worker scope owner %q is not registered", queueKind)
	}
	if len(workspaceID) == 0 {
		return fmt.Errorf("worker queue kind and workspace are required")
	}
	when := time.Now().UTC()
	if updatedAt != "" {
		value, err := time.Parse(time.RFC3339Nano, updatedAt)
		if err != nil {
			return fmt.Errorf("worker queue scope timestamp is invalid: %w", err)
		}
		when = value
	}
	workerScopes := s.WorkerScopes()
	if workerScopes == nil {
		return fmt.Errorf("worker queue scope store is required")
	}
	return workerScopes.Register(ctx, executor, sharedworkerscope.NewIdentity(queueKind, workspaceID), when)
}

func (s *RuntimeStore) WorkerQueueScopePage(ctx context.Context, queryer WorkerScopeQueryer, queueKind string, limit int) ([]string, error) {
	if s == nil || queryer == nil {
		return nil, fmt.Errorf("worker queue scope store is required")
	}
	queueKind = strings.TrimSpace(queueKind)
	if queueKind == "" {
		return nil, fmt.Errorf("worker queue kind is required")
	}
	if _, registered := WorkerScopeRegistrationFor(queueKind); !registered {
		return nil, fmt.Errorf("worker scope owner %q is not registered", queueKind)
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
	workerScopes := s.WorkerScopes()
	if workerScopes == nil {
		return nil, fmt.Errorf("worker queue scope store is required")
	}
	values, err := workerScopes.ScopeKeys(ctx, queryer, sharedworkerscope.ScopeQuery{Owner: queueKind, AfterKey: after, Order: sharedworkerscope.ScopeKeyAscending, Limit: limit + 1})
	if err != nil {
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
	runtimeDatabase := base.NewSQLDatabase(db, engine, databaseSchema)
	store := &RuntimeStore{SQLDatabase: runtimeDatabase, db: db, migrationDB: migrationDB, engine: engine, config: cfg, databaseSchema: databaseSchema, postgresProfile: connection.postgresProfile, postgresCapabilities: connection.postgresCapabilities, migratorCapabilities: connection.migratorCapabilities, secretMaterialKey: activeMaterial, secretKeyProvider: keyRing, idempotencyMetrics: idempotency.NewMemoryMetricsCollector(4096), sqlMetrics: sqlMetrics, operationalMetrics: operationalMetrics, workerScopeCursor: &runtimeWorkerScopeCursor{}, workerScopes: sharedworkerscope.NewStore(db, runtimeDatabase.SQLRenderer), workerWakeups: workerplatform.NewWakeupBroker()}
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
