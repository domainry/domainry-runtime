package schema

import (
	"context"
	"fmt"

	ormschema "github.com/domainry/domainry-orm/schema"
)

// DefinitionUpgradeReceiptsTable records every column addition and backfill
// executed by a definition version upgrade so a crashed upgrade can resume
// from the last completed step instead of losing the backfill forever.
const DefinitionUpgradeReceiptsTable = "_application_schema_upgrade_receipts"

// EnsureDefinitionUpgradeReceiptsSchema is Runtime schema 025. The table is
// host-owned and migrates under the single _schema_migrations ledger.
func EnsureDefinitionUpgradeReceiptsSchema(ctx context.Context, store Store) error {
	statement, args, err := ormschema.NewTable(store.RuntimeRenderer(), DefinitionUpgradeReceiptsTable).IfNotExists().Columns(
		ormschema.Column("id", ormschema.TextKey(128)).NotNull(),
		ormschema.Column("from_version", ormschema.TextKey(255)).NotNull().DefaultValue(""),
		ormschema.Column("to_version", ormschema.TextKey(255)).NotNull().DefaultValue(""),
		ormschema.Column("object_key", ormschema.TextKey(255)).NotNull(),
		ormschema.Column("column_key", ormschema.TextKey(255)).NotNull().DefaultValue(""),
		ormschema.Column("step_key", ormschema.Text()).NotNull(),
		ormschema.Column("status", ormschema.TextKey(32)).NotNull(),
		ormschema.Column("error_code", ormschema.TextKey(255)).NotNull().DefaultValue(""),
		ormschema.Column("backup_id", ormschema.TextKey(255)).NotNull().DefaultValue(""),
		ormschema.Column("started_at", ormschema.TextKey(64)).NotNull(),
		ormschema.Column("completed_at", ormschema.TextKey(64)).NotNull().DefaultValue(""),
	).PrimaryKey("id").Build()
	if err != nil {
		return fmt.Errorf("build %s: %w", DefinitionUpgradeReceiptsTable, err)
	}
	if _, err := store.SchemaDB().ExecContext(ctx, statement, args...); err != nil {
		return fmt.Errorf("create %s: %w", DefinitionUpgradeReceiptsTable, err)
	}
	return nil
}
