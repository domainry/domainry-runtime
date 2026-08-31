package schema

import (
	"context"
	"fmt"

	ormschema "github.com/domainry/domainry-orm/schema"
)

func EnsureWorkspaceProvisioningSchema(ctx context.Context, store Store) error {
	tables := []struct {
		name    string
		columns []ormschema.ColumnDefinition
		primary []string
	}{
		{name: "_workspaces", columns: []ormschema.ColumnDefinition{
			ormschema.Column("id", ormschema.TextKey(255)).NotNull(), ormschema.Column("canonical_code", ormschema.TextKey(191)).NotNull(),
			ormschema.Column("name", ormschema.Text()).NotNull(), ormschema.Column("status", ormschema.TextKey(64)).NotNull(),
			ormschema.Column("created_at", ormschema.TextKey(64)).NotNull(), ormschema.Column("updated_at", ormschema.TextKey(64)).NotNull(),
		}, primary: []string{"id"}},
		{name: "_tenant_registry", columns: []ormschema.ColumnDefinition{
			ormschema.Column("id", ormschema.TextKey(255)).NotNull(), ormschema.Column("workspace_id", ormschema.TextKey(255)).NotNull(),
			ormschema.Column("canonical_code", ormschema.TextKey(191)).NotNull(), ormschema.Column("created_at", ormschema.TextKey(64)).NotNull(),
			ormschema.Column("updated_at", ormschema.TextKey(64)).NotNull(),
		}, primary: []string{"id"}},
		{name: "_workspace_configuration", columns: []ormschema.ColumnDefinition{
			ormschema.Column("workspace_id", ormschema.TextKey(255)).NotNull(), ormschema.Column("configuration_json", ormschema.LongText()).NotNull(),
			ormschema.Column("created_at", ormschema.TextKey(64)).NotNull(), ormschema.Column("updated_at", ormschema.TextKey(64)).NotNull(),
		}, primary: []string{"workspace_id"}},
		{name: "_workspace_provisioning_receipts", columns: []ormschema.ColumnDefinition{
			ormschema.Column("request_id", ormschema.TextKey(255)).NotNull(), ormschema.Column("request_fingerprint", ormschema.TextKey(191)).NotNull(),
			ormschema.Column("tenant_registry_id", ormschema.TextKey(255)).NotNull(), ormschema.Column("workspace_id", ormschema.TextKey(255)).NotNull(),
			ormschema.Column("canonical_code", ormschema.TextKey(191)).NotNull(), ormschema.Column("admin_login_id", ormschema.Text()).NotNull(),
			ormschema.Column("must_change_password", ormschema.Boolean()).NotNull().DefaultValue(false),
			ormschema.Column("application_projection_ids_json", ormschema.LongText()).NotNull().DefaultValue("{}"),
			ormschema.Column("created_at", ormschema.TextKey(64)).NotNull(),
		}, primary: []string{"request_id"}},
		{name: "_tenant_installation", columns: []ormschema.ColumnDefinition{
			ormschema.Column("installation_key", ormschema.TextKey(64)).NotNull(),
			ormschema.Column("tenant_registry_id", ormschema.TextKey(255)).NotNull(),
			ormschema.Column("workspace_id", ormschema.TextKey(255)).NotNull(),
			ormschema.Column("initialized_at", ormschema.TextKey(64)).NotNull(),
		}, primary: []string{"installation_key"}},
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
		{table: "_tenant_registry", name: "uniq_tenant_code", columns: []string{"canonical_code"}},
		{table: "_tenant_registry", name: "uniq_tenant_workspace", columns: []string{"workspace_id"}},
		{table: "_tenant_installation", name: "uniq_tenant_installation_workspace", columns: []string{"workspace_id"}},
		{table: "_tenant_installation", name: "uniq_tenant_installation_tenant", columns: []string{"tenant_registry_id"}},
	} {
		if err := store.CreateIndexIfMissing(ctx, index.table, index.name, true, index.columns...); err != nil {
			return err
		}
	}
	return nil
}
