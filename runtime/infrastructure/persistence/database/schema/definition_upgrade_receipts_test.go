package schema

import (
	"strings"
	"testing"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/mysql"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/postgres"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite"
)

func TestDefinitionUpgradeReceiptsSchemaRendersAllDialectsAndPropagatesFailures(t *testing.T) {
	for _, test := range []struct {
		name     string
		renderer ormdialect.Renderer
		keyType  string
	}{
		{"sqlite", sqlite.NewEngine().SQLDialect().WithSchema(""), "TEXT"},
		{"postgres", postgres.NewEngine().SQLDialect().WithSchema(""), "TEXT"},
		{"mysql", mysql.NewEngine().SQLDialect().WithSchema(""), "VARCHAR(255)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := &schemaSQLState{}
			database := openSchemaScriptedDB(state)
			t.Cleanup(func() { _ = database.Close() })
			store := timeZoneSchemaStore{database: database, renderer: test.renderer}
			if err := EnsureDefinitionUpgradeReceiptsSchema(t.Context(), store); err != nil {
				t.Fatal(err)
			}
			if len(state.execQueries) != 1 {
				t.Fatalf("DDL=%v", state.execQueries)
			}
			ddl := state.execQueries[0]
			for _, required := range []string{"CREATE TABLE IF NOT EXISTS", DefinitionUpgradeReceiptsTable, "step_key", "backup_id", "PRIMARY KEY", "to_version", "completed_at"} {
				if !strings.Contains(ddl, required) {
					t.Fatalf("DDL %q lacks %q", ddl, required)
				}
			}
			if !strings.Contains(ddl, "from_version") || !strings.Contains(ddl, test.keyType) {
				t.Fatalf("DDL %q lacks dialect key type %q", ddl, test.keyType)
			}
			state.execSteps = []schemaSQLExecStep{{err: errSchemaSQL}}
			if err := EnsureDefinitionUpgradeReceiptsSchema(t.Context(), store); err == nil {
				t.Fatal("DDL failure ignored")
			}
		})
	}
}
