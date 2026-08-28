package database

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/jackc/pgx/v5"
)

// TestPostgresRuntimePersistenceEndToEnd proves the provider-neutral contract:
// a plain PostgreSQL DSN is enough to migrate, assemble Runtime schema,
// persist data, and restart in verify-only mode.
func TestPostgresRuntimePersistenceEndToEnd(t *testing.T) {
	dsn := strings.TrimSpace(os.Getenv("RUNTIME_POSTGRES_TEST_DSN"))
	if dsn == "" {
		t.Skip("RUNTIME_POSTGRES_TEST_DSN is not configured")
	}

	schema := fmt.Sprintf("runtime_contract_%d", time.Now().UnixNano())
	admin, err := pgx.Connect(t.Context(), dsn)
	if err != nil {
		t.Fatalf("connect PostgreSQL cleanup session: %v", err)
	}
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, _ = admin.Exec(cleanupCtx, "DROP SCHEMA IF EXISTS "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		_ = admin.Close(cleanupCtx)
	})

	migrationPath := filepath.Join(t.TempDir(), "001_provider_neutral_contract.sql")
	if err := os.WriteFile(migrationPath, []byte(`CREATE TABLE provider_neutral_contract (
    id TEXT PRIMARY KEY,
    payload TEXT NOT NULL
);`), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.Config{
		Environment:           "development",
		DatabaseDriver:        "postgres",
		DatabaseDSN:           dsn,
		DatabaseMigrationMode: "apply",
		DatabaseSchema:        schema,
		DatabaseMaxOpenConns:  4,
		DatabaseMaxIdleConns:  2,
		MigrationSQL:          migrationPath,
	}
	store, err := OpenContext(t.Context(), cfg)
	if err != nil {
		t.Fatalf("open generic PostgreSQL Runtime store: %v", err)
	}
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		_ = store.Close()
		t.Fatalf("assemble Runtime schema: %v", err)
	}
	if _, err := store.DB().ExecContext(t.Context(), "INSERT INTO "+store.TableIdentifier("provider_neutral_contract")+" (id, payload) VALUES ($1, $2)", "row-1", "persisted"); err != nil {
		_ = store.Close()
		t.Fatalf("persist through generic PostgreSQL path: %v", err)
	}
	var payload string
	if err := store.DB().QueryRowContext(t.Context(), "SELECT payload FROM "+store.TableIdentifier("provider_neutral_contract")+" WHERE id = $1", "row-1").Scan(&payload); err != nil || payload != "persisted" {
		_ = store.Close()
		t.Fatalf("read persisted row: payload=%q err=%v", payload, err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}

	cfg.DatabaseMigrationMode = "verify"
	restarted, err := OpenContext(t.Context(), cfg)
	if err != nil {
		t.Fatalf("restart generic PostgreSQL Runtime store: %v", err)
	}
	defer restarted.Close()
	if err := restarted.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatalf("verify Runtime schema after restart: %v", err)
	}
	if err := restarted.DB().QueryRowContext(t.Context(), "SELECT payload FROM "+restarted.TableIdentifier("provider_neutral_contract")+" WHERE id = $1", "row-1").Scan(&payload); err != nil || payload != "persisted" {
		t.Fatalf("verify persisted row after restart: payload=%q err=%v", payload, err)
	}
}
