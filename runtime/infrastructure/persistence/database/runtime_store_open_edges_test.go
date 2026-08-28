package database

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/postgres"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-runtime/runtime/platform/secrets"
	"github.com/domainry/domainry-runtime/runtime/platform/telemetry"
)

type runtimeOpenDialectStub struct {
	name                 string
	dsnErr, configureErr error
}

func (stub runtimeOpenDialectStub) Name() string      { return stub.name }
func (stub runtimeOpenDialectStub) SQLDriver() string { return "scripted" }
func (stub runtimeOpenDialectStub) DSN(config.Config) (string, error) {
	return "scripted", stub.dsnErr
}
func (stub runtimeOpenDialectStub) Configure(context.Context, *sql.DB, string) error {
	return stub.configureErr
}
func (stub runtimeOpenDialectStub) Identifier(value string) string { return value }
func (stub runtimeOpenDialectStub) Placeholder(int) string         { return "?" }
func (stub runtimeOpenDialectStub) SchemaMigrationSQL() string     { return "" }

type runtimePostgresProfileStub struct {
	profile               postgres.ConnectionProfile
	db, migrationDB       *sql.DB
	openErr, migrationErr error
	probeResults          []postgres.Capabilities
	probeErrors           []error
	validateErr           error
}

func (stub *runtimePostgresProfileStub) Open(*telemetry.SQLMetrics) (*sql.DB, error) {
	return stub.db, stub.openErr
}
func (stub *runtimePostgresProfileStub) OpenMigration(*telemetry.SQLMetrics) (*sql.DB, error) {
	return stub.migrationDB, stub.migrationErr
}
func (stub *runtimePostgresProfileStub) ProbeWithBackoff(context.Context, *sql.DB) (postgres.Capabilities, error) {
	var result postgres.Capabilities
	var err error
	if len(stub.probeResults) > 0 {
		result, stub.probeResults = stub.probeResults[0], stub.probeResults[1:]
	}
	if len(stub.probeErrors) > 0 {
		err, stub.probeErrors = stub.probeErrors[0], stub.probeErrors[1:]
	}
	return result, err
}
func (stub *runtimePostgresProfileStub) ValidateRuntimeCapabilities(postgres.Capabilities, postgres.Capabilities) error {
	return stub.validateErr
}
func (stub *runtimePostgresProfileStub) Profile() *postgres.ConnectionProfile { return &stub.profile }

func runtimeOpenTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db := openDatabaseScriptedDB(&databaseSQLState{})
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func runtimeOpenTestDependencies(selectedDialect dialect, profile runtimePostgresProfile) runtimeOpenDependencies {
	return runtimeOpenDependencies{
		dialect: func(string) (dialect, error) { return selectedDialect, nil },
		postgresProfile: func(config.Config) (runtimePostgresProfile, error) {
			return profile, nil
		},
		observedSQL: func(string, string, string, *telemetry.SQLMetrics) (*sql.DB, error) {
			return nil, errors.New("observed SQL not configured")
		},
		keyRing: func(active secrets.Key, decryptOnly ...secrets.Key) (secrets.KeyProvider, error) {
			return secrets.NewMemoryKeyRing(active, decryptOnly...)
		},
	}
}

func TestOpenContextNonPostgresDependencyFailures(t *testing.T) {
	cfg := config.Config{DatabaseMigrationMode: "verify"}
	for _, test := range []struct {
		name string
		deps runtimeOpenDependencies
	}{
		{"dsn", runtimeOpenDependencies{dialect: func(string) (dialect, error) {
			return runtimeOpenDialectStub{name: "sqlite", dsnErr: errDatabaseSQL}, nil
		}}},
		{"open", runtimeOpenDependencies{dialect: func(string) (dialect, error) { return runtimeOpenDialectStub{name: "sqlite"}, nil }, observedSQL: func(string, string, string, *telemetry.SQLMetrics) (*sql.DB, error) { return nil, errDatabaseSQL }}},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := openContextWithDependencies(t.Context(), cfg, test.deps); err == nil {
				t.Fatal("expected open error")
			}
		})
	}
	db := runtimeOpenTestDB(t)
	deps := runtimeOpenTestDependencies(runtimeOpenDialectStub{name: "sqlite", configureErr: errDatabaseSQL}, nil)
	deps.observedSQL = func(string, string, string, *telemetry.SQLMetrics) (*sql.DB, error) { return db, nil }
	if _, err := openContextWithDependencies(t.Context(), cfg, deps); !errors.Is(err, errDatabaseSQL) {
		t.Fatalf("configure error=%v", err)
	}
}

