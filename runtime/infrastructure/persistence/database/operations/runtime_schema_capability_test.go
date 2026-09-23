package operations

import (
	"path/filepath"
	"testing"
	"time"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestOperationsDiagnosticsSkipUnselectedNativeLeaseTables(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "minimal.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchemaFor(t.Context(), database.RuntimeSchemaCapabilities{}); err != nil {
		t.Fatal(err)
	}
	if _, err := NewOperationsStore(store).OperationsLeaseSnapshot(t.Context(), "runtime-minimal", time.Now()); err != nil {
		t.Fatalf("minimal Runtime diagnostics queried an unselected lease table: %v", err)
	}
}
