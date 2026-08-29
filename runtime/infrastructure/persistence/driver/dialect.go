package driver

import (
	"context"
	"database/sql"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

// Dialect combines the shared SQL renderer with Runtime-owned connection and
// migration configuration. Generic rendering lives in domainry-orm.
type Dialect interface {
	Name() string
	SQLDriver() string
	DSN(config.Config) (string, error)
	Configure(context.Context, *sql.DB, string) error
	Identifier(string) string
	Placeholder(int) string
	SchemaMigrationSQL() string
}

var QuoteIdentifier = ormdialect.QuoteIdentifier
var ValidSQLIdentifier = ormdialect.ValidIdentifier
var QuestionPlaceholder = ormdialect.QuestionPlaceholder
