package schema

import (
	"context"
	"strings"
	"testing"

	ormdialect "github.com/domainry/domainry-orm/dialect"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/mysql"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/postgres"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/sqlite"
)

type routeStepSchemaIndex struct {
	table, name string
	unique      bool
	columns     []string
}

type routeStepSchemaStore struct {
	Store
	database SQLDatabase
	renderer ormdialect.Renderer
	indexes  []routeStepSchemaIndex
	indexErr error
}

func (s *routeStepSchemaStore) SchemaDB() SQLDatabase { return s.database }
func (s *routeStepSchemaStore) RuntimeRenderer() ormdialect.Renderer {
	return s.renderer
}
func (s *routeStepSchemaStore) CreateIndexIfMissing(_ context.Context, table, name string, unique bool, columns ...string) error {
	s.indexes = append(s.indexes, routeStepSchemaIndex{table: table, name: name, unique: unique, columns: columns})
	return s.indexErr
}

func TestWorkflowRouteStepsSchemaRendersAllDialectsAndPropagatesFailures(t *testing.T) {
	for _, test := range []struct {
		name     string
		renderer ormdialect.Renderer
		keyType  string
	}{
		{"sqlite", sqlite.NewEngine().SQLDialect().WithSchema(""), "TEXT"},
		{"postgres", postgres.NewEngine().SQLDialect().WithSchema(""), "TEXT"},
		{"mysql", mysql.NewEngine().SQLDialect().WithSchema(""), "VARCHAR(128)"},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := &schemaSQLState{}
			database := openSchemaScriptedDB(state)
			t.Cleanup(func() { _ = database.Close() })
			store := &routeStepSchemaStore{database: database, renderer: test.renderer}
			if err := EnsureWorkflowRouteStepsSchema(t.Context(), store); err != nil {
				t.Fatal(err)
			}
			if len(state.execQueries) != 1 {
				t.Fatalf("DDL=%v", state.execQueries)
			}
			ddl := state.execQueries[0]
			for _, required := range []string{
				"CREATE TABLE IF NOT EXISTS", WorkflowRouteStepsTable, "workspace_id", "process_id", "node_id", "step_no", "step_key",
				"title", "mode", "required_approvals", "status", "assignee_snapshot_json", "configured_by", "configured_at",
				"configure_source", "node_instance_id", "created_at", "updated_at", "PRIMARY KEY",
			} {
				if !strings.Contains(ddl, required) {
					t.Fatalf("DDL %q lacks %q", ddl, required)
				}
			}
			if !strings.Contains(ddl, test.keyType) {
				t.Fatalf("DDL %q lacks dialect key type %q", ddl, test.keyType)
			}
			if test.name == "mysql" && (strings.Contains(ddl, "`title` TEXT NOT NULL DEFAULT") || strings.Contains(ddl, "`assignee_snapshot_json` TEXT NOT NULL DEFAULT")) {
				t.Fatalf("MySQL workflow route DDL contains an unsupported TEXT default: %q", ddl)
			}
			if len(store.indexes) != 2 {
				t.Fatalf("indexes=%#v", store.indexes)
			}
			unique := store.indexes[0]
			if unique.name != "uniq_workflow_route_step_no" || !unique.unique || strings.Join(unique.columns, ",") != "workspace_id,process_id,step_no" {
				t.Fatalf("unique index=%#v", unique)
			}
			byStatus := store.indexes[1]
			if byStatus.name != "idx_workflow_route_step_status" || byStatus.unique || strings.Join(byStatus.columns, ",") != "workspace_id,process_id,status" {
				t.Fatalf("status index=%#v", byStatus)
			}
			store.indexErr = errSchemaSQL
			store.indexes = nil
			state.execSteps = nil
			if err := EnsureWorkflowRouteStepsSchema(t.Context(), store); err == nil {
				t.Fatal("index failure ignored")
			}
			state.execSteps = []schemaSQLExecStep{{err: errSchemaSQL}}
			if err := EnsureWorkflowRouteStepsSchema(t.Context(), store); err == nil {
				t.Fatal("DDL failure ignored")
			}
		})
	}
}
