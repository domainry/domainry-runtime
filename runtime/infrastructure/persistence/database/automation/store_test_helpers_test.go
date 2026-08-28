package automation

import (
	"path/filepath"
	"testing"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func openStoreForGeneratedListTest(t *testing.T) *database.RuntimeStore {
	t.Helper()
	return openRuntimeStore(t)
}

func openRuntimeStore(t *testing.T) *database.RuntimeStore {
	t.Helper()
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "automation.db")})
	if err != nil {
		t.Fatal(err)
	}
	return store
}
