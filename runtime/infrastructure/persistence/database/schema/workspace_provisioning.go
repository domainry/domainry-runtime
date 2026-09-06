package schema

import (
	"context"
	"fmt"

	ormschema "github.com/domainry/domainry-orm/schema"
)

// EnsureWorkspaceProvisioningSchema creates only Workspace-owned V3
// authorities. Legacy registry, installation, opaque-configuration, and
// projection-receipt tables are migration inputs and are never created here.
func EnsureWorkspaceProvisioningSchema(ctx context.Context, store Store) error {
	tables := []struct {
		name    string
		columns []ormschema.ColumnDefinition
		primary []string
	}{
		{name: "_workspaces", columns: []ormschema.ColumnDefinition{
			ormschema.Column("id", ormschema.TextKey(255)).NotNull(),
			ormschema.Column("canonical_code", ormschema.TextKey(191)).NotNull(),
			ormschema.Column("name", ormschema.Text()).NotNull(),
			ormschema.Column("status", ormschema.TextKey(64)).NotNull(),
			// Only the initial Workspace carries this stable host identity. A
			// nullable unique value avoids a second Workspace mapping table.
			ormschema.Column("initial_installation_identity", ormschema.TextKey(64)),
			ormschema.Column("revision", ormschema.Integer()).NotNull().DefaultValue(1),
			ormschema.Column("created_at", ormschema.TextKey(64)).NotNull(),
			ormschema.Column("updated_at", ormschema.TextKey(64)).NotNull(),
		}, primary: []string{"id"}},
		{name: "_workspace_commercial_configuration", columns: []ormschema.ColumnDefinition{
			ormschema.Column("workspace_id", ormschema.TextKey(255)).NotNull(),
			ormschema.Column("plan", ormschema.TextKey(191)).NotNull(),
			ormschema.Column("included_user_limit", ormschema.Integer()).NotNull(),
			ormschema.Column("max_user_limit", ormschema.Integer()).NotNull(),
			ormschema.Column("included_customer_limit", ormschema.Integer()).NotNull(),
			ormschema.Column("max_customer_limit", ormschema.Integer()).NotNull(),
			ormschema.Column("included_store_limit", ormschema.Integer()).NotNull(),
			ormschema.Column("max_stores", ormschema.Integer()).NotNull(),
			ormschema.Column("contract_date", ormschema.TextKey(32)).NotNull(),
			ormschema.Column("billing_day", ormschema.Integer()).NotNull(),
			ormschema.Column("billing_contact_name", ormschema.Text()).NotNull(),
			ormschema.Column("billing_contact_phone", ormschema.Text()).NotNull(),
			ormschema.Column("billing_contact_email", ormschema.Text()).NotNull(),
			ormschema.Column("billing_contact_address", ormschema.Text()).NotNull(),
			ormschema.Column("billing_contact_notes", ormschema.LongText()).NotNull(),
			ormschema.Column("revision", ormschema.Integer()).NotNull().DefaultValue(1),
			ormschema.Column("created_at", ormschema.TextKey(64)).NotNull(),
			ormschema.Column("updated_at", ormschema.TextKey(64)).NotNull(),
		}, primary: []string{"workspace_id"}},
		{name: "_workspace_provisioning_receipts_v3", columns: []ormschema.ColumnDefinition{
			ormschema.Column("request_id", ormschema.TextKey(255)).NotNull(),
			ormschema.Column("request_fingerprint", ormschema.TextKey(191)).NotNull(),
			ormschema.Column("workspace_id", ormschema.TextKey(255)).NotNull(),
			ormschema.Column("canonical_code", ormschema.TextKey(191)).NotNull(),
			ormschema.Column("admin_login_id", ormschema.Text()).NotNull(),
			ormschema.Column("must_change_password", ormschema.Boolean()).NotNull().DefaultValue(false),
			ormschema.Column("receipt_status", ormschema.TextKey(64)).NotNull(),
			ormschema.Column("identity_receipt_id", ormschema.TextKey(255)),
			ormschema.Column("identity_contract_version", ormschema.TextKey(191)),
			ormschema.Column("identity_contract_hash", ormschema.TextKey(64)),
			ormschema.Column("company_id", ormschema.TextKey(255)),
			ormschema.Column("first_store_id", ormschema.TextKey(255)),
			ormschema.Column("initial_admin_user_id", ormschema.TextKey(255)),
			ormschema.Column("created_at", ormschema.TextKey(64)).NotNull(),
		}, primary: []string{"request_id"}},
		{name: "_workspace_administration_receipts_v1", columns: []ormschema.ColumnDefinition{
			ormschema.Column("id", ormschema.TextKey(255)).NotNull(),
			ormschema.Column("request_fingerprint", ormschema.TextKey(64)).NotNull(),
			ormschema.Column("action_key", ormschema.TextKey(191)).NotNull(),
			ormschema.Column("workspace_id", ormschema.TextKey(255)).NotNull(),
			ormschema.Column("result_json", ormschema.LongText()).NotNull(),
			ormschema.Column("created_at", ormschema.TextKey(64)).NotNull(),
		}, primary: []string{"id"}},
	}
	for _, table := range tables {
		statement, arguments, err := ormschema.NewTable(store.RuntimeRenderer(), table.name).IfNotExists().Columns(table.columns...).PrimaryKey(table.primary...).Build()
		if err != nil {
			return fmt.Errorf("build %s schema: %w", table.name, err)
		}
		if _, err := store.SchemaDB().ExecContext(ctx, statement, arguments...); err != nil {
			return fmt.Errorf("create %s schema: %w", table.name, err)
		}
	}
	for _, index := range []struct {
		table, name string
		columns     []string
	}{
		{table: "_workspaces", name: "uniq_workspace_code", columns: []string{"canonical_code"}},
		{table: "_workspaces", name: "uniq_initial_workspace_installation", columns: []string{"initial_installation_identity"}},
	} {
		if err := store.CreateIndexIfMissing(ctx, index.table, index.name, true, index.columns...); err != nil {
			return err
		}
	}
	return nil
}
