package schema

import (
	"slices"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/schemaownership"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite"
)

func TestWorkflowSchemaOwnershipMatchesEveryFreshTableAndPrimaryKey(t *testing.T) {
	tables := WorkflowSchemaOwnership()
	if err := schemaownership.ValidateAll(tables); err != nil {
		t.Fatal(err)
	}
	engine := sqlite.NewEngine()
	database := &schemaCaptureDB{}
	store := &schemaCaptureStore{
		db: database, driver: "sqlite", profile: engine,
		renderer: engine.SQLDialect().WithSchema(""),
	}
	if err := EnsureEvidenceSchemaFor(t.Context(), store, EvidenceSchemaCapabilities{Workflow: true}); err != nil {
		t.Fatal(err)
	}
	if err := EnsureWorkflowProcessSchema(t.Context(), store); err != nil {
		t.Fatal(err)
	}

	created := map[string]string{}
	for _, statement := range database.statements {
		for _, table := range tables {
			if strings.Contains(statement, `CREATE TABLE IF NOT EXISTS "`+table.Name+`"`) {
				if _, duplicate := created[table.Name]; duplicate {
					t.Fatalf("Workflow table %s is created more than once", table.Name)
				}
				created[table.Name] = statement
			}
		}
	}
	if len(created) != len(tables) || !slices.Equal(WorkflowOwnedTables(), schemaownership.Names(tables)) {
		t.Fatalf("fresh Workflow tables=%v ownership=%+v", created, tables)
	}
	for _, table := range tables {
		statement, found := created[table.Name]
		if !found {
			t.Fatalf("Workflow table %s has ownership but no canonical DDL", table.Name)
		}
		quoted := make([]string, len(table.PrimaryKey))
		for index, column := range table.PrimaryKey {
			quoted[index] = `"` + column + `"`
		}
		if primaryKey := "PRIMARY KEY (" + strings.Join(quoted, ", ") + ")"; !strings.Contains(statement, primaryKey) {
			t.Fatalf("Workflow table %s ownership primary key %v does not match DDL: %s", table.Name, table.PrimaryKey, statement)
		}
	}
}

func TestWorkflowSchemaOwnershipReturnsIndependentValues(t *testing.T) {
	first, second := WorkflowSchemaOwnership(), WorkflowSchemaOwnership()
	first[0].PrimaryKey[0] = "changed"
	if second[0].PrimaryKey[0] == "changed" {
		t.Fatal("Workflow ownership primary keys share mutable storage")
	}
}
