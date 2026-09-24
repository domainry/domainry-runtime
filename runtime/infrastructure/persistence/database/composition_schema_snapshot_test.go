package database

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	agentmodule "github.com/domainry/domainry-agent/module"
	auditmodule "github.com/domainry/domainry-audit/module"
	dataexchangemodule "github.com/domainry/domainry-data-exchange/module"
	sharedartifact "github.com/domainry/domainry-foundation/artifact"
	shareddefinition "github.com/domainry/domainry-foundation/definition"
	sharedoperation "github.com/domainry/domainry-foundation/operation"
	"github.com/domainry/domainry-foundation/schemaownership"
	sharedsubject "github.com/domainry/domainry-foundation/subjectlifecycle"
	sharedworkerscope "github.com/domainry/domainry-foundation/workerscope"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	identitymodule "github.com/domainry/domainry-identity/module"
	integrationmodule "github.com/domainry/domainry-integration/module"
	knowledgemodule "github.com/domainry/domainry-knowledge/module"
	lifecyclemodule "github.com/domainry/domainry-lifecycle/module"
	metadatamodule "github.com/domainry/domainry-metadata/module"
	notificationmodule "github.com/domainry/domainry-notification/module"
	ormmigration "github.com/domainry/domainry-orm/migration"
	reportmodule "github.com/domainry/domainry-report/module"
	runtimeschema "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/schema"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	schedulermodule "github.com/domainry/domainry-scheduler/module"
	todomodule "github.com/domainry/domainry-todo/module"
)

type schemaComposition string

const (
	minimalComposition     schemaComposition = "minimal"
	fullNoAgentComposition schemaComposition = "full_no_agent"
	fullAgentComposition   schemaComposition = "full_agent"
)

func TestSQLiteCompositionSchemasMatchExactSourceOwnedSnapshotsAndBudgets(t *testing.T) {
	tests := []struct {
		composition schemaComposition
		minimum     int
		maximum     int
	}{
		{minimalComposition, 30, 45},
		{fullNoAgentComposition, 70, 85},
		{fullAgentComposition, 85, 105},
	}
	for _, test := range tests {
		t.Run(string(test.composition), func(t *testing.T) {
			store := openCompositionSchemaStore(t, test.composition)
			actual := physicalSQLiteTables(t, store)
			expected := expectedCompositionTables(t, test.composition)
			if !slices.Equal(actual, expected) {
				t.Fatalf("%s physical schema drift\nactual=%v\nexpected=%v", test.composition, actual, expected)
			}
			if len(actual) < test.minimum || len(actual) > test.maximum {
				t.Fatalf("%s physical table count=%d outside budget %d-%d", test.composition, len(actual), test.minimum, test.maximum)
			}
		})
	}
}

