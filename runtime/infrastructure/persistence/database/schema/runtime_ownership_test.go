package schema

import (
	"slices"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/schemaownership"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite"
)

func TestRuntimeSchemaOwnershipMatchesEveryFreshTableAndPrimaryKey(t *testing.T) {
	tables := RuntimeSchemaOwnership()
	if err := schemaownership.ValidateAll(tables); err != nil {
		t.Fatal(err)
	}
	if len(tables) != 16 {
		t.Fatalf("Runtime owned table count=%d, want 16", len(tables))
	}
	engine := sqlite.NewEngine()
	database := &schemaCaptureDB{}
	store := &schemaCaptureStore{db: database, driver: "sqlite", profile: engine, renderer: engine.SQLDialect().WithSchema("")}
	for _, ensure := range []func() error{
		func() error { return EnsureWorkspaceProvisioningSchema(t.Context(), store) },
		func() error { return EnsureApplicationSchema(t.Context(), store) },
		func() error { return EnsureEvidenceSchema(t.Context(), store) },
		func() error { return EnsureWorkflowProcessSchema(t.Context(), store) },
		func() error { return EnsureRateLimitSchema(t.Context(), store) },
	} {
		if err := ensure(); err != nil {
			t.Fatal(err)
		}
	}

	created := map[string]string{}
	for _, statement := range database.statements {
		for _, table := range tables {
			if strings.Contains(statement, `CREATE TABLE IF NOT EXISTS "`+table.Name+`"`) {
				if _, duplicate := created[table.Name]; duplicate {
					t.Fatalf("Runtime table %s is created more than once", table.Name)
				}
				created[table.Name] = statement
			}
		}
	}
	if len(created) != len(tables)-1 {
		t.Fatalf("fresh SQLite Runtime tables=%v ownership=%+v", created, tables)
	}
	for _, table := range tables {
		if table.Name == ManagedDatabaseCohortTable {
			continue // Managed server databases persist this installation identity; SQLite derives it from its canonical path.
		}
		statement, found := created[table.Name]
		if !found {
			t.Fatalf("Runtime table %s has ownership but no canonical DDL", table.Name)
		}
		if !ddlDeclaresPrimaryKey(statement, table.PrimaryKey) {
			t.Fatalf("Runtime table %s ownership primary key %v does not match DDL: %s", table.Name, table.PrimaryKey, statement)
		}
	}
	if !slices.Equal(RuntimeOwnedTables(), schemaownership.Names(tables)) {
		t.Fatalf("Runtime owned table names do not match ownership catalog")
	}
}

func TestRuntimeSchemaOwnershipReturnsIndependentValues(t *testing.T) {
	first, second := RuntimeSchemaOwnership(), RuntimeSchemaOwnership()
	first[0].PrimaryKey[0] = "changed"
	if second[0].PrimaryKey[0] == "changed" {
		t.Fatal("Runtime ownership primary keys share mutable storage")
	}
}

func ddlDeclaresPrimaryKey(statement string, columns []string) bool {
	quoted := make([]string, len(columns))
	for index, column := range columns {
		quoted[index] = `"` + column + `"`
	}
	if strings.Contains(statement, "PRIMARY KEY ("+strings.Join(quoted, ", ")+")") {
		return true
	}
	if len(quoted) != 1 {
		return false
	}
	start := strings.Index(statement, quoted[0]+" ")
	if start < 0 {
		return false
	}
	end := strings.Index(statement[start:], ",")
	if end < 0 {
		end = len(statement) - start
	}
	return strings.Contains(statement[start:start+end], "PRIMARY KEY")
}
