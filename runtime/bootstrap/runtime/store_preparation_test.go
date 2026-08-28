package runtime

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestPrepareRuntimeStoreOpensSQLiteAndEnsuresSchema(t *testing.T) {
	store, err := prepareRuntimeStore(t.Context(), config.Config{
		DatabaseDriver: "sqlite",
		DBPath:         filepath.Join(t.TempDir(), "runtime.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if _, err := store.DB().ExecContext(t.Context(), "SELECT 1"); err != nil {
		t.Fatalf("prepared store is not usable: %v", err)
	}
}

func TestPrepareRuntimeStoreRejectsUnsupportedDriverAndCancelledContext(t *testing.T) {
	if store, err := prepareRuntimeStore(t.Context(), config.Config{DatabaseDriver: "unsupported"}); err == nil || store != nil {
		t.Fatalf("unsupported driver store=%v error=%v", store, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if store, err := prepareRuntimeStore(ctx, config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "cancelled.db")}); err == nil || store != nil {
		t.Fatalf("cancelled context store=%v error=%v", store, err)
	}
}

func TestEnsureRuntimeStoreSchemaRejectsClosedStore(t *testing.T) {
	store, err := prepareRuntimeStore(t.Context(), config.Config{
		DatabaseDriver: "sqlite",
		DBPath:         filepath.Join(t.TempDir(), "closed.db"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	if prepared, err := completeRuntimeStoreSchemaPreparation(t.Context(), store); err == nil || prepared != nil {
		t.Fatal("closed store schema preparation must fail")
	}
}
