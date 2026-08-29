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

type EngineProfile interface {
	ormdriver.Profile
	MigrationDatabasePath(config.Config) string
	ManagedDatabaseMarkerEnabled() bool
	ColumnDefinition(string) string
	ApplicationTablesQuery(ormdialect.Renderer, string) SchemaQuery
	WorkspaceTablesQuery(ormdialect.Renderer, string) SchemaQuery
	MigrationLedgerTypes() MigrationLedgerTypes
	EnsureMigrationNamespace(context.Context, SchemaDatabase, ormdialect.Renderer, string) error
	ConfigureMigrationTransaction(context.Context, *sql.Tx, ormdialect.Renderer, string, time.Duration, time.Duration) error
}

type SchemaQuery struct {
	Statement string
	Arguments []any
}

type MigrationLedgerTypes struct {
	Key       string
	Timestamp string
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
func (portableEngineProfile) MigrationLedgerTypes() MigrationLedgerTypes {
	return MigrationLedgerTypes{Key: "TEXT", Timestamp: "TEXT"}
}
func (portableEngineProfile) EnsureMigrationNamespace(context.Context, SchemaDatabase, ormdialect.Renderer, string) error {
	return nil
}
func (portableEngineProfile) ConfigureMigrationTransaction(context.Context, *sql.Tx, ormdialect.Renderer, string, time.Duration, time.Duration) error {
	return nil
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