func openCompositionSchemaStore(t *testing.T, composition schemaComposition) *RuntimeStore {
	t.Helper()
	store, err := OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), string(composition)+".db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	capabilities := RuntimeSchemaCapabilities{}
	if composition != minimalComposition {
		capabilities = FullRuntimeSchemaCapabilities()
	}
	if err := store.EnsureRuntimeSchemaFor(t.Context(), capabilities); err != nil {
		t.Fatal(err)
	}
	definitionMigrations, err := shareddefinition.SchemaMigrations(store.Driver(), store.DatabaseSchema())
	applyORMMigrations(t, store, shareddefinition.MigrationOwner, mustMigrations(t, definitionMigrations, err))
	subjectMigrations, err := sharedsubject.SchemaMigrations(store.Driver(), store.DatabaseSchema())
	applyORMMigrations(t, store, sharedsubject.MigrationOwner, mustMigrations(t, subjectMigrations, err))
	metadataMigrations, err := metadatamodule.SchemaMigrations(store.Driver(), store.DatabaseSchema())
	applyORMMigrations(t, store, ownerOf(t, metadatamodule.SchemaOwnership()), mustMigrations(t, metadataMigrations, err))
	openCompositionIdentity(t, store)
	if composition == minimalComposition {
		return store
	}
	integrationMigrations, err := integrationmodule.SchemaMigrations(store.Driver(), store.DatabaseSchema())
	applyORMMigrations(t, store, ownerOf(t, integrationmodule.SchemaOwnership()), mustMigrations(t, integrationMigrations, err))
	notificationMigrations, err := notificationmodule.SchemaMigrations(store.Driver(), store.DatabaseSchema(), "")
	applyORMMigrations(t, store, ownerOf(t, notificationmodule.SchemaOwnership()), mustMigrations(t, notificationMigrations, err))
	lifecycleMigrations, err := lifecyclemodule.SchemaMigrations(store.SQLRenderer)
	applyORMMigrations(t, store, ownerOf(t, lifecyclemodule.SchemaOwnership()), mustMigrations(t, lifecycleMigrations, err))
	schedulerMigrations, err := schedulermodule.SchemaMigrations(store.Driver(), store.DatabaseSchema())
	applyORMMigrations(t, store, ownerOf(t, schedulermodule.SchemaOwnership()), mustMigrations(t, schedulerMigrations, err))
	applyDataExchangeMigrations(t, store)
	reportMigrations, err := reportmodule.SchemaMigrations(store.Driver(), store.DatabaseSchema())
	applyORMMigrations(t, store, ownerOf(t, reportmodule.SchemaOwnership()), mustMigrations(t, reportMigrations, err))
	if composition == fullAgentComposition {
		agentMigrations, err := agentmodule.SchemaMigrations(store.Driver(), store.DatabaseSchema())
		applyORMMigrations(t, store, ownerOf(t, agentmodule.SchemaOwnership()), mustMigrations(t, agentMigrations, err))
		knowledgeMigrations, err := knowledgemodule.SchemaMigrations(store.SQLRenderer)
		applyORMMigrations(t, store, knowledgemodule.MigrationOwner, mustMigrations(t, knowledgeMigrations, err))
		todoMigrations, err := todomodule.SchemaMigrations(store.SQLRenderer)
		applyORMMigrations(t, store, todomodule.MigrationOwner, mustMigrations(t, todoMigrations, err))
	}
	return store
}

func openCompositionIdentity(t *testing.T, store *RuntimeStore) {
	t.Helper()
	binding, err := identitymodule.NewFactory(identitymodule.Options{
		DatabaseDriver:  store.Driver(),
		AuditFactory:    auditmodule.NewFactory(auditmodule.Options{}),
		MetadataFactory: metadatamodule.NewFactory(),
	}).OpenWithDatabase(
		t.Context(), identitysdk.ApplicationRef{WorkspaceID: "workspace-primary", ApplicationKey: "runtime"},
		identitysdk.DatabaseHandle{Pool: store.DB(), Driver: store.Driver(), Schema: store.DatabaseSchema(), Migrations: store, ModuleMigrations: store},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = binding.Close(context.Background()) })
}

func applyORMMigrations(t *testing.T, store *RuntimeStore, owner string, migrations []ormmigration.Migration) {
	t.Helper()
	if err := store.ApplyORMOwnedMigrations(t.Context(), owner, migrations); err != nil {
		t.Fatalf("apply %s migrations: %v", owner, err)
	}
}

func mustMigrations[T ~[]ormmigration.Migration](t *testing.T, migrations T, err error) []ormmigration.Migration {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
	return []ormmigration.Migration(migrations)
}

var compositionMigrationNamePattern = regexp.MustCompile(`[^a-z0-9_-]+`)

