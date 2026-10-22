package schema

import (
	"fmt"

	ormschema "github.com/domainry/domainry-orm/schema"
)

// ManagedDatabaseCohortCreateStatement is the canonical physical definition
// for the managed-server database identity marker. SQLite products do not
// install this table, but schema-contract tooling can execute the same owner
// definition with a SQLite renderer for deterministic inspection.
func ManagedDatabaseCohortCreateStatement(renderer ormschema.Renderer) (string, error) {
	if renderer == nil {
		return "", fmt.Errorf("managed database cohort schema requires a renderer")
	}
	statement, _, err := ormschema.NewTableWithRequiredColumns(renderer, ManagedDatabaseCohortTable, physicalIDColumn()).IfNotExists().Columns(
		ormschema.Column("marker_id", ormschema.SmallInt()).NotNull(),
		ormschema.Column("contract_version", ormschema.Varchar(128)).NotNull(),
		ormschema.Column("database_identity_sha256", ormschema.Varchar(64)).NotNull(),
	).PrimaryKey("id").Unique("marker_id").Build()
	return statement, err
}
