package appschema

import (
	"path/filepath"
	"testing"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func metadataTestInstallationScope() principalmodel.SystemScope {
	return principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "test metadata repository")
}

func openStoreForGeneratedListTest(t *testing.T) *database.RuntimeStore {
	t.Helper()
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "metadata.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BindMetadata(newMetadataBindingStub()); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	return store
}

type metadataTestStore struct {
	ApplicationSchemaStore
	raw *database.RuntimeStore
}

func openStoreForMetadataTest(t *testing.T) *metadataTestStore {
	t.Helper()
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "app.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.BindMetadata(newMetadataBindingStub()); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		_ = store.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	return &metadataTestStore{ApplicationSchemaStore: NewApplicationSchemaStore(store), raw: store}
}
