package driver

import (
	"context"
	"database/sql"
	"fmt"

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

func ProfileFor(value Dialect) ormdriver.Profile {
	switch value.Name() {
	case "sqlite":
		return ormsqlite.NewProfile()
	case "mysql":
		return ormmysql.NewProfile()
	case "postgres":
		return ormpostgres.NewProfile()
	default:
		panic(fmt.Sprintf("unsupported database profile %q", value.Name()))
	}
}
