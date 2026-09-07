package schema

import (
	"context"
	"fmt"

	ormschema "github.com/domainry/domainry-orm/schema"
)

// The projection header is Runtime-owned and migrates under the host's single
// _schema_migrations ledger. Existing installations retain UTC until their
// next compiled application manifest explicitly publishes a business zone.
func EnsureApplicationTimeZoneSchema(ctx context.Context, store Store) error {
	const table = "_application_schema_projection"
	shape, found, err := store.RuntimeProfile().InspectModuleSchemaTable(ctx, store.SchemaDB(), store.RuntimeRenderer(), store.DatabaseSchema(), table)
	if err != nil {
		return fmt.Errorf("inspect application time zone column: %w", err)
	}
	if !found {
		return fmt.Errorf("application time zone migration requires %s", table)
	}
	for _, column := range shape.Columns {
		if column.Name == "time_zone" {
			return nil
		}
	}
	statement, args, err := ormschema.NewAddColumn(store.RuntimeRenderer(), table,
		ormschema.Column("time_zone", ormschema.TextKey(255)).NotNull().DefaultValue("UTC")).Build()
	if err != nil {
		return fmt.Errorf("build application time zone column: %w", err)
	}
	if _, err := store.SchemaDB().ExecContext(ctx, statement, args...); err != nil {
		return fmt.Errorf("add application time zone column: %w", err)
	}
	return nil
}