func applyDataExchangeMigrations(t *testing.T, store *RuntimeStore) {
	t.Helper()
	migrations, err := dataexchangemodule.SchemaMigrations(store.Driver(), store.DatabaseSchema())
	if err != nil {
		t.Fatal(err)
	}
	values := make([]ormmigration.Migration, len(migrations))
	for index, migration := range migrations {
		name := compositionMigrationNamePattern.ReplaceAllString(strings.ToLower(strings.TrimSpace(migration.ID)), "_")
		values[index] = ormmigration.Migration{Version: uint(index + 1), Name: name, Statements: []string{migration.SQL}}
	}
	applyORMMigrations(t, store, ownerOf(t, dataexchangemodule.SchemaOwnership()), values)
}

func ownerOf(t *testing.T, tables []schemaownership.Table) string {
	t.Helper()
	if len(tables) == 0 {
		t.Fatal("schema ownership is empty")
	}
	owner := tables[0].Owner
	for _, table := range tables[1:] {
		if table.Owner != owner {
			t.Fatalf("schema group has multiple migration owners: %s and %s", owner, table.Owner)
		}
	}
	return owner
}

func physicalSQLiteTables(t *testing.T, store *RuntimeStore) []string {
	t.Helper()
	rows, err := store.DB().QueryContext(t.Context(), `SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	tables := []string{}
	for rows.Next() {
		var table string
		if err := rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, table)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return tables
}

func expectedCompositionTables(t *testing.T, composition schemaComposition) []string {
	t.Helper()
	tables := []string{"_schema_migrations"}
	appendOwned := func(groups ...[]schemaownership.Table) {
		for _, group := range groups {
			for _, table := range group {
				tables = append(tables, table.Name)
			}
		}
	}
	appendOwned(shareddefinition.SchemaOwnership(), sharedoperation.SchemaOwnership(), sharedartifact.SchemaOwnership(), sharedsubject.SchemaOwnership(), sharedworkerscope.SchemaOwnership())
	for _, table := range runtimeschema.RuntimeSchemaOwnership() {
		if table.Name == runtimeschema.ManagedDatabaseCohortTable {
			continue
		}
		if composition == minimalComposition {
			switch table.Name {
			case "_automation_runs", "_release_cohorts", "_release_instances", "_workflow_executions", "_workflow_node_instances", "_workflow_process_events", "_workflow_process_instances", "_workflow_route_steps", "_workflow_tasks":
				continue
			}
		}
		tables = append(tables, table.Name)
	}
	appendOwned(auditmodule.SchemaOwnership(), identitymodule.SchemaOwnership(), metadatamodule.SchemaOwnership())
	if composition != minimalComposition {
		appendOwned(integrationmodule.SchemaOwnership(), notificationmodule.SchemaOwnership(), lifecyclemodule.SchemaOwnership(), schedulermodule.SchemaOwnership(), dataexchangemodule.SchemaOwnership(), reportmodule.SchemaOwnership())
	}
	if composition == fullAgentComposition {
		appendOwned(agentmodule.SchemaOwnership(), knowledgemodule.SchemaOwnership(), todomodule.SchemaOwnership())
	}
	sort.Strings(tables)
	for index := 1; index < len(tables); index++ {
		if tables[index] == tables[index-1] {
			t.Fatalf("expected composition registers duplicate table %s", tables[index])
		}
	}
	return tables
}

func TestCompositionSnapshotCountsRemainStable(t *testing.T) {
	for composition, want := range map[schemaComposition]int{
		minimalComposition: 41, fullNoAgentComposition: 77, fullAgentComposition: 105,
	} {
		if got := len(expectedCompositionTables(t, composition)); got != want {
			t.Fatalf("%s target table count=%d want=%d", composition, got, want)
		}
	}
}

func TestCompositionSnapshotDoesNotClassifyMonitoringAsPersistent(t *testing.T) {
	for _, composition := range []schemaComposition{minimalComposition, fullNoAgentComposition, fullAgentComposition} {
		for _, table := range expectedCompositionTables(t, composition) {
			if strings.HasPrefix(table, "_monitoring_") {
				t.Fatal(fmt.Sprintf("%s unexpectedly owns Monitoring table %s", composition, table))
			}
		}
	}
}
