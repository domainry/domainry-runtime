package database

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/domainry/domainry-orm/query"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestRuntimeInstallationIdentityIsStableAcrossRestartAndDistinctAcrossDatabases(t *testing.T) {
	firstPath := filepath.Join(t.TempDir(), "installation-a.db")
	open := func(path string) *RuntimeStore {
		store, err := OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: path})
		if err != nil {
			t.Fatal(err)
		}
		if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
			_ = store.Close()
			t.Fatal(err)
		}
		return store
	}
	first := open(firstPath)
	identityA, err := first.InstallationIdentity(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	restarted := open(firstPath)
	identityRestarted, err := restarted.InstallationIdentity(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	other := open(filepath.Join(t.TempDir(), "installation-b.db"))
	defer other.Close()
	identityB, err := other.InstallationIdentity(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if identityA == "" || identityA != identityRestarted || identityA == identityB {
		t.Fatalf("installation identities first=%q restarted=%q other=%q", identityA, identityRestarted, identityB)
	}
}

func TestSQLiteInstallationRetainsWorkspaceAuthorityAfterDatabaseRelocation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "original.db")
	store, err := OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: path})
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
	statement, arguments, err := query.NewInsertBuilder(store.RuntimeRenderer(), "_workspaces").
		Columns("id", "canonical_code", "name", "status", "initial_installation_identity", "created_at", "updated_at").
		Values("workspace-restored", "restored", "Restored workspace", "active", identity, 1, 1).Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), statement, arguments...); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "restored.db")
	if err := os.Rename(path, destination); err != nil {
		t.Fatal(err)
	}
	restored, err := OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: destination})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	if err := restored.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	observed, err := restored.InstallationIdentity(t.Context())
	if err != nil || observed != identity {
		t.Fatalf("restored identity=%q expected=%q error=%v", observed, identity, err)
	}
	second, secondArgs, err := query.NewInsertBuilder(restored.RuntimeRenderer(), "_workspaces").
		Columns("id", "canonical_code", "name", "status", "initial_installation_identity", "created_at", "updated_at").
		Values("workspace-conflict", "conflict", "Conflicting authority", "active", strings.Repeat("a", 64), 1, 1).Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := restored.DB().ExecContext(t.Context(), second, secondArgs...); err != nil {
		t.Fatal(err)
	}
	if _, err := restored.InstallationIdentity(t.Context()); err == nil {
		t.Fatal("multiple initial authorities must be rejected")
	}
}
