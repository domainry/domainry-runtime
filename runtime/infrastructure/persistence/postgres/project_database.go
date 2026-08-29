package postgres

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
)

func (Dialect) EnsureProjectDatabase(ctx context.Context, cfg config.Config) error {
	target, err := pgx.ParseConfig(strings.TrimSpace(cfg.DatabaseDSN))
	if err != nil {
		return fmt.Errorf("parse PostgreSQL project database DSN: %w", err)
	}
	databaseName := strings.TrimSpace(target.Database)
	if databaseName == "" {
		return fmt.Errorf("PostgreSQL project database name is required")
	}
	maintenance := target.Copy()
	maintenance.Database = "postgres"
	database := sql.OpenDB(stdlib.GetConnector(*maintenance))
	defer database.Close()
	var exists bool
	if err := database.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", databaseName).Scan(&exists); err != nil {
		return fmt.Errorf("inspect PostgreSQL project database: %w", err)
	}
	if exists {
		return nil
	}
	if _, err := database.ExecContext(ctx, `CREATE DATABASE "`+strings.ReplaceAll(databaseName, `"`, `""`)+`"`); err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == "42P04" {
			return nil
		}
		return fmt.Errorf("create PostgreSQL project database %q: %w", databaseName, err)
	}
	return nil
}
