package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

type Dialect struct{}

func (Dialect) Name() string { return "postgres" }

func (Dialect) SQLDriver() string { return "pgx" }

func (Dialect) DSN(cfg config.Config) (string, error) {
	if strings.TrimSpace(cfg.DatabaseDSN) == "" {
		return "", fmt.Errorf("DATABASE_DSN is required when DATABASE_DRIVER=postgres")
	}
	return strings.TrimSpace(cfg.DatabaseDSN), nil
}

func (Dialect) Configure(ctx context.Context, db *sql.DB, _ string) error {
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("connect postgres database (%s)", ClassifyConnectionFailure(err))
	}
	return nil
}

func (Dialect) SQLDialect() ormdialect.Dialect {
	value, _ := ormdialect.New(ormdialect.Postgres)
	return value
}

func (Dialect) SchemaMigrationSQL() string {
	return `CREATE TABLE IF NOT EXISTS "_schema_migrations" ("path" TEXT PRIMARY KEY, "applied_at" TEXT NOT NULL)`
}
