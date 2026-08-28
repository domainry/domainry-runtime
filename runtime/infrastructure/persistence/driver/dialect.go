package driver

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

// Dialect describes the database-specific behavior used by the shared
// persistence stores.
type Dialect interface {
	Name() string
	SQLDriver() string
	DSN(config.Config) (string, error)
	Configure(context.Context, *sql.DB, string) error
	Identifier(string) string
	Placeholder(int) string
	SchemaMigrationSQL() string
}

func QuoteIdentifier(value, quote string) string {
	if !ValidSQLIdentifier(value) {
		panic(fmt.Sprintf("invalid SQL identifier %q", value))
	}
	return quote + value + quote
}

func ValidSQLIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for i, r := range value {
		if i == 0 {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || r == '_' {
				continue
			}
			return false
		}
		if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '_' {
			continue
		}
		return false
	}
	return true
}

func QuestionPlaceholder(_ int) string {
	return "?"
}
