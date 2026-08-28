package bootstrap_test

import (
	"context"
	"errors"
	"testing"

	. "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestOpenContextRejectsCancelledBootstrapBeforeDatabaseIO(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	store, err := OpenContext(ctx, config.Config{DatabaseDriver: "sqlite", DBPath: t.TempDir() + "/never.db"})
	if store != nil {
		_ = store.Close()
		t.Fatal("cancelled bootstrap returned a store")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("OpenContext error = %v", err)
	}
}

func TestEnsureRuntimeSchemaRejectsCancelledBootstrap(t *testing.T) {
	store, err := OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: t.TempDir() + "/runtime.db"})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := store.EnsureRuntimeSchema(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("EnsureRuntimeSchema error = %v", err)
	}
}
