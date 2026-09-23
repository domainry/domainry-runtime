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
			ormschema.Column("company_organization_id", ormschema.TextKey(255)),
			ormschema.Column("plan", ormschema.TextKey(191)).NotNull().DefaultValue(""),
			ormschema.Column("included_user_limit", ormschema.Integer()).NotNull().DefaultValue(0),
			ormschema.Column("max_user_limit", ormschema.Integer()).NotNull().DefaultValue(0),
			ormschema.Column("included_customer_limit", ormschema.Integer()).NotNull().DefaultValue(0),
			ormschema.Column("max_customer_limit", ormschema.Integer()).NotNull().DefaultValue(0),
			ormschema.Column("included_store_limit", ormschema.Integer()).NotNull().DefaultValue(0),
			ormschema.Column("max_stores", ormschema.Integer()).NotNull().DefaultValue(0),
			ormschema.Column("contract_date", ormschema.TextKey(32)).NotNull().DefaultValue(""),
			ormschema.Column("billing_day", ormschema.Integer()).NotNull().DefaultValue(0),
			ormschema.Column("billing_contact_name", ormschema.Text()).NotNull().DefaultValue(""),
			ormschema.Column("billing_contact_phone", ormschema.Text()).NotNull().DefaultValue(""),
			ormschema.Column("billing_contact_email", ormschema.Text()).NotNull().DefaultValue(""),
			ormschema.Column("billing_contact_address", ormschema.Text()).NotNull().DefaultValue(""),
			ormschema.Column("billing_contact_notes", ormschema.LongText()).NotNull().DefaultValue(""),
			ormschema.Column("commercial_revision", ormschema.Integer()).NotNull().DefaultValue(0),
			ormschema.Column("revision", ormschema.Integer()).NotNull().DefaultValue(1),
			ormschema.Column("created_at", ormschema.TextKey(64)).NotNull(),
			ormschema.Column("updated_at", ormschema.TextKey(64)).NotNull(),
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
		{table: "_workspaces", name: "uniq_workspace_company_organization", columns: []string{"company_organization_id"}},
	} {
		if err := store.CreateIndexIfMissing(ctx, index.table, index.name, true, index.columns...); err != nil {
			return err
		}
	}
	return nil
}
