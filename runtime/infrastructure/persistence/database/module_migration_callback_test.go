package database

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestOwnedMigrationCallbackUsesHostLedgerExactlyOnce(t *testing.T) {
	store, err := OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "module.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	applied := 0
	apply := func(ctx context.Context) error {
		applied++
		_, err := store.DB().ExecContext(ctx, `CREATE TABLE IF NOT EXISTS identity_owned_fixture (id TEXT PRIMARY KEY)`)
		return err
	}
	for range 2 {
		if err := store.ApplyOwnedMigration(t.Context(), "identity", 1, "identity_foundation", "identity-schema-v1", apply); err != nil {
			t.Fatal(err)
		}
	}
	if applied != 1 {
		t.Fatalf("source schema callback applied %d times", applied)
	}
	var clean int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _schema_migrations WHERE path='module_identity_000001_identity_foundation' AND kind='module:identity' AND dirty=FALSE`).Scan(&clean); err != nil || clean != 1 {
		t.Fatalf("host ledger clean rows=%d err=%v", clean, err)
	}
	var migrationLedgers int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name LIKE '%schema_migrations'`).Scan(&migrationLedgers); err != nil || migrationLedgers != 1 {
		t.Fatalf("migration ledger count=%d err=%v", migrationLedgers, err)
	}
}
