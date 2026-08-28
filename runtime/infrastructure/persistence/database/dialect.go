package database

import (
	"fmt"
	"strings"

	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/mysql"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/postgres"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite"
)

type dialect = driver.Dialect

func dialectFor(driver string) (dialect, error) {
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "", "sqlite", "sqlite3":
		return sqlite.Dialect{}, nil
	case "mysql":
		return mysql.Dialect{}, nil
	case "postgres", "postgresql", "pgx":
		return postgres.Dialect{}, nil
	default:
		return nil, fmt.Errorf("unsupported database driver %q", driver)
	}
}

func validSQLIdentifier(value string) bool {
	return driver.ValidSQLIdentifier(value)
}

func SQLIdentifier(value string) string {
	return sqlite.Dialect{}.Identifier(value)
}

func (s *RuntimeStore) metadataIDColumnType() string {
	if s.dialect.Name() == "mysql" {
		return "VARCHAR(191)"
	}
	return "TEXT"
}

// SetDialectForTesting exercises SQL generation contracts against the shared
// in-memory fixture without exposing the dialect field.
func (s *RuntimeStore) SetDialectForTesting(driver string) error {
	dialect, err := dialectFor(driver)
	if err != nil {
		return err
	}
	s.dialect = dialect
	return nil
}
