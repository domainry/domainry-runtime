package database_test

import (
	"strings"
	"testing"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestWorkspaceRLSIsPostgresOnlyAndFailsClosedWithoutWorkspace(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: t.TempDir() + "/rls.db", IntegrationSecretKey: "rls-contract-key", DatabaseRLSEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	status := store.WorkspaceRLSStatus(t.Context())
	if status.Enabled || len(status.CoveredTables) != 0 || len(status.MissingTables) != 0 {
		t.Fatalf("non-PostgreSQL runtime must not claim RLS coverage: %+v", status)
	}

	tx, err := store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	err = database.SetLocalWorkspaceRLSContext(t.Context(), tx)
	if err == nil || !strings.Contains(err.Error(), "requires workspace_id") {
		t.Fatalf("RLS transaction binding must fail closed without workspace: %v", err)
	}
}
