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

func TestCommercialConfigurationStoreResolvesWorkspaceLimitsWithoutOrganizationBinding(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "commercial.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.CloseContext(context.Background()) })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	insertTestBuilder(t, store, query.NewInsertBuilder(store.RuntimeRenderer(), "_workspaces").Columns(
		"id", "canonical_code", "name", "status",
		"plan", "included_user_limit", "max_user_limit", "included_customer_limit", "max_customer_limit", "included_store_limit", "max_stores", "contract_date", "billing_day",
		"billing_contact_name", "billing_contact_phone", "billing_contact_email", "billing_contact_address", "billing_contact_notes", "commercial_revision", "revision", "created_at", "updated_at",
	).Values("workspace-a", "primary", "Primary", "active", "standard", 1, 10, 0, 100, 1, 2, "2026-09-06", 1, "", "", "", "", "", 1, 1, now, now))
	tx, err := store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	result, err := NewCommercialConfigurationStore(store).LockWorkspaceCommercialConfiguration(database.WithActionExecutionTransaction(t.Context(), tx), "workspace-a")
	if err != nil || result.MaxStores != 2 || result.Revision != 1 {
		t.Fatalf("result=%+v error=%v", result, err)
	}
}

func insertTestBuilder(t *testing.T, store *database.RuntimeStore, builder *query.InsertBuilder) {
	t.Helper()
	statement, arguments, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), statement, arguments...); err != nil {
		t.Fatal(err)
	}
}
