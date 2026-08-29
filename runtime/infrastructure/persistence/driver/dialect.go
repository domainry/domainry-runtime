package driver

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	ormdriver "github.com/domainry/domainry-orm/driver"
	ormmysql "github.com/domainry/domainry-orm/mysql"
	ormpostgres "github.com/domainry/domainry-orm/postgres"
	ormsqlite "github.com/domainry/domainry-orm/sqlite"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

// Dialect combines the shared SQL renderer with Runtime-owned connection and
// migration configuration. Generic rendering lives in domainry-orm.
type Dialect interface {
	Name() string
	SQLDriver() string
	DSN(config.Config) (string, error)
	Configure(context.Context, *sql.DB, string) error
	SQLDialect() ormdialect.Dialect
	SchemaMigrationSQL() string
}

type ProjectDatabaseEnsurer interface {
	EnsureProjectDatabase(context.Context, config.Config) error
}

type EngineProfile interface {
	ormdriver.Profile
	MigrationDatabasePath(config.Config) string
	ManagedDatabaseMarkerEnabled() bool
	ColumnDefinition(string) string
	ApplicationTablesQuery(ormdialect.Renderer, string) SchemaQuery
	WorkspaceTablesQuery(ormdialect.Renderer, string) SchemaQuery
	TableExistsQuery(ormdialect.Renderer, string, string) SchemaQuery
	MigrationLedgerTypes() MigrationLedgerTypes
	EnsureMigrationNamespace(context.Context, SchemaDatabase, ormdialect.Renderer, string) error
	ConfigureMigrationTransaction(context.Context, *sql.Tx, ormdialect.Renderer, string, time.Duration, time.Duration) error
	MigrationBackupPolicy() MigrationBackupPolicy
	MigrationRollbackPolicy() MigrationRollbackPolicy
	AcquireMigrationLock(context.Context, *sql.DB, ormdialect.Renderer, MigrationLockOptions) (MigrationLock, error)
	WorkspaceRLSSupported() bool
	ApplyWorkspaceRLS(context.Context, *sql.DB, ormdialect.Renderer, string, string, string) error
	InspectWorkspaceRLS(context.Context, *sql.DB, ormdialect.Renderer, string, string, string) (WorkspaceRLSStatus, error)
	OrderedDecimalTextStorage() bool
	RecordReadIsolation() sql.IsolationLevel
	ReportDateBucket(string, string, bool) (string, error)
}

type SchemaQuery struct {
	Statement string
	Arguments []any
}

type MigrationLedgerTypes struct {
	Key       string
	Timestamp string
}

type MigrationBackupPolicy struct {
	LocalSnapshot  bool
	EvidenceEngine string
	BackupIDPrefix string
}

type MigrationRollbackPolicy struct {
	Mode                   string
	RequiresVerifiedBackup bool
	Procedure              []string
}

type MigrationLockOptions struct {
	DatabasePath   string
	DatabaseSchema string
	Owner          string
	LockTimeout    time.Duration
	ConnectTimeout time.Duration
}

type MigrationLock struct {
	Connection *sql.Conn
	Release    func()
}

type WorkspaceRLSStatus struct {
	Enabled       bool     `json:"enabled"`
	Forced        bool     `json:"forced"`
	RuntimeRole   string   `json:"runtime_role,omitempty"`
	RoleOwnsTable bool     `json:"role_owns_table"`
	RoleBypassRLS bool     `json:"role_bypass_rls"`
	PolicyVersion string   `json:"policy_version,omitempty"`
	CoveredTables []string `json:"covered_tables,omitempty"`
	MissingTables []string `json:"missing_tables,omitempty"`
}

type SchemaDatabase interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

type engineProfileProvider interface{ EngineProfile() EngineProfile }

type portableEngineProfile struct{ ormdriver.Profile }

func (portableEngineProfile) MigrationDatabasePath(config.Config) string { return "" }
func (portableEngineProfile) ManagedDatabaseMarkerEnabled() bool         { return true }
func (portableEngineProfile) ColumnDefinition(value string) string       { return value }
func (portableEngineProfile) ApplicationTablesQuery(ormdialect.Renderer, string) SchemaQuery {
	return SchemaQuery{}
}
func (portableEngineProfile) WorkspaceTablesQuery(ormdialect.Renderer, string) SchemaQuery {
	return SchemaQuery{}
}
func (portableEngineProfile) TableExistsQuery(ormdialect.Renderer, string, string) SchemaQuery {
	return SchemaQuery{}
}
func (portableEngineProfile) MigrationLedgerTypes() MigrationLedgerTypes {
	return MigrationLedgerTypes{Key: "TEXT", Timestamp: "TEXT"}
}
func (portableEngineProfile) EnsureMigrationNamespace(context.Context, SchemaDatabase, ormdialect.Renderer, string) error {
	return nil
}
func (portableEngineProfile) ConfigureMigrationTransaction(context.Context, *sql.Tx, ormdialect.Renderer, string, time.Duration, time.Duration) error {
	return nil
}
func (portableEngineProfile) MigrationBackupPolicy() MigrationBackupPolicy {
	return MigrationBackupPolicy{}
}
func (portableEngineProfile) MigrationRollbackPolicy() MigrationRollbackPolicy {
	return MigrationRollbackPolicy{Mode: "unsupported", RequiresVerifiedBackup: true}
}
func (portableEngineProfile) AcquireMigrationLock(context.Context, *sql.DB, ormdialect.Renderer, MigrationLockOptions) (MigrationLock, error) {
	return MigrationLock{}, fmt.Errorf("database engine does not support migration locking")
}
func (portableEngineProfile) WorkspaceRLSSupported() bool { return false }
func (portableEngineProfile) ApplyWorkspaceRLS(context.Context, *sql.DB, ormdialect.Renderer, string, string, string) error {
	return nil
}
func (portableEngineProfile) InspectWorkspaceRLS(context.Context, *sql.DB, ormdialect.Renderer, string, string, string) (WorkspaceRLSStatus, error) {
	return WorkspaceRLSStatus{}, nil
}
func (portableEngineProfile) OrderedDecimalTextStorage() bool         { return false }
func (portableEngineProfile) RecordReadIsolation() sql.IsolationLevel { return sql.LevelSerializable }
func (portableEngineProfile) ReportDateBucket(string, string, bool) (string, error) {
	return "", fmt.Errorf("database engine does not support report date buckets")
}

var portableProfileRegistry = map[ormdialect.Name]func() EngineProfile{
	ormdialect.SQLite: func() EngineProfile { return portableEngineProfile{Profile: ormsqlite.NewProfile()} },
	ormdialect.MySQL:  func() EngineProfile { return portableEngineProfile{Profile: ormmysql.NewProfile()} },
	ormdialect.Postgres: func() EngineProfile {
		return portableEngineProfile{Profile: ormpostgres.NewProfile()}
	},
}

func ProfileFor(value Dialect) EngineProfile {
	if provider, ok := value.(engineProfileProvider); ok {
		return provider.EngineProfile()
	}
	factory, found := portableProfileRegistry[value.SQLDialect().Name()]
	if !found {
		panic(fmt.Sprintf("unsupported database profile %q", value.Name()))
	}
	return factory()
}
