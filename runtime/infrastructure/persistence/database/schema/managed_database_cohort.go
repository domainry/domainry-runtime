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
	return "CREATE TABLE IF NOT EXISTS " + renderer.Table(ManagedDatabaseCohortTable) + " (" +
		renderer.Identifier("marker_id") + " SMALLINT NOT NULL PRIMARY KEY, " +
		renderer.Identifier("contract_version") + " VARCHAR(128) NOT NULL, " +
		renderer.Identifier("database_identity_sha256") + " CHAR(64) NOT NULL)", nil
}
