package workspaceprovision

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/domainry/domainry-orm/query"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestIdentityCatalogResolvesOnlyActiveLocalWorkspaces(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "catalog.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.CloseContext(context.Background()) })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	installation, err := store.InstallationIdentity(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	insertWorkspaceAdministrationFixture(t, store.DB(), "physical-a", "public-a", "A", installation, now)
	insertWorkspaceAdministrationFixture(t, store.DB(), "physical-b", "public-b", "B", "", now)
	insertWorkspaceAdministrationFixture(t, store.DB(), "foreign-id", "foreign-code", "Foreign", "another-installation", now)
	for ref, want := range map[string]string{"public-a": "physical-a", "physical-a": "physical-a", "public-b": "physical-b", "physical-b": "physical-b"} {
		id, err := ResolveActiveIdentityWorkspace(t.Context(), store, ref)
		if err != nil || id != want {
			t.Fatalf("%s resolved incorrectly: %v", ref, err)
		}
	}
	for _, ref := range []string{"", "unknown", "foreign-id", "foreign-code"} {
		if _, err := ResolveActiveIdentityWorkspace(t.Context(), store, ref); err == nil {
			t.Fatalf("accepted %q", ref)
		}
	}
	for _, status := range []string{"suspended", "active"} {
		statement, args, err := query.NewUpdateBuilder(store.RuntimeRenderer(), "_workspaces").Set("status", status).Where(query.Equal("id", "physical-b")).Build()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.DB().ExecContext(t.Context(), statement, args...); err != nil {
			t.Fatal(err)
		}
		_, err = ResolveActiveIdentityWorkspace(t.Context(), store, "public-b")
		if (err == nil) != (status == "active") {
			t.Fatalf("status %s resolved incorrectly: %v", status, err)
		}
	}
}
