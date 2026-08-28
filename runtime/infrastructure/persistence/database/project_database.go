package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/domainry/domainry-runtime/runtime/platform/config"
	mysqlDriver "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/stdlib"
)

// EnsureProjectDatabase creates the configured project database before the
// Runtime opens its single process-owned pool.
func EnsureProjectDatabase(ctx context.Context, cfg config.Config) error {
	switch strings.ToLower(strings.TrimSpace(cfg.DatabaseDriver)) {
	case "", "sqlite", "sqlite3":
		path := strings.TrimSpace(cfg.DBPath)
		if path == "" || path == ":memory:" || strings.HasPrefix(path, "file:") {
			return nil
		}
		return os.MkdirAll(filepath.Dir(path), 0o755)
	case "postgres", "postgresql", "pgx":
		return ensurePostgresDatabase(ctx, cfg.DatabaseDSN)
	case "mysql":
		return ensureMySQLDatabase(ctx, cfg.DatabaseDSN)
	default:
		return fmt.Errorf("unsupported database driver %q", cfg.DatabaseDriver)
	}
}

func ensurePostgresDatabase(ctx context.Context, dsn string) error {
	target, err := pgx.ParseConfig(strings.TrimSpace(dsn))
	if err != nil {
		return fmt.Errorf("parse PostgreSQL project database DSN: %w", err)
	}
	databaseName := strings.TrimSpace(target.Database)
	if databaseName == "" {
		return fmt.Errorf("PostgreSQL project database name is required")
	}
	maintenance := target.Copy()
	maintenance.Database = "postgres"
	db := sql.OpenDB(stdlib.GetConnector(*maintenance))
	defer db.Close()
	var exists bool
	if err := db.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = $1)", databaseName).Scan(&exists); err != nil {
		return fmt.Errorf("inspect PostgreSQL project database: %w", err)
	}
	if exists {
		return nil
	}
	if _, err := db.ExecContext(ctx, `CREATE DATABASE "`+strings.ReplaceAll(databaseName, `"`, `""`)+`"`); err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) && postgresError.Code == "42P04" {
			return nil
		}
		return fmt.Errorf("create PostgreSQL project database %q: %w", databaseName, err)
	}
	return nil
}

func ensureMySQLDatabase(ctx context.Context, dsn string) error {
	target, err := mysqlDriver.ParseDSN(strings.TrimSpace(dsn))
	if err != nil {
		return fmt.Errorf("parse MySQL project database DSN: %w", err)
	}
	databaseName := strings.TrimSpace(target.DBName)
	if databaseName == "" {
		return fmt.Errorf("MySQL project database name is required")
	}
	target.DBName = ""
	db, err := sql.Open("mysql", target.FormatDSN())
	if err != nil {
		return fmt.Errorf("open MySQL project database bootstrap connection: %w", err)
	}
	defer db.Close()
	quoted := "`" + strings.ReplaceAll(databaseName, "`", "``") + "`"
	if _, err := db.ExecContext(ctx, "CREATE DATABASE IF NOT EXISTS "+quoted); err != nil {
		return fmt.Errorf("create MySQL project database %q: %w", databaseName, err)
	}
	return nil
}
