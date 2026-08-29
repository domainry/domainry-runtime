package postgres

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func (engineProfile) MigrationLedgerTypes() persistencedriver.MigrationLedgerTypes {
	return persistencedriver.MigrationLedgerTypes{Key: "TEXT", Timestamp: "TEXT"}
}
func (engineProfile) EnsureMigrationNamespace(ctx context.Context, database persistencedriver.SchemaDatabase, renderer ormdialect.Renderer, databaseSchema string) error {
	if strings.TrimSpace(databaseSchema) == "" || strings.EqualFold(databaseSchema, "public") {
		return nil
	}
	if _, err := database.ExecContext(ctx, "CREATE SCHEMA IF NOT EXISTS "+renderer.Identifier(databaseSchema)); err != nil {
		return fmt.Errorf("prepare PostgreSQL runtime schema: %w", err)
	}
	return nil
}
func (engineProfile) ConfigureMigrationTransaction(ctx context.Context, transaction *sql.Tx, renderer ormdialect.Renderer, databaseSchema string, lockTimeout, statementTimeout time.Duration) error {
	if _, err := transaction.ExecContext(ctx, "SELECT set_config('search_path', "+renderer.Placeholder(1)+", true)", databaseSchema); err != nil {
		return fmt.Errorf("set migration schema search path: %w", err)
	}
	if lockTimeout > 0 {
		if _, err := transaction.ExecContext(ctx, "SELECT set_config('lock_timeout', "+renderer.Placeholder(1)+", true)", fmt.Sprintf("%dms", lockTimeout.Milliseconds())); err != nil {
			return fmt.Errorf("set migration lock timeout: %w", err)
		}
	}
	if statementTimeout > 0 {
		if _, err := transaction.ExecContext(ctx, "SELECT set_config('statement_timeout', "+renderer.Placeholder(1)+", true)", fmt.Sprintf("%dms", statementTimeout.Milliseconds())); err != nil {
			return fmt.Errorf("set migration statement timeout: %w", err)
		}
	}
	return nil
}

func (engineProfile) MigrationDatabasePath(config.Config) string { return "" }
