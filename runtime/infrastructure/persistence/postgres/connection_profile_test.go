package postgres

import (
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestPostgresProfileUsesGenericDefaults(t *testing.T) {
	profile, err := NewConnectionProfile(config.Config{
		DatabaseDSN:          "postgres://runtime:secret@localhost:5432/runtime?sslmode=disable",
		DatabaseMaxOpenConns: 12,
		DatabaseMaxIdleConns: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	if profile.Backend != BackendPostgres || profile.Mode != ConnectionModeDirect || profile.Schema != "public" {
		t.Fatalf("unexpected generic PostgreSQL profile: %+v", profile)
	}
	if !profile.PreparedStatements || profile.MaxOpenConns != 12 || profile.MaxIdleConns != 4 {
		t.Fatalf("unexpected connection behavior: %+v", profile)
	}
}

func TestPostgresTransactionPoolerModeIsProviderNeutral(t *testing.T) {
	profile, err := NewConnectionProfile(config.Config{
		DatabaseDSN:            "postgres://runtime:secret@pool.example.test:9999/runtime?sslmode=require",
		DatabaseConnectionMode: ConnectionModeTransactionPooler,
	})
	if err != nil {
		t.Fatal(err)
	}
	if profile.PreparedStatements {
		t.Fatal("transaction pooler mode must not retain prepared statements")
	}
	if got := profile.PortString(); got != "9999" {
		t.Fatalf("provider-specific port validation leaked into generic profile: %s", got)
	}
}

func TestPostgresProfileDoesNotInterpretProviderCredentialsOrRoles(t *testing.T) {
	for _, dsn := range []string{
		"postgres://postgres:secret@localhost:5432/runtime?sslmode=disable",
		"postgres://runtime:sb_secret_opaque@localhost:5432/runtime?sslmode=disable",
	} {
		if _, err := NewConnectionProfile(config.Config{DatabaseDSN: dsn}); err != nil {
			t.Fatalf("generic PostgreSQL rejected provider-owned credential semantics: %v", err)
		}
	}
}

func TestPostgresProfileValidatesGenericConnectionSettings(t *testing.T) {
	tests := []struct {
		name string
		cfg  config.Config
		want string
	}{
		{name: "missing dsn", cfg: config.Config{}, want: "DATABASE_DSN is required"},
		{name: "unsafe schema", cfg: config.Config{DatabaseDSN: "postgres://u:p@localhost/db", DatabaseSchema: "bad-name"}, want: "safe SQL identifier"},
		{name: "idle exceeds open", cfg: config.Config{DatabaseDSN: "postgres://u:p@localhost/db", DatabaseMaxOpenConns: 2, DatabaseMaxIdleConns: 3}, want: "must not exceed"},
		{name: "unknown mode", cfg: config.Config{DatabaseDSN: "postgres://u:p@localhost/db", DatabaseConnectionMode: "vendor_pool"}, want: "must be direct"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewConnectionProfile(tt.cfg)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want %q", err, tt.want)
			}
		})
	}
}

func TestPostgresPoolBudgetIsGeneric(t *testing.T) {
	_, err := NewConnectionProfile(config.Config{
		DatabaseDSN:                 "postgres://u:p@localhost/db",
		DatabaseMaxOpenConns:        20,
		DatabaseMaxConnections:      50,
		DatabaseReservedConnections: 15,
		RuntimeReplicaCount:         2,
		DatabaseConnectionMode:      ConnectionModeDirect,
		DatabaseConnMaxLifetime:     time.Minute,
	})
	if err == nil || !strings.Contains(err.Error(), "pool budget") {
		t.Fatalf("expected generic pool budget failure, got %v", err)
	}
}

func TestPostgresCapabilitiesDoNotRequireProviderRoleLayout(t *testing.T) {
	profile := ConnectionProfile{Backend: BackendPostgres}
	query := Capabilities{Database: "runtime", User: "postgres", SchemaExists: true, SchemaUsage: true, SchemaCreate: true}
	if err := profile.ValidateRuntimeCapabilities(query, Capabilities{}); err != nil {
		t.Fatalf("generic PostgreSQL must not impose provider role layout: %v", err)
	}
}
