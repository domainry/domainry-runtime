package workspaceprovision

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-orm/query"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestCommercialConfigurationStoreResolvesOneWorkspaceCompanyAuthority(t *testing.T) {
	tests := []struct {
		name       string
		companyIDs []string
		want       string
		wantError  string
	}{
		{name: "one receipt", companyIDs: []string{"company-a"}, want: "company-a"},
		{name: "missing receipt", wantError: "missing or ambiguous"},
		{name: "ambiguous companies", companyIDs: []string{"company-a", "company-b"}, wantError: "missing or ambiguous"},
		{name: "equal replay receipts", companyIDs: []string{"company-a", "company-a"}, want: "company-a"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "commercial.db")})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = store.CloseContext(context.Background()) })
			if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Format(time.RFC3339Nano)
			insertTestBuilder(t, store, query.NewInsertBuilder(store.RuntimeRenderer(), "_workspace_commercial_configuration").Columns(
				"workspace_id", "plan", "included_user_limit", "max_user_limit", "included_customer_limit", "max_customer_limit", "included_store_limit", "max_stores", "contract_date", "billing_day",
				"billing_contact_name", "billing_contact_phone", "billing_contact_email", "billing_contact_address", "billing_contact_notes", "revision", "created_at", "updated_at",
			).Values("workspace-a", "standard", 1, 10, 0, 100, 1, 2, "2026-09-06", 1, "", "", "", "", "", 1, now, now))
			for index, companyID := range test.companyIDs {
				insertTestBuilder(t, store, query.NewInsertBuilder(store.RuntimeRenderer(), workspaceProvisioningReceiptTable).Columns(
					"request_id", "request_fingerprint", "workspace_id", "canonical_code", "admin_login_id", "must_change_password", "receipt_status", "company_id", "created_at",
				).Values("request-"+string(rune('a'+index)), "fingerprint", "workspace-a", "primary", "owner@example.test", true, "committed", companyID, now))
			}
			tx, err := store.DB().BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			result, err := NewCommercialConfigurationStore(store).LockWorkspaceCommercialConfiguration(database.WithActionExecutionTransaction(t.Context(), tx), "workspace-a")
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("result=%+v error=%v", result, err)
				}
				return
			}
			if err != nil || result.CompanyOrganizationID != test.want || result.MaxStores != 2 {
				t.Fatalf("result=%+v error=%v", result, err)
			}
		})
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
