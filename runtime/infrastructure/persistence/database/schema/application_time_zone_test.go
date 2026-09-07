package schema

import (
	"context"
	"strings"
	"testing"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	persistencedriver "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/driver"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/mysql"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/postgres"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite"
)

type timeZoneSchemaProfile struct {
	persistencedriver.EngineProfile
	columns []persistencedriver.ModuleSchemaColumn
	found   bool
	err     error
}

func (p timeZoneSchemaProfile) InspectModuleSchemaTable(context.Context, persistencedriver.SchemaDatabase, ormdialect.Renderer, string, string) (persistencedriver.ModuleSchemaTable, bool, error) {
	return persistencedriver.ModuleSchemaTable{Columns: p.columns}, p.found, p.err
}

type timeZoneSchemaStore struct {
	Store
	database SQLDatabase
	profile  timeZoneSchemaProfile
	renderer ormdialect.Renderer
}

func (s timeZoneSchemaStore) SchemaDB() SQLDatabase                           { return s.database }
func (s timeZoneSchemaStore) RuntimeProfile() persistencedriver.EngineProfile { return s.profile }
func (s timeZoneSchemaStore) RuntimeRenderer() ormdialect.Renderer {
	return s.renderer
}
func (s timeZoneSchemaStore) DatabaseSchema() string { return "" }

func TestApplicationTimeZoneMigrationRendersAllDialectsAndPropagatesFailures(t *testing.T) {
	for _, test := range []struct {
		profile  persistencedriver.EngineProfile
		renderer ormdialect.Renderer
	}{
		{sqlite.NewEngine(), sqlite.NewEngine().SQLDialect().WithSchema("")},
		{postgres.NewEngine(), postgres.NewEngine().SQLDialect().WithSchema("")},
		{mysql.NewEngine(), mysql.NewEngine().SQLDialect().WithSchema("")},
	} {
		t.Run(string(test.profile.Name()), func(t *testing.T) {
			state := &schemaSQLState{}
			database := openSchemaScriptedDB(state)
			t.Cleanup(func() { _ = database.Close() })
			store := timeZoneSchemaStore{database: database, renderer: test.renderer, profile: timeZoneSchemaProfile{EngineProfile: test.profile, found: true}}
			if err := EnsureApplicationTimeZoneSchema(t.Context(), store); err != nil {
				t.Fatal(err)
			}
			if len(state.execQueries) != 1 || !strings.Contains(state.execQueries[0], "time_zone") || !strings.Contains(state.execQueries[0], "DEFAULT 'UTC'") {
				t.Fatalf("migration DDL=%v", state.execQueries)
			}
			store.profile.columns = []persistencedriver.ModuleSchemaColumn{{Name: "time_zone"}}
			if err := EnsureApplicationTimeZoneSchema(t.Context(), store); err != nil || len(state.execQueries) != 1 {
				t.Fatalf("repeat migration: %v", err)
			}
			store.profile.columns = nil
			store.profile.found = false
			if err := EnsureApplicationTimeZoneSchema(t.Context(), store); err == nil {
				t.Fatal("missing header accepted")
			}
			store.profile.found, store.profile.err = true, errSchemaSQL
			if err := EnsureApplicationTimeZoneSchema(t.Context(), store); err == nil {
				t.Fatal("inspection failure ignored")
			}
			store.profile.err = nil
			state.execSteps = []schemaSQLExecStep{{err: errSchemaSQL}}
			if err := EnsureApplicationTimeZoneSchema(t.Context(), store); err == nil {
				t.Fatal("DDL failure ignored")
			}
		})
	}
}
