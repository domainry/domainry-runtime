package runtimehost

import (
	"github.com/domainry/domainry-foundation/schemaownership"
	databaseschema "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/schema"
)

// RuntimeSchemaOwnership returns Runtime's source-owned durable-table contract.
// It exposes descriptive ownership metadata only; schema DDL and Store behavior
// remain inside Runtime's persistence packages.
func RuntimeSchemaOwnership() []schemaownership.Table {
	return databaseschema.RuntimeSchemaOwnership()
}