func TestOpenContextPostgresLifecycleFailures(t *testing.T) {
	cfg := config.Config{DatabaseMigrationMode: "verify"}
	profileFactoryError := runtimeOpenTestDependencies(runtimeOpenDialectStub{name: "postgres"}, nil)
	profileFactoryError.postgresProfile = func(config.Config) (runtimePostgresProfile, error) { return nil, errDatabaseSQL }
	if _, err := openContextWithDependencies(t.Context(), cfg, profileFactoryError); !errors.Is(err, errDatabaseSQL) {
		t.Fatalf("profile error=%v", err)
	}

	tests := []struct {
		name    string
		profile func(*testing.T) *runtimePostgresProfileStub
	}{
		{"query open", func(t *testing.T) *runtimePostgresProfileStub {
			return &runtimePostgresProfileStub{openErr: errDatabaseSQL}
		}},
		{"migration open", func(t *testing.T) *runtimePostgresProfileStub {
			return &runtimePostgresProfileStub{profile: postgres.ConnectionProfile{MigrationConfigured: true}, db: runtimeOpenTestDB(t), migrationErr: errDatabaseSQL}
		}},
		{"migration ping", func(t *testing.T) *runtimePostgresProfileStub {
			db := runtimeOpenTestDB(t)
			migration := runtimeOpenTestDB(t)
			_ = migration.Close()
			return &runtimePostgresProfileStub{profile: postgres.ConnectionProfile{MigrationConfigured: true}, db: db, migrationDB: migration}
		}},
		{"query probe", func(t *testing.T) *runtimePostgresProfileStub {
			return &runtimePostgresProfileStub{db: runtimeOpenTestDB(t), probeErrors: []error{errDatabaseSQL}}
		}},
		{"query probe with migration", func(t *testing.T) *runtimePostgresProfileStub {
			return &runtimePostgresProfileStub{profile: postgres.ConnectionProfile{MigrationConfigured: true}, db: runtimeOpenTestDB(t), migrationDB: runtimeOpenTestDB(t), probeErrors: []error{errDatabaseSQL}}
		}},
		{"migration probe", func(t *testing.T) *runtimePostgresProfileStub {
			return &runtimePostgresProfileStub{profile: postgres.ConnectionProfile{MigrationConfigured: true}, db: runtimeOpenTestDB(t), migrationDB: runtimeOpenTestDB(t), probeResults: []postgres.Capabilities{{}}, probeErrors: []error{nil, errDatabaseSQL}}
		}},
		{"validate", func(t *testing.T) *runtimePostgresProfileStub {
			return &runtimePostgresProfileStub{db: runtimeOpenTestDB(t), probeResults: []postgres.Capabilities{{}}, validateErr: errDatabaseSQL}
		}},
		{"validate with migration", func(t *testing.T) *runtimePostgresProfileStub {
			return &runtimePostgresProfileStub{profile: postgres.ConnectionProfile{MigrationConfigured: true}, db: runtimeOpenTestDB(t), migrationDB: runtimeOpenTestDB(t), probeResults: []postgres.Capabilities{{}, {}}, validateErr: errDatabaseSQL}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			profile := test.profile(t)
			deps := runtimeOpenTestDependencies(runtimeOpenDialectStub{name: "postgres"}, profile)
			if _, err := openContextWithDependencies(t.Context(), cfg, deps); err == nil {
				t.Fatal("expected lifecycle error")
			}
		})
	}
}

