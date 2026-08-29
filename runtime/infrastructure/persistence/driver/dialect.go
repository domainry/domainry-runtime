package driver

import (
	"context"
	"database/sql"
	"time"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	ormdriver "github.com/domainry/domainry-orm/driver"
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

type EngineProfile interface {
	ormdriver.Profile
	EnsureProjectDatabase(context.Context, config.Config) error
	MigrationDatabasePath(config.Config) string
	ManagedDatabaseMarkerEnabled() bool
	ColumnDefinition(string) string
	ApplicationTablesQuery(ormdialect.Renderer, string) SchemaQuery
	WorkspaceTablesQuery(ormdialect.Renderer, string) SchemaQuery
	TableExistsQuery(ormdialect.Renderer, string, string) SchemaQuery
	IndexesQuery(ormdialect.Renderer, string, string) SchemaQuery
	InspectModuleSchemaTable(context.Context, SchemaDatabase, ormdialect.Renderer, string, string) (ModuleSchemaTable, bool, error)
	EvidenceSchemaTypes(string) EvidenceSchemaTypes
	NormalizeEvidenceSchema(context.Context, SchemaDatabase, ormdialect.Renderer) error
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

type Engine interface {
	EngineProfile
	SQLDriver() string
	DSN(config.Config) (string, error)
	Configure(context.Context, *sql.DB, string) error
	SQLDialect() ormdialect.Dialect
	SchemaMigrationSQL() string
}

type EvidenceSchemaProfile interface {
	Types(string) EvidenceSchemaTypes
	Normalize(context.Context, SchemaDatabase, ormdialect.Renderer) error
}

type RecordProfile interface {
	OrderedDecimalTextStorage() bool
	ReadIsolation() sql.IsolationLevel
}

type ReportProfile interface {
	DateBucket(string, string, bool) (string, error)
}

type MigrationProfile interface {
	MigrationLedgerTypes() MigrationLedgerTypes
	EnsureMigrationNamespace(context.Context, SchemaDatabase, ormdialect.Renderer, string) error
	ConfigureMigrationTransaction(context.Context, *sql.Tx, ormdialect.Renderer, string, time.Duration, time.Duration) error
	MigrationBackupPolicy() MigrationBackupPolicy
	MigrationRollbackPolicy() MigrationRollbackPolicy
	AcquireMigrationLock(context.Context, *sql.DB, ormdialect.Renderer, MigrationLockOptions) (MigrationLock, error)
	MigrationDatabasePath(config.Config) string
}

type SchemaProfile interface {
	ManagedDatabaseMarkerEnabled() bool
	ColumnDefinition(string) string
	ApplicationTablesQuery(ormdialect.Renderer, string) SchemaQuery
	WorkspaceTablesQuery(ormdialect.Renderer, string) SchemaQuery
	TableExistsQuery(ormdialect.Renderer, string, string) SchemaQuery
	IndexesQuery(ormdialect.Renderer, string, string) SchemaQuery
	InspectModuleSchemaTable(context.Context, SchemaDatabase, ormdialect.Renderer, string, string) (ModuleSchemaTable, bool, error)
}

type WorkspaceRLSProfile interface {
	WorkspaceRLSSupported() bool
	ApplyWorkspaceRLS(context.Context, *sql.DB, ormdialect.Renderer, string, string, string) error
	InspectWorkspaceRLS(context.Context, *sql.DB, ormdialect.Renderer, string, string, string) (WorkspaceRLSStatus, error)
}

type ProjectDatabaseProfile interface {
	EnsureProjectDatabase(context.Context, config.Config) error
}

type SchemaQuery struct {
	Statement string
	Arguments []any
}

type ModuleSchemaColumn struct {
	Name       string
	Physical   string
	Nullable   bool
	PrimaryKey bool
}

type ModuleSchemaIndex struct {
	Name    string
	Unique  bool
	Columns []string
}

type ModuleSchemaTable struct {
	Columns []ModuleSchemaColumn
	Indexes []ModuleSchemaIndex
}

type MigrationLedgerTypes struct {
	Key       string
	Timestamp string
}

type EvidenceSchemaTypes struct {
	LargeText           string
	IdempotencyScope    string
	AuditCursor         string
	RetirementEngine    string
	RetirementNamespace string
	RetirementKind      string
	RetirementObject    string
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
	QueryRowContext(context.Context, string, ...any) *sql.Row
}
