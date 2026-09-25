package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
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

func (Dialect) Configure(ctx context.Context, db *sql.DB, _ config.Config) error {
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("connect mysql database: %w", err)
	}
	return nil
}

func (Dialect) SQLDialect() ormdialect.Dialect {
	value, _ := ormdialect.New(ormdialect.MySQL)
	return value
}

func (Dialect) SchemaMigrationSQL() string {
	return "CREATE TABLE IF NOT EXISTS `_schema_migrations` (`path` VARCHAR(255) PRIMARY KEY, `applied_at` BIGINT NOT NULL)"
}
