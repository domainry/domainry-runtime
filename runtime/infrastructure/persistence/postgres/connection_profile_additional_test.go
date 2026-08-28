package postgres

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql/driver"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-runtime/runtime/platform/requestcontext"
	"github.com/domainry/domainry-runtime/runtime/platform/telemetry"
	"github.com/jackc/pgx/v5"
)

func TestPostgresProfileValidationMatrix(t *testing.T) {
	base := config.Config{DatabaseDSN: "postgres://user:secret@localhost:5432/runtime?sslmode=disable"}
	tests := []struct {
		name string
		edit func(*config.Config)
		want string
	}{
		{name: "migration mode", edit: func(cfg *config.Config) { cfg.DatabaseMigrationMode = "unsafe" }, want: "MIGRATION_MODE"},
		{name: "malformed dsn", edit: func(cfg *config.Config) { cfg.DatabaseDSN = "://" }, want: "malformed"},
		{name: "negative open", edit: func(cfg *config.Config) { cfg.DatabaseMaxOpenConns = -1 }, want: "MAX_OPEN"},
		{name: "negative idle", edit: func(cfg *config.Config) { cfg.DatabaseMaxIdleConns = -1 }, want: "MAX_IDLE"},
		{name: "reserved all capacity", edit: func(cfg *config.Config) { cfg.DatabaseMaxConnections, cfg.DatabaseReservedConnections = 10, 10 }, want: "pool budget"},
		{name: "production tls missing", edit: func(cfg *config.Config) { cfg.Environment = "production" }, want: "TLS must be enabled"},
		{name: "production tls unverified", edit: func(cfg *config.Config) {
			cfg.Environment = "production"
			cfg.DatabaseDSN = "postgres://user:secret@localhost/runtime?sslmode=require"
		}, want: "verification"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cfg := base
			test.edit(&cfg)
			if _, err := NewConnectionProfile(cfg); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v want=%q", err, test.want)
			}
		})
	}

	profile, err := NewConnectionProfile(config.Config{DatabaseDSN: "postgres://user:secret@localhost/runtime?sslmode=verify-full", DatabaseConnectTimeout: 3 * time.Second, DatabaseMaxConnections: 50, DatabaseReservedConnections: 5, DatabaseMaxOpenConns: 10, RuntimeReplicaCount: 2, Environment: "production", DatabaseRLSEnabled: true})
	if err != nil || profile.ConnectTimeout != 3*time.Second || profile.MaxOpenConns != 10 || profile.RuntimeReplicaCount != 2 || !profile.TLSVerified || !profile.RLSEnabled {
		t.Fatalf("profile=%#v err=%v", profile, err)
	}
	defaults, err := NewConnectionProfile(base)
	if err != nil || defaults.MaxOpenConns != 10 || defaults.RuntimeReplicaCount != 1 {
		t.Fatalf("defaults=%#v err=%v", defaults, err)
	}
	sessionPooler := base
	sessionPooler.DatabaseConnectionMode = ConnectionModeSessionPooler
	if profile, err := NewConnectionProfile(sessionPooler); err != nil || profile.Mode != ConnectionModeSessionPooler {
		t.Fatalf("session pooler profile=%#v err=%v", profile, err)
	}
}

