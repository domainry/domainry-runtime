package database

import (
	"fmt"
	"strings"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/base"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/mysql"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/postgres"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite"
)

type dialect = driver.Engine

var engineRegistry = map[string]func() driver.Engine{
	"":           func() driver.Engine { return sqlite.NewEngine() },
	"sqlite":     func() driver.Engine { return sqlite.NewEngine() },
	"sqlite3":    func() driver.Engine { return sqlite.NewEngine() },
	"mysql":      func() driver.Engine { return mysql.NewEngine() },
	"postgres":   func() driver.Engine { return postgres.NewEngine() },
	"postgresql": func() driver.Engine { return postgres.NewEngine() },
	"pgx":        func() driver.Engine { return postgres.NewEngine() },
}

func dialectFor(driver string) (dialect, error) {
	factory := engineRegistry[strings.ToLower(strings.TrimSpace(driver))]
	if factory == nil {
		return nil, fmt.Errorf("unsupported database driver %q", driver)
	}
	return factory(), nil
}

func validSQLIdentifier(value string) bool {
	return ormdialect.ValidIdentifier(value)
}

func SQLIdentifier(value string) string {
	dialect, _ := ormdialect.New(ormdialect.SQLite)
	return dialect.Identifier(value)
}

func (s *RuntimeStore) metadataIDColumnType() string {
	return s.sqlBase().RuntimeEngine.TextKeyColumnType(191)
}

// SetDialectForTesting exercises SQL generation contracts against the shared
// in-memory fixture without exposing the dialect field.
func (s *RuntimeStore) SetDialectForTesting(driver string) error {
	dialect, err := dialectFor(driver)
	if err != nil {
		return err
	}
	s.dialect = dialect
	s.SQLDatabase = base.NewSQLDatabase(s.db, dialect, s.databaseSchema)
	return nil
}
