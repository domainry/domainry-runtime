package workspaceprovision

import (
	"os"
	"path/filepath"
	"testing"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestRestoredSQLiteLoadsTheOriginalInitialWorkspace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "acceptance.db")
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	identity, err := store.InstallationIdentity(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	insertWorkspaceAdministrationFixture(t, store.DB(), "workspace-acceptance", "acceptance", "Acceptance", identity, 1790294400000)
	expected, found, err := LoadInstallation(t.Context(), store)
	if err != nil || !found {
		t.Fatalf("initial Workspace not loaded: %v", err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	restoredPath := filepath.Join(t.TempDir(), "persistent-acceptance.db")
	if err := os.Rename(path, restoredPath); err != nil {
		t.Fatal(err)
	}
	restored, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: restoredPath})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if err := restored.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	observed, found, err := LoadInstallation(t.Context(), restored)
	if err != nil || !found || observed != expected {
		t.Fatalf("restored installation=%+v expected=%+v found=%t error=%v", observed, expected, found, err)
	}
}