func TestPostgresMigrationProfileValidationAndOpen(t *testing.T) {
	base := config.Config{DatabaseDSN: "postgres://query:secret@localhost/runtime?sslmode=disable"}
	for _, test := range []struct {
		name string
		dsn  string
		want string
	}{
		{name: "malformed", dsn: "://", want: "malformed"},
		{name: "database mismatch", dsn: "postgres://migrator:secret@localhost/other?sslmode=disable", want: "same database"},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := base
			cfg.DatabaseMigrationDSN = test.dsn
			if _, err := NewConnectionProfile(cfg); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v want=%q", err, test.want)
			}
		})
	}

	production := config.Config{Environment: "production", DatabaseDSN: "postgres://query:secret@localhost/runtime?sslmode=verify-full", DatabaseMigrationDSN: "postgres://migrator:secret@localhost/runtime?sslmode=disable"}
	if _, err := NewConnectionProfile(production); err == nil || !strings.Contains(err.Error(), "TLS must be enabled") {
		t.Fatalf("migration tls error=%v", err)
	}
	production.DatabaseMigrationDSN = "postgres://migrator:secret@localhost/runtime?sslmode=require"
	if _, err := NewConnectionProfile(production); err == nil || !strings.Contains(err.Error(), "verification") {
		t.Fatalf("migration verification error=%v", err)
	}

	cfg := base
	cfg.DatabaseMigrationDSN = "postgres://migrator:secret@localhost/runtime?sslmode=disable"
	cfg.DatabaseConnectTimeout = 2 * time.Second
	profile, err := NewConnectionProfile(cfg)
	if err != nil || !profile.MigrationConfigured || profile.migrationConfig.ConnectTimeout != 2*time.Second || profile.migrationConfig.DefaultQueryExecMode != pgx.QueryExecModeCacheStatement {
		t.Fatalf("profile=%#v err=%v", profile, err)
	}
	if _, err := (ConnectionProfile{}).OpenMigration(); err == nil {
		t.Fatal("expected missing migration DSN error")
	}
	db, err := profile.OpenMigration(telemetry.NewSQLMetrics())
	if err != nil || db.Stats().MaxOpenConnections != 1 {
		t.Fatalf("migration db stats=%#v err=%v", db.Stats(), err)
	}
	_ = db.Close()
	db, err = profile.OpenMigration()
	if err != nil || db.Stats().MaxOpenConnections != 1 {
		t.Fatalf("migration db without metrics stats=%#v err=%v", db.Stats(), err)
	}
	_ = db.Close()
	parsed, err := parseMigrationConfig(config.Config{DatabaseMigrationDSN: "postgres://migrator:secret@localhost/runtime?sslmode=disable"}, nil)
	if err != nil || parsed == nil || parsed.Database != "runtime" {
		t.Fatalf("migration config without query=%#v err=%v", parsed, err)
	}
	parsed, err = parseMigrationConfig(config.Config{Environment: "production", DatabaseMigrationDSN: "postgres://migrator:secret@localhost/runtime?sslmode=verify-full"}, nil)
	if err != nil || parsed == nil || !postgresTLSVerificationEnabled(parsed) {
		t.Fatalf("verified production migration config=%#v err=%v", parsed, err)
	}
}

func TestPostgresProfileOpenAndWorkspaceInitializer(t *testing.T) {
	if _, err := (ConnectionProfile{}).Open(); err == nil {
		t.Fatal("expected uninitialized profile error")
	}
	profile, err := NewConnectionProfile(config.Config{DatabaseDSN: "postgres://user:secret@localhost/runtime?sslmode=disable", DatabaseMaxOpenConns: 7, DatabaseMaxIdleConns: 3, DatabaseConnMaxLifetime: time.Minute, DatabaseConnMaxIdleTime: 30 * time.Second, DatabaseRLSEnabled: true})
	if err != nil {
		t.Fatal(err)
	}
	db, err := profile.Open(telemetry.NewSQLMetrics())
	if err != nil || db.Stats().MaxOpenConnections != 7 {
		t.Fatalf("db stats=%#v err=%v", db.Stats(), err)
	}
	_ = db.Close()
	db, err = profile.Open()
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	profile.RLSEnabled = false
	db, err = profile.Open()
	if err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	execer := &profileExecer{}
	if err := postgresWorkspaceTransactionInitializer(t.Context(), execer); err != nil || execer.calls != 0 {
		t.Fatalf("empty workspace calls=%d err=%v", execer.calls, err)
	}
	ctx := requestcontext.WithActorID(requestcontext.WithWorkspaceID(t.Context(), "workspace-1"), "actor-1")
	if err := postgresWorkspaceTransactionInitializer(ctx, execer); err != nil || execer.calls != 1 || len(execer.args) != 2 || execer.args[0].Value != "workspace-1" || execer.args[1].Value != "actor-1" {
		t.Fatalf("calls=%d args=%#v err=%v", execer.calls, execer.args, err)
	}
	execer.err = errors.New("connection lost")
	if err := postgresWorkspaceTransactionInitializer(ctx, execer); err == nil || !strings.Contains(err.Error(), "workspace RLS context") {
		t.Fatalf("initializer error=%v", err)
	}
}

