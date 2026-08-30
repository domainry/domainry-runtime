package appschema

import (
	"context"
	"errors"
	"testing"

	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestManifestStorageBootstrapRejectsCancelledContext(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: t.TempDir() + "/metadata.db"})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	legacy := NewApplicationSchemaStore(store)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	manifest := manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: "never_created"}}}
	if err := legacy.SyncManifestStorage(ctx, manifest); !errors.Is(err, context.Canceled) {
		t.Fatalf("SyncManifestStorage error = %v", err)
	}
	if _, err := legacy.ApplicationSchemaMigrationPlan(ctx, manifest); !errors.Is(err, context.Canceled) {
		t.Fatalf("ApplicationSchemaMigrationPlan error = %v", err)
	}
}

func TestManifestMetadataBootstrapRejectsCancelledContext(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: t.TempDir() + "/manifest.db"})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	legacy := NewApplicationSchemaStore(store)
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	manifest := manifestmodel.ManifestSchema{TemplateID: "never", Version: "1", Objects: []definitionmodel.ObjectSchema{{Key: "never_created"}}}
	if err := legacy.EnsureManifestMetadata(ctx, manifest); !errors.Is(err, context.Canceled) {
		t.Fatalf("EnsureManifestMetadata error = %v", err)
	}
	if _, err := legacy.LoadManifestMetadata(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("LoadManifestMetadata error = %v", err)
	}
}

func TestManifestIdentitySeedVersionRejectsCancelledContext(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: t.TempDir() + "/identity-seed.db"})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	legacy := NewApplicationSchemaStore(store)
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := legacy.ManifestIdentitySeedSyncedVersion(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("ManifestIdentitySeedSyncedVersion error = %v", err)
	}
	if err := legacy.SetManifestIdentitySeedSyncedVersion(ctx, "never-written"); !errors.Is(err, context.Canceled) {
		t.Fatalf("SetManifestIdentitySeedSyncedVersion error = %v", err)
	}
}

func TestApplicationDefinitionCompatibilityAPIsRejectCancelledContext(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: t.TempDir() + "/metadata-definition.db"})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	legacy := NewApplicationSchemaStore(store)
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := legacy.ListApplicationDefinitions(ctx, "object", ""); !errors.Is(err, context.Canceled) {
		t.Fatalf("ListApplicationDefinitions error = %v", err)
	}
	if _, err := legacy.ApplyDefinitionMutations(ctx, metadataTestInstallationScope(), nil, nil, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("ApplyDefinitionMutations error = %v", err)
	}
}
