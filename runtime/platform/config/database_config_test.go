package config

import (
	"testing"
	"time"
)

func TestFromEnvLoadsTypedDatabaseConnectionConfig(t *testing.T) {
	t.Setenv("DATABASE_DSN", "postgres://runtime:secret@localhost:5432/runtime")
	t.Setenv("DATABASE_MIGRATION_DSN", "postgres://migrator:secret@localhost:5432/runtime")
	t.Setenv("DATABASE_MIGRATION_MODE", "apply")
	t.Setenv("DATABASE_CONNECTION_MODE", "session_pooler")
	t.Setenv("DATABASE_SCHEMA", "tenant_runtime")
	t.Setenv("DATABASE_MAX_OPEN_CONNS", "17")
	t.Setenv("DATABASE_MAX_IDLE_CONNS", "6")
	t.Setenv("DATABASE_MAX_CONNECTIONS", "120")
	t.Setenv("DATABASE_RESERVED_CONNECTIONS", "30")
	t.Setenv("RUNTIME_REPLICA_COUNT", "4")
	t.Setenv("DATABASE_CONN_MAX_LIFETIME", "45m")
	t.Setenv("DATABASE_CONN_MAX_IDLE_TIME", "7m")
	t.Setenv("DATABASE_CONNECT_TIMEOUT", "11s")
	t.Setenv("DATABASE_STATEMENT_TIMEOUT", "23s")
	t.Setenv("DATABASE_LOCK_TIMEOUT", "4s")
	t.Setenv("DATABASE_SSL_ROOT_CERT", "/run/secrets/postgres-ca.pem")
	t.Setenv("MIGRATION_BACKUP_EVIDENCE_PATH", "/run/evidence/postgres-backup.json")

	cfg := FromEnv()
	if cfg.DatabaseConnectionMode != "session_pooler" || cfg.DatabaseSchema != "tenant_runtime" {
		t.Fatalf("typed database identity config not loaded: %+v", cfg)
	}
	if cfg.DatabaseMaxOpenConns != 17 || cfg.DatabaseMaxIdleConns != 6 {
		t.Fatalf("pool config not loaded: open=%d idle=%d", cfg.DatabaseMaxOpenConns, cfg.DatabaseMaxIdleConns)
	}
	if cfg.DatabaseMaxConnections != 120 || cfg.DatabaseReservedConnections != 30 || cfg.RuntimeReplicaCount != 4 {
		t.Fatalf("connection budget config not loaded: %+v", cfg)
	}
	if cfg.DatabaseConnMaxLifetime != 45*time.Minute || cfg.DatabaseConnMaxIdleTime != 7*time.Minute || cfg.DatabaseConnectTimeout != 11*time.Second {
		t.Fatalf("connection durations not loaded: %+v", cfg)
	}
	if cfg.DatabaseStatementTimeout != 23*time.Second || cfg.DatabaseLockTimeout != 4*time.Second {
		t.Fatalf("query timeouts not loaded: %+v", cfg)
	}
	if cfg.DatabaseSSLRootCert != "/run/secrets/postgres-ca.pem" || cfg.DatabaseMigrationDSN == "" || cfg.DatabaseMigrationMode != "apply" {
		t.Fatalf("TLS or migration connection config not loaded")
	}
	if cfg.MigrationBackupEvidencePath != "/run/evidence/postgres-backup.json" {
		t.Fatal("MIGRATION_BACKUP_EVIDENCE_PATH was not loaded")
	}
}

func TestDatabaseMigrationModeDefaultsToVerifyInProduction(t *testing.T) {
	t.Setenv("APP_ENV", "production")
	t.Setenv("DATABASE_MIGRATION_MODE", "")
	if got := FromEnv().DatabaseMigrationMode; got != "verify" {
		t.Fatalf("production migration mode = %q, want verify", got)
	}
	if got := (Config{Environment: "development"}).EffectiveDatabaseMigrationMode(); got != "apply" {
		t.Fatalf("development migration mode = %q, want apply", got)
	}
}

func TestDefinitionUpgradeModeFollowsMigrationModeAndProduction(t *testing.T) {
	t.Setenv("APP_ENV", "development")
	t.Setenv("DATABASE_MIGRATION_MODE", "")
	t.Setenv("DEFINITION_UPGRADE_MODE", "")
	if got := FromEnv().DefinitionUpgradeMode; got != "apply" {
		t.Fatalf("development definition upgrade mode = %q, want apply", got)
	}
	t.Setenv("DATABASE_MIGRATION_MODE", "verify")
	if got := FromEnv().DefinitionUpgradeMode; got != "verify" {
		t.Fatalf("verify migration mode definition upgrade mode = %q, want verify", got)
	}
	t.Setenv("DEFINITION_UPGRADE_MODE", "Plan")
	if got := FromEnv().DefinitionUpgradeMode; got != "plan" {
		t.Fatalf("explicit definition upgrade mode = %q, want plan", got)
	}
	t.Setenv("APP_ENV", "production")
	t.Setenv("DATABASE_MIGRATION_MODE", "")
	t.Setenv("DEFINITION_UPGRADE_MODE", "")
	if got := FromEnv().DefinitionUpgradeMode; got != "verify" {
		t.Fatalf("production definition upgrade mode = %q, want verify", got)
	}
	if got := (Config{Environment: "development"}).EffectiveDefinitionUpgradeMode(); got != "apply" {
		t.Fatalf("effective development mode = %q, want apply", got)
	}
	if got := (Config{Environment: "development", DatabaseMigrationMode: "verify"}).EffectiveDefinitionUpgradeMode(); got != "verify" {
		t.Fatalf("effective verify-migration mode = %q, want verify", got)
	}
	if got := (Config{Environment: "production"}).EffectiveDefinitionUpgradeMode(); got != "verify" {
		t.Fatalf("effective production mode = %q, want verify", got)
	}
	if got := (Config{Environment: "production", DefinitionUpgradeMode: " APPLY "}).EffectiveDefinitionUpgradeMode(); got != "apply" {
		t.Fatalf("effective explicit mode = %q, want apply", got)
	}
	found := false
	for _, definition := range Definitions() {
		if definition.Name == "DEFINITION_UPGRADE_MODE" {
			found = definition.Type == TypeString && !definition.Secret
		}
	}
	if !found {
		t.Fatal("DEFINITION_UPGRADE_MODE is not part of the configuration contract")
	}
}
