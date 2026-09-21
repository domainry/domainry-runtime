package schema_test

import (
	"path/filepath"
	"testing"

	. "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestMySQLTextDefaultsUseExpressions(t *testing.T) {
	store, err := OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "schema.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.SetEngineForTesting("mysql"); err != nil {
		t.Fatal(err)
	}
	for input, want := range map[string]string{
		"TEXT NOT NULL DEFAULT ''":                 "TEXT NOT NULL DEFAULT ('')",
		"TEXT NOT NULL DEFAULT '[]'":               "TEXT NOT NULL DEFAULT ('[]')",
		"TEXT NOT NULL DEFAULT '{}'":               "TEXT NOT NULL DEFAULT ('{}')",
		"TEXT NOT NULL DEFAULT '{\"matches\":[]}'": "TEXT NOT NULL DEFAULT ('{\"matches\":[]}')",
		"TEXT NOT NULL":                            "TEXT NOT NULL",
	} {
		if got := store.RuntimeColumnDefinition(input); got != want {
			t.Fatalf("runtimeColumnDefinition(%q) = %q, want %q", input, got, want)
		}
	}
}
