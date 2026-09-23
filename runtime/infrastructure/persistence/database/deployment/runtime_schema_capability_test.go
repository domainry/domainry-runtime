package deployment

import (
	"path/filepath"
	"testing"
	"time"

	deploymentmodel "github.com/domainry/domainry-runtime/runtime/domain/deployment/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestRuntimeStatusSkipsUnselectedNativeReceiptTables(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "minimal.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.EnsureRuntimeSchemaFor(t.Context(), database.RuntimeSchemaCapabilities{}); err != nil {
		t.Fatal(err)
	}
	status := NewRuntimeStatusStore(store)
	if _, err := status.IdempotencyOperationalStatus(t.Context(), "workspace-minimal", time.Now()); err != nil {
		t.Fatalf("minimal Runtime status queried an unselected receipt table: %v", err)
	}
	if _, err := status.RunIdempotencyCleanup(t.Context(), deploymentmodel.IdempotencyCleanupRequest{LeaseOwner: "runtime-minimal", Now: time.Now()}); err != nil {
		t.Fatalf("minimal Runtime cleanup queried an unselected receipt table: %v", err)
	}
}
