package database

import (
	"path/filepath"
	"testing"

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
