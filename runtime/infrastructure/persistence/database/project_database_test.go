package database_test

import (
	"path/filepath"
	"testing"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestEnsureProjectDatabaseCreatesSQLiteParentDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "crm.db")
	if err := database.EnsureProjectDatabase(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: path}); err != nil {
		t.Fatal(err)
	}
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureProjectDatabaseRejectsMissingMySQLDatabaseName(t *testing.T) {
	if err := database.EnsureProjectDatabase(t.Context(), config.Config{DatabaseDriver: "mysql", DatabaseDSN: "user:pass@tcp(localhost:3306)/"}); err == nil {
		t.Fatal("accepted MySQL DSN without project database name")
	}
}