func TestOpenContextMigrationFailureClosesMigrationDatabase(t *testing.T) {
	path := t.TempDir() + "/missing.sql"
	cfg := config.Config{DatabaseMigrationMode: "verify", MigrationSQL: path}
	profile := &runtimePostgresProfileStub{profile: postgres.ConnectionProfile{MigrationConfigured: true}, db: runtimeOpenTestDB(t), migrationDB: runtimeOpenTestDB(t), probeResults: []postgres.Capabilities{{}, {}}}
	deps := runtimeOpenTestDependencies(runtimeOpenDialectStub{name: "postgres"}, profile)
	if _, err := openContextWithDependencies(t.Context(), cfg, deps); err == nil {
		t.Fatal("missing migration was accepted")
	}
}

func TestOpenContextKeyRingAndPostgresSuccess(t *testing.T) {
	cfg := config.Config{DatabaseMigrationMode: "verify", IntegrationActiveKeyID: "active", IntegrationDecryptOnlyKeys: map[string]string{"old": "material"}}
	profile := &runtimePostgresProfileStub{db: runtimeOpenTestDB(t), probeResults: []postgres.Capabilities{{Database: "runtime"}}}
	deps := runtimeOpenTestDependencies(runtimeOpenDialectStub{name: "postgres"}, profile)
	deps.keyRing = func(secrets.Key, ...secrets.Key) (secrets.KeyProvider, error) { return nil, errDatabaseSQL }
	if _, err := openContextWithDependencies(t.Context(), cfg, deps); !errors.Is(err, errDatabaseSQL) {
		t.Fatalf("key ring error=%v", err)
	}

	profile = &runtimePostgresProfileStub{db: runtimeOpenTestDB(t), probeResults: []postgres.Capabilities{{Database: "runtime"}}}
	deps = runtimeOpenTestDependencies(runtimeOpenDialectStub{name: "postgres"}, profile)
	store, err := openContextWithDependencies(t.Context(), cfg, deps)
	if err != nil {
		t.Fatal(err)
	}
	if store.postgresProfile == nil || store.secretKeyProvider == nil || store.migrationCompatible != true {
		t.Fatalf("store=%#v", store)
	}
}

func TestDefaultRuntimeOpenDependenciesAndProfileAdapter(t *testing.T) {
	dependencies := defaultRuntimeOpenDependencies()
	if _, err := dependencies.dialect("sqlite"); err != nil {
		t.Fatal(err)
	}
	if _, err := dependencies.postgresProfile(config.Config{}); err == nil {
		t.Fatal("invalid PostgreSQL config accepted")
	}
	profile, err := dependencies.postgresProfile(config.Config{
		DatabaseDriver: "postgres", DatabaseDSN: "postgres://user:password@localhost/runtime?sslmode=disable", DatabaseMigrationMode: "verify",
	})
	if err != nil || profile.Profile() == nil {
		t.Fatalf("profile=%#v err=%v", profile, err)
	}
	db, err := profile.Open(telemetry.NewSQLMetrics())
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	if _, err := profile.OpenMigration(telemetry.NewSQLMetrics()); err == nil {
		t.Fatal("missing migration DSN accepted")
	}
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := profile.ProbeWithBackoff(cancelled, runtimeOpenTestDB(t)); err == nil {
		t.Fatal("cancelled probe succeeded")
	}
	if err := profile.ValidateRuntimeCapabilities(postgres.Capabilities{}, postgres.Capabilities{}); err == nil {
		t.Fatal("empty capabilities accepted")
	}
	observed, err := dependencies.observedSQL("sqlite", ":memory:", "runtime", telemetry.NewSQLMetrics())
	if err != nil {
		t.Fatal(err)
	}
	_ = observed.Close()
	if _, err := dependencies.keyRing(secrets.Key{ID: "active", Material: make([]byte, 32)}); err != nil {
		t.Fatal(err)
	}
}
