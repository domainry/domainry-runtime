package database

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/secrets"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestRuntimeStoreNilAndDialectContracts(t *testing.T) {
	var nilStore *RuntimeStore
	if err := nilStore.Close(); err != nil {
		t.Fatalf("nil close: %v", err)
	}
	if err := nilStore.CloseContext(t.Context()); err != nil {
		t.Fatalf("nil context close: %v", err)
	}
	if nilStore.SQLMetrics() != nil || nilStore.OperationalMetrics() != nil || nilStore.IdempotencyMetrics(t.Context()) != nil {
		t.Fatal("nil store exposed metrics")
	}
	if _, ok := nilStore.DatabaseStatus(); ok {
		t.Fatal("nil store exposed database status")
	}
	if readiness := nilStore.DatabaseReadiness(); readiness.Ready || readiness.ReadReady || readiness.WriteReady || readiness.MigrationCompatible {
		t.Fatalf("nil readiness = %#v", readiness)
	}

	store := &RuntimeStore{}
	if readiness := store.DatabaseReadiness(); readiness.Ready || readiness.ReadReady {
		t.Fatalf("empty readiness = %#v", readiness)
	}
	if got := SQLIdentifier("safe_name"); got != `"safe_name"` || !validSQLIdentifier("safe_name") || validSQLIdentifier("unsafe-name") {
		t.Fatalf("identifier=%q valid=%v unsafe=%v", got, validSQLIdentifier("safe_name"), validSQLIdentifier("unsafe-name"))
	}
	if err := store.SetDialectForTesting("mysql"); err != nil || store.metadataIDColumnType() != "VARCHAR(191)" {
		t.Fatalf("mysql dialect type=%q error=%v", store.metadataIDColumnType(), err)
	}
	if err := store.SetDialectForTesting("postgres"); err != nil || store.metadataIDColumnType() != "TEXT" {
		t.Fatalf("postgres dialect type=%q error=%v", store.metadataIDColumnType(), err)
	}
	if err := store.SetDialectForTesting("oracle"); err == nil {
		t.Fatal("unsupported dialect accepted")
	}
}

func TestRuntimeStoreOpenContextAndKeyProviderEdges(t *testing.T) {
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := OpenContext(canceled, config.Config{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled open = %v", err)
	}
	if _, err := OpenContext(t.Context(), config.Config{DatabaseDriver: "oracle"}); err == nil {
		t.Fatal("unsupported database driver accepted")
	}
	if _, err := OpenContextWithKeyProvider(t.Context(), config.Config{}, nil); err == nil {
		t.Fatal("nil key provider accepted")
	}
	keyRing, err := secrets.NewMemoryKeyRing(secrets.Key{ID: "active", Material: make([]byte, 32)})
	if err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "runtime.db"), IntegrationSecretKey: "runtime-key"}
	store, err := OpenContextWithKeyProvider(t.Context(), cfg, keyRing)
	if err != nil {
		t.Fatal(err)
	}
	if store.secretKeyProvider != keyRing || store.SQLMetrics() == nil || store.OperationalMetrics() == nil || store.IdempotencyMetrics(t.Context()) == nil {
		t.Fatal("store dependencies were not initialized")
	}
	if status, ok := store.DatabaseStatus(); ok {
		t.Fatalf("sqlite status=%#v ok=%v", status, ok)
	}
	if readiness := store.DatabaseReadiness(); !readiness.Ready || !readiness.ReadReady || !readiness.WriteReady || !readiness.MigrationCompatible {
		t.Fatalf("sqlite readiness = %#v", readiness)
	}
	if err := store.CloseContext(t.Context()); err != nil {
		t.Fatalf("close context: %v", err)
	}
}

func TestRuntimeStoreSystemUpdateContract(t *testing.T) {
	cfg := config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "updates.db"), IntegrationSecretKey: "runtime-key"}
	store, err := OpenContext(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB().ExecContext(t.Context(), `CREATE TABLE "edge_updates" ("id" TEXT PRIMARY KEY, "name" TEXT, "status" TEXT)`); err != nil {
		t.Fatal(err)
	}
	if err := store.insertSystemRowContext(t.Context(), "edge_updates", []string{"id", "name", "status"}, []any{"one", "before", "draft"}); err != nil {
		t.Fatal(err)
	}
	if err := store.updateSystemRowContext(t.Context(), "edge_updates", "one", []string{"name", "status"}, []any{"after", "active"}); err != nil {
		t.Fatal(err)
	}
	var name, status string
	if err := store.DB().QueryRowContext(t.Context(), `SELECT name, status FROM edge_updates WHERE id = 'one'`).Scan(&name, &status); err != nil || name != "after" || status != "active" {
		t.Fatalf("name=%q status=%q error=%v", name, status, err)
	}
	if err := store.updateSystemRowContext(t.Context(), "missing_table", "one", []string{"name"}, []any{"after"}); err == nil || !strings.Contains(err.Error(), "update missing_table") {
		t.Fatalf("missing update error = %v", err)
	}
}
