package database

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-orm/query"
	ormschema "github.com/domainry/domainry-orm/schema"
	workspaceschema "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/schema"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

const completeLegacyCommercialConfiguration = `{"plan":"standard","included_user_limit":1,"max_user_limit":10,"included_customer_limit":0,"max_customer_limit":100,"included_store_limit":1,"max_stores":2,"contract_date":"2026-09-06","billing_day":1,"billing_contact_name":"","billing_contact_phone":"","billing_contact_email":"","billing_contact_address":"","billing_contact_notes":""}`

func TestLegacyWorkspaceAuthorityMigrationIsTypedFailClosedAndRestartSafe(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	store := openLegacyWorkspaceMigrationStore(t, path)
	createLegacyWorkspaceAuthorityTables(t, store)
	seedLegacyWorkspaceAuthorities(t, store, completeLegacyCommercialConfiguration, true)
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	var installationIdentity string
	if err := store.DB().QueryRowContext(t.Context(), `SELECT "initial_installation_identity" FROM "_workspaces" WHERE "id" = ?`, "workspace-a").Scan(&installationIdentity); err != nil || strings.TrimSpace(installationIdentity) == "" {
		t.Fatalf("installation identity=%q error=%v", installationIdentity, err)
	}
	var plan string
	var maxStores int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT "plan","max_stores" FROM "_workspace_commercial_configuration" WHERE "workspace_id" = ?`, "workspace-a").Scan(&plan, &maxStores); err != nil || plan != "standard" || maxStores != 2 {
		t.Fatalf("typed commercial plan=%q max_stores=%d error=%v", plan, maxStores, err)
	}
	var status string
	var companyID sql.NullString
	if err := store.DB().QueryRowContext(t.Context(), `SELECT "receipt_status","company_id" FROM "_workspace_provisioning_receipts_v3" WHERE "request_id" = ?`, "legacy-request").Scan(&status, &companyID); err != nil || status != "identity_graph_adjudication_required" || companyID.Valid {
		t.Fatalf("receipt status=%q company=%#v error=%v", status, companyID, err)
	}
	if count := migrationTestCount(t, store, `_schema_migrations`); count < 1 {
		t.Fatalf("host migration ledger count=%d", count)
	}
	if err := store.CloseContext(t.Context()); err != nil {
		t.Fatal(err)
	}
	restarted := openLegacyWorkspaceMigrationStore(t, path)
	if err := restarted.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	if count := migrationTestCount(t, restarted, `_workspace_commercial_configuration`); count != 1 {
		t.Fatalf("restart duplicated commercial rows=%d", count)
	}
	if count := migrationTestCount(t, restarted, `_workspace_provisioning_receipts_v3`); count != 1 {
		t.Fatalf("restart duplicated receipts=%d", count)
	}
}

func TestLegacyWorkspaceAuthorityMigrationRejectsInventedDefaultsAndMissingMappings(t *testing.T) {
	for _, test := range []struct {
		name          string
		configuration string
		seedRegistry  bool
		want          string
	}{
		{name: "opaque empty configuration", configuration: `{}`, seedRegistry: true, want: "adjudication required"},
		{name: "missing registry mapping", configuration: completeLegacyCommercialConfiguration, seedRegistry: false, want: "missing retired registry mapping"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := openLegacyWorkspaceMigrationStore(t, filepath.Join(t.TempDir(), "legacy-invalid.db"))
			createLegacyWorkspaceAuthorityTables(t, store)
			seedLegacyWorkspaceAuthorities(t, store, test.configuration, false)
			if !test.seedRegistry {
				deleteBuilder := query.NewDeleteBuilder(store.RuntimeRenderer(), "_tenant_registry").Where(query.Equal("workspace_id", "workspace-a"))
				statement, arguments, err := deleteBuilder.Build()
				if err != nil {
					t.Fatal(err)
				}
				if _, err := store.DB().ExecContext(t.Context(), statement, arguments...); err != nil {
					t.Fatal(err)
				}
			}
			if err := store.EnsureRuntimeSchema(t.Context()); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("migration error=%v want=%q", err, test.want)
			}
		})
	}
}

func openLegacyWorkspaceMigrationStore(t *testing.T, path string) *RuntimeStore {
	t.Helper()
	store, err := OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: path, MigrationBackupDir: filepath.Join(filepath.Dir(path), "backups")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.CloseContext(context.Background()) })
	return store
}

func createLegacyWorkspaceAuthorityTables(t *testing.T, store *RuntimeStore) {
	t.Helper()
	tables := []struct {
		name    string
		columns []ormschema.ColumnDefinition
		primary []string
	}{
		{"_workspaces", []ormschema.ColumnDefinition{ormschema.Column("id", ormschema.TextKey(255)).NotNull(), ormschema.Column("canonical_code", ormschema.TextKey(191)).NotNull(), ormschema.Column("name", ormschema.Text()).NotNull(), ormschema.Column("status", ormschema.TextKey(64)).NotNull(), ormschema.Column("created_at", ormschema.TextKey(64)).NotNull(), ormschema.Column("updated_at", ormschema.TextKey(64)).NotNull()}, []string{"id"}},
		{"_tenant_registry", []ormschema.ColumnDefinition{ormschema.Column("id", ormschema.TextKey(255)).NotNull(), ormschema.Column("workspace_id", ormschema.TextKey(255)).NotNull(), ormschema.Column("canonical_code", ormschema.TextKey(191)).NotNull(), ormschema.Column("created_at", ormschema.TextKey(64)).NotNull(), ormschema.Column("updated_at", ormschema.TextKey(64)).NotNull()}, []string{"id"}},
		{"_workspace_configuration", []ormschema.ColumnDefinition{ormschema.Column("workspace_id", ormschema.TextKey(255)).NotNull(), ormschema.Column("configuration_json", ormschema.LongText()).NotNull(), ormschema.Column("created_at", ormschema.TextKey(64)).NotNull(), ormschema.Column("updated_at", ormschema.TextKey(64)).NotNull()}, []string{"workspace_id"}},
		{"_tenant_installation", []ormschema.ColumnDefinition{ormschema.Column("installation_key", ormschema.TextKey(64)).NotNull(), ormschema.Column("tenant_registry_id", ormschema.TextKey(255)).NotNull(), ormschema.Column("workspace_id", ormschema.TextKey(255)).NotNull(), ormschema.Column("initialized_at", ormschema.TextKey(64)).NotNull()}, []string{"installation_key"}},
		{"_workspace_provisioning_receipts", []ormschema.ColumnDefinition{ormschema.Column("request_id", ormschema.TextKey(255)).NotNull(), ormschema.Column("request_fingerprint", ormschema.TextKey(191)).NotNull(), ormschema.Column("tenant_registry_id", ormschema.TextKey(255)).NotNull(), ormschema.Column("workspace_id", ormschema.TextKey(255)).NotNull(), ormschema.Column("canonical_code", ormschema.TextKey(191)).NotNull(), ormschema.Column("admin_login_id", ormschema.Text()).NotNull(), ormschema.Column("must_change_password", ormschema.Boolean()).NotNull(), ormschema.Column("application_projection_ids_json", ormschema.LongText()).NotNull(), ormschema.Column("created_at", ormschema.TextKey(64)).NotNull()}, []string{"request_id"}},
	}
	for _, table := range tables {
		statement, arguments, err := ormschema.NewTable(store.RuntimeRenderer(), table.name).Columns(table.columns...).PrimaryKey(table.primary...).Build()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := store.SchemaDB().ExecContext(t.Context(), statement, arguments...); err != nil {
			t.Fatal(err)
		}
	}
}

func seedLegacyWorkspaceAuthorities(t *testing.T, store *RuntimeStore, configuration string, receipt bool) {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, builder := range []*query.InsertBuilder{
		query.NewInsertBuilder(store.RuntimeRenderer(), "_workspaces").Columns("id", "canonical_code", "name", "status", "created_at", "updated_at").Values("workspace-a", "primary", "Primary", "active", now, now),
		query.NewInsertBuilder(store.RuntimeRenderer(), "_tenant_registry").Columns("id", "workspace_id", "canonical_code", "created_at", "updated_at").Values("registry-a", "workspace-a", "primary", now, now),
		query.NewInsertBuilder(store.RuntimeRenderer(), "_workspace_configuration").Columns("workspace_id", "configuration_json", "created_at", "updated_at").Values("workspace-a", configuration, now, now),
		query.NewInsertBuilder(store.RuntimeRenderer(), "_tenant_installation").Columns("installation_key", "tenant_registry_id", "workspace_id", "initialized_at").Values("primary", "registry-a", "workspace-a", now),
	} {
		executeMigrationTestInsert(t, store, builder)
	}
	if receipt {
		executeMigrationTestInsert(t, store, query.NewInsertBuilder(store.RuntimeRenderer(), "_workspace_provisioning_receipts").Columns("request_id", "request_fingerprint", "tenant_registry_id", "workspace_id", "canonical_code", "admin_login_id", "must_change_password", "application_projection_ids_json", "created_at").Values("legacy-request", "legacy-fingerprint", "registry-a", "workspace-a", "primary", "owner@example.test", true, `{}`, now))
	}
}

func executeMigrationTestInsert(t *testing.T, store *RuntimeStore, builder *query.InsertBuilder) {
	t.Helper()
	statement, arguments, err := builder.Build()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), statement, arguments...); err != nil {
		t.Fatal(err)
	}
}

func migrationTestCount(t *testing.T, store *RuntimeStore, table string) int {
	t.Helper()
	var count int
	// domainry-orm intentionally does not expose aggregate expressions on the
	// basic select builder; this read-only test assertion uses COUNT(*).
	if err := store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+store.TableIdentifier(table)).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

var _ workspaceschema.Store = (*RuntimeStore)(nil)
