package integrationtest

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/domainry/domainry-runtime/runtime/platform/config"
	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/jackc/pgx/v5"
)

func realDialectPostgresConfig(t *testing.T, dsn, schema string) config.Config {
	t.Helper()
	admin, err := pgx.Connect(t.Context(), dsn)
	if err != nil {
		t.Fatalf("connect PostgreSQL contract cleanup session: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		_ = admin.Close(cleanupCtx)
	})
	return config.Config{
		DatabaseDriver:        "postgres",
		DatabaseDSN:           dsn,
		DatabaseSchema:        schema,
		DatabaseMigrationMode: "apply",
	}
}

func realDialectMySQLConfig(t *testing.T, dsn, databaseName string) config.Config {
	t.Helper()
	parsed, err := mysqldriver.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("parse MySQL contract DSN: %v", err)
	}
	adminConfig := parsed.Clone()
	adminConfig.DBName = ""
	admin, err := sql.Open("mysql", adminConfig.FormatDSN())
	if err != nil {
		t.Fatalf("open MySQL contract admin connection: %v", err)
	}
	if err := admin.PingContext(t.Context()); err != nil {
		_ = admin.Close()
		t.Fatalf("connect MySQL contract admin database: %v", err)
	}
	identifier := "`" + databaseName + "`"
	if _, err := admin.ExecContext(t.Context(), "CREATE DATABASE "+identifier+" CHARACTER SET utf8mb4 COLLATE utf8mb4_bin"); err != nil {
		_ = admin.Close()
		t.Fatalf("create isolated MySQL contract database: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		_, _ = admin.ExecContext(cleanupCtx, "DROP DATABASE IF EXISTS "+identifier)
		_ = admin.Close()
	})
	parsed.DBName = databaseName
	return config.Config{
		DatabaseDriver:        "mysql",
		DatabaseDSN:           parsed.FormatDSN(),
		DatabaseMigrationMode: "apply",
	}
}