func TestPostgresRootCertificateAndTLSHelpers(t *testing.T) {
	plain, err := pgx.ParseConfig("postgres://user:secret@localhost/runtime?sslmode=disable")
	if err != nil {
		t.Fatal(err)
	}
	if postgresTLSVerificationEnabled(nil) || postgresTLSVerificationEnabled(plain) {
		t.Fatal("plain profile must not report TLS verification")
	}
	if err := configureRootCertificate(plain, "missing"); err == nil || !strings.Contains(err.Error(), "requires TLS") {
		t.Fatalf("plain root cert error=%v", err)
	}
	if _, err := NewConnectionProfile(config.Config{DatabaseDSN: "postgres://user:secret@localhost/runtime?sslmode=disable", DatabaseSSLRootCert: "missing"}); err == nil || !strings.Contains(err.Error(), "requires TLS") {
		t.Fatalf("query root cert propagation=%v", err)
	}
	tlsConfig, err := pgx.ParseConfig("postgres://user:secret@localhost/runtime?sslmode=verify-full")
	if err != nil {
		t.Fatal(err)
	}
	if !postgresTLSVerificationEnabled(tlsConfig) {
		t.Fatal("verify-full must verify certificates")
	}
	if err := configureRootCertificate(tlsConfig, filepath.Join(t.TempDir(), "missing.pem")); err == nil || !strings.Contains(err.Error(), "read DATABASE_SSL_ROOT_CERT") {
		t.Fatalf("missing cert error=%v", err)
	}
	invalidPath := filepath.Join(t.TempDir(), "invalid.pem")
	if err := os.WriteFile(invalidPath, []byte("not pem"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := configureRootCertificate(tlsConfig, invalidPath); err == nil || !strings.Contains(err.Error(), "no valid PEM") {
		t.Fatalf("invalid cert error=%v", err)
	}
	validPath := createPostgresTestCertificate(t)
	if err := configureRootCertificate(tlsConfig, validPath); err != nil || tlsConfig.TLSConfig.RootCAs == nil {
		t.Fatalf("valid cert error=%v", err)
	}
	cfg := config.Config{DatabaseDSN: "postgres://user:secret@localhost/runtime?sslmode=verify-full", DatabaseMigrationDSN: "postgres://migrator:secret@localhost/runtime?sslmode=verify-full", DatabaseSSLRootCert: validPath}
	if _, err := NewConnectionProfile(cfg); err != nil {
		t.Fatalf("profile with root certificate: %v", err)
	}
	cfg.DatabaseMigrationDSN = "postgres://migrator:secret@localhost/runtime?sslmode=disable"
	if _, err := NewConnectionProfile(cfg); err == nil || !strings.Contains(err.Error(), "requires TLS") {
		t.Fatalf("migration root cert propagation=%v", err)
	}
	if got := (ConnectionProfile{}).PortString(); got != "" {
		t.Fatalf("empty port=%q", got)
	}
}

func createPostgresTestCertificate(t *testing.T) string {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 1024)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "runtime-test"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	raw, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "root.pem")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: raw}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

type profileExecer struct {
	calls int
	args  []driver.NamedValue
	err   error
}

func (e *profileExecer) ExecContext(_ context.Context, _ string, args []driver.NamedValue) (driver.Result, error) {
	e.calls++
	e.args = append([]driver.NamedValue(nil), args...)
	return driver.RowsAffected(1), e.err
}
