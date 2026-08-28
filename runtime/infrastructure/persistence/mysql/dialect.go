package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
	"github.com/domainry/domainry-runtime/runtime/platform/config"

	_ "github.com/go-sql-driver/mysql"
)

type Dialect struct{}

func (Dialect) Name() string { return "mysql" }

func (Dialect) SQLDriver() string { return "mysql" }

func (Dialect) DSN(cfg config.Config) (string, error) {
	if strings.TrimSpace(cfg.DatabaseDSN) == "" {
		return "", fmt.Errorf("DATABASE_DSN is required when DATABASE_DRIVER=mysql")
	}
	return strings.TrimSpace(cfg.DatabaseDSN), nil
}

func (Dialect) Configure(ctx context.Context, db *sql.DB, _ string) error {
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("connect mysql database: %w", err)
	}
	return nil
}

func (Dialect) Identifier(value string) string {
	return database.QuoteIdentifier(value, "`")
}

func (Dialect) Placeholder(position int) string {
	return database.QuestionPlaceholder(position)
}

func (Dialect) SchemaMigrationSQL() string {
	return "CREATE TABLE IF NOT EXISTS `_schema_migrations` (`path` VARCHAR(255) PRIMARY KEY, `applied_at` VARCHAR(64) NOT NULL)"
}
