package database

import (
	"database/sql"
	"testing"

	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/postgres"
	_ "modernc.org/sqlite"
)

func TestDatabaseReadinessUsesOnlyCapabilityFacts(t *testing.T) {
	profile := postgres.ConnectionProfile{Backend: postgres.BackendPostgres, Mode: postgres.ConnectionModeDirect, Schema: "domainry_runtime", TLS: true, MigrationConfigured: true}
	store := &RuntimeStore{
		db:                  openReadinessTestDatabase(t),
		postgresProfile:     &profile,
		migrationCompatible: true,
		postgresCapabilities: postgres.Capabilities{
			Database: "runtime", SchemaExists: true, SchemaUsage: true, TLS: true,
		},
		migratorCapabilities: postgres.Capabilities{Database: "runtime", SchemaExists: true, SchemaUsage: true, SchemaCreate: true, TLS: true},
	}
	readiness := store.DatabaseReadiness()
	if !readiness.Ready || readiness.Failure != "" || !readiness.ReadReady || !readiness.WriteReady || !readiness.MigrationCompatible || readiness.PoolDegraded || !readiness.MigrationConnectionReady {
		t.Fatalf("unexpected readiness: %+v", readiness)
	}
	status, ok := store.DatabaseStatus()
	if !ok || status.Backend != postgres.BackendPostgres || status.Schema != "domainry_runtime" {
		t.Fatalf("unexpected safe status: %+v, ok=%v", status, ok)
	}
}

func TestDatabaseReadinessSeparatesReadWriteMigrationAndPoolState(t *testing.T) {
	db := openReadinessTestDatabase(t)
	db.SetMaxOpenConns(1)
	tx, err := db.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	profile := postgres.ConnectionProfile{Backend: postgres.BackendPostgres, Schema: "domainry_runtime"}
	store := &RuntimeStore{db: db, postgresProfile: &profile, migrationCompatible: true, postgresCapabilities: postgres.Capabilities{SchemaExists: true, SchemaUsage: true}}
	readiness := store.DatabaseReadiness()
	if readiness.Ready || !readiness.ReadReady || !readiness.WriteReady || !readiness.MigrationCompatible || !readiness.PoolDegraded || readiness.Failure != "pool_degraded" {
		t.Fatalf("pool saturation was not separated: %+v", readiness)
	}
	store.migrationCompatible = false
	store.postgresCapabilities.ReadOnly = true
	readiness = store.DatabaseReadiness()
	if readiness.WriteReady || readiness.MigrationCompatible || readiness.Failure != "read_only" {
		t.Fatalf("read-only/migration state was not separated: %+v", readiness)
	}
}

func openReadinessTestDatabase(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestDatabaseReadinessFailsClosedForMissingSchema(t *testing.T) {
	profile := postgres.ConnectionProfile{Backend: postgres.BackendPostgres, Schema: "domainry_runtime", TLS: true}
	store := &RuntimeStore{db: openReadinessTestDatabase(t), postgresProfile: &profile, migrationCompatible: true, postgresCapabilities: postgres.Capabilities{TLS: true}}
	readiness := store.DatabaseReadiness()
	if readiness.Ready || readiness.Failure != postgres.FailureSchemaIncompatible {
		t.Fatalf("unexpected readiness: %+v", readiness)
	}
}

func TestPostgresTableIdentifierQualifiesOnlyRelations(t *testing.T) {
	store := &RuntimeStore{dialect: postgres.Dialect{}, databaseSchema: "domainry_runtime"}
	if got := store.TableIdentifier("business_records"); got != `"domainry_runtime"."business_records"` {
		t.Fatalf("TableIdentifier() = %s", got)
	}
	if got := store.Identifier("workspace_id"); got != `"workspace_id"` {
		t.Fatalf("Identifier() qualified a column: %s", got)
	}
}
