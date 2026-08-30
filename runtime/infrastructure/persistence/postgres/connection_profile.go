package postgres

import (
	"crypto/x509"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/telemetry"
	ormdialect "github.com/domainry/domainry-orm/dialect"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
)

const (
	BackendPostgres = "postgres"

	ConnectionModeDirect            = "direct"
	ConnectionModeSessionPooler     = "session_pooler"
	ConnectionModeTransactionPooler = "transaction_pooler"
)

// ConnectionProfile owns generic PostgreSQL connection behavior. Hosting
// providers do not get backend-specific branches in Runtime.
type ConnectionProfile struct {
	Backend             string
	Mode                string
	Schema              string
	MaxOpenConns        int
	MaxIdleConns        int
	ServerMaxConns      int
	ReservedConns       int
	RuntimeReplicaCount int
	ConnMaxLifetime     time.Duration
	ConnMaxIdleTime     time.Duration
	ConnectTimeout      time.Duration
	StatementTimeout    time.Duration
	LockTimeout         time.Duration
	TLS                 bool
	TLSVerified         bool
	PreparedStatements  bool
	MigrationConfigured bool
	MigrationMode       string

	connConfig      *pgx.ConnConfig
	migrationConfig *pgx.ConnConfig
}

// NewConnectionProfile parses and validates config without opening a network
// connection. It intentionally never returns the DSN or database password.
func NewConnectionProfile(cfg config.Config) (ConnectionProfile, error) {
	migrationMode := cfg.EffectiveDatabaseMigrationMode()
	if migrationMode != "apply" && migrationMode != "verify" {
		return ConnectionProfile{}, fmt.Errorf("DATABASE_MIGRATION_MODE must be apply or verify")
	}
	mode := strings.ToLower(strings.TrimSpace(cfg.DatabaseConnectionMode))
	if mode == "" {
		mode = ConnectionModeDirect
	}
	if mode != ConnectionModeDirect && mode != ConnectionModeSessionPooler && mode != ConnectionModeTransactionPooler {
		return ConnectionProfile{}, fmt.Errorf("DATABASE_CONNECTION_MODE must be direct, session_pooler, or transaction_pooler")
	}

	dsn := strings.TrimSpace(cfg.DatabaseDSN)
	if dsn == "" {
		return ConnectionProfile{}, fmt.Errorf("DATABASE_DSN is required for PostgreSQL")
	}
	connConfig, err := pgx.ParseConfig(dsn)
	if err != nil {
		return ConnectionProfile{}, fmt.Errorf("invalid DATABASE_DSN: malformed PostgreSQL connection string")
	}
	migrationConfig, err := parseMigrationConfig(cfg, connConfig)
	if err != nil {
		return ConnectionProfile{}, err
	}

	schema := strings.TrimSpace(cfg.DatabaseSchema)
	if schema == "" {
		schema = "public"
	}
	if !ormdialect.ValidIdentifier(schema) {
		return ConnectionProfile{}, fmt.Errorf("DATABASE_SCHEMA must be a safe SQL identifier")
	}
	if cfg.DatabaseConnectTimeout > 0 {
		connConfig.ConnectTimeout = cfg.DatabaseConnectTimeout
	}
	preparedStatements := mode != ConnectionModeTransactionPooler
	if !preparedStatements {
		connConfig.StatementCacheCapacity = 0
		connConfig.DescriptionCacheCapacity = 0
		connConfig.DefaultQueryExecMode = pgx.QueryExecModeExec
	} else {
		connConfig.DefaultQueryExecMode = pgx.QueryExecModeCacheStatement
	}
	if cfg.DatabaseSSLRootCert != "" {
		if err := configureRootCertificate(connConfig, cfg.DatabaseSSLRootCert); err != nil {
			return ConnectionProfile{}, err
		}
	}
	tlsEnabled := connConfig.TLSConfig != nil
	tlsVerified := postgresTLSVerificationEnabled(connConfig)
	if cfg.IsProduction() {
		if !tlsEnabled {
			return ConnectionProfile{}, fmt.Errorf("PostgreSQL TLS must be enabled in production")
		}
		if !tlsVerified {
			return ConnectionProfile{}, fmt.Errorf("PostgreSQL certificate verification must be enabled in production")
		}
	}

	maxOpen := cfg.DatabaseMaxOpenConns
	if maxOpen < 0 {
		return ConnectionProfile{}, fmt.Errorf("DATABASE_MAX_OPEN_CONNS must not be negative")
	}
	if maxOpen == 0 {
		maxOpen = 10
	}
	maxIdle := cfg.DatabaseMaxIdleConns
	if maxIdle < 0 {
		return ConnectionProfile{}, fmt.Errorf("DATABASE_MAX_IDLE_CONNS must not be negative")
	}
	if maxIdle > maxOpen {
		return ConnectionProfile{}, fmt.Errorf("DATABASE_MAX_IDLE_CONNS must not exceed DATABASE_MAX_OPEN_CONNS")
	}
	replicaCount := cfg.RuntimeReplicaCount
	if replicaCount <= 0 {
		replicaCount = 1
	}
	if cfg.DatabaseMaxConnections > 0 {
		available := cfg.DatabaseMaxConnections - cfg.DatabaseReservedConnections
		if available <= 0 || maxOpen*replicaCount > available {
			return ConnectionProfile{}, fmt.Errorf("Runtime database pool budget exceeds DATABASE_MAX_CONNECTIONS after reserved connections")
		}
	}

	return ConnectionProfile{
		Backend:             BackendPostgres,
		Mode:                mode,
		Schema:              schema,
		MaxOpenConns:        maxOpen,
		MaxIdleConns:        maxIdle,
		ServerMaxConns:      cfg.DatabaseMaxConnections,
		ReservedConns:       cfg.DatabaseReservedConnections,
		RuntimeReplicaCount: replicaCount,
		ConnMaxLifetime:     cfg.DatabaseConnMaxLifetime,
		ConnMaxIdleTime:     cfg.DatabaseConnMaxIdleTime,
		ConnectTimeout:      connConfig.ConnectTimeout,
		StatementTimeout:    cfg.DatabaseStatementTimeout,
		LockTimeout:         cfg.DatabaseLockTimeout,
		TLS:                 tlsEnabled,
		TLSVerified:         tlsVerified,
		PreparedStatements:  preparedStatements,
		MigrationConfigured: migrationConfig != nil,
		MigrationMode:       migrationMode,
		connConfig:          connConfig,
		migrationConfig:     migrationConfig,
	}, nil
}

// Open uses pgx's native config path so connection-mode behavior does not
// depend on driver defaults hidden inside sql.Open.
func (p ConnectionProfile) Open(metrics ...*telemetry.SQLMetrics) (*sql.DB, error) {
	if p.connConfig == nil {
		return nil, fmt.Errorf("PostgreSQL connection profile is not initialized")
	}
	var observer *telemetry.SQLMetrics
	if len(metrics) > 0 {
		observer = metrics[0]
	}
	db := sql.OpenDB(telemetry.WrapSQLConnector(stdlib.GetConnector(*p.connConfig.Copy()), "runtime", observer, nil))
	db.SetMaxOpenConns(p.MaxOpenConns)
	db.SetMaxIdleConns(p.MaxIdleConns)
	db.SetConnMaxLifetime(p.ConnMaxLifetime)
	db.SetConnMaxIdleTime(p.ConnMaxIdleTime)
	return db, nil
}

// OpenMigration returns the separately authenticated management connection.
// The migration pool is intentionally single-connection so release jobs cannot
// multiply advisory-lock and DDL pressure.
func (p ConnectionProfile) OpenMigration(metrics ...*telemetry.SQLMetrics) (*sql.DB, error) {
	if p.migrationConfig == nil {
		return nil, fmt.Errorf("DATABASE_MIGRATION_DSN is not configured")
	}
	var observer *telemetry.SQLMetrics
	if len(metrics) > 0 {
		observer = metrics[0]
	}
	db := sql.OpenDB(telemetry.WrapSQLConnector(stdlib.GetConnector(*p.migrationConfig.Copy()), "migration", observer))
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(p.ConnMaxLifetime)
	db.SetConnMaxIdleTime(p.ConnMaxIdleTime)
	return db, nil
}

func configureRootCertificate(connConfig *pgx.ConnConfig, path string) error {
	if connConfig.TLSConfig == nil {
		return fmt.Errorf("DATABASE_SSL_ROOT_CERT requires TLS to be enabled in DATABASE_DSN")
	}
	pem, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read DATABASE_SSL_ROOT_CERT: %w", err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pem) {
		return fmt.Errorf("DATABASE_SSL_ROOT_CERT contains no valid PEM certificates")
	}
	connConfig.TLSConfig.RootCAs = pool
	return nil
}

func postgresTLSVerificationEnabled(connConfig *pgx.ConnConfig) bool {
	if connConfig == nil || connConfig.TLSConfig == nil {
		return false
	}
	return !connConfig.TLSConfig.InsecureSkipVerify || connConfig.TLSConfig.VerifyPeerCertificate != nil
}

func parseMigrationConfig(cfg config.Config, queryConfig *pgx.ConnConfig) (*pgx.ConnConfig, error) {
	dsn := strings.TrimSpace(cfg.DatabaseMigrationDSN)
	if dsn == "" {
		return nil, nil
	}
	parsed, err := pgx.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("invalid DATABASE_MIGRATION_DSN: malformed PostgreSQL connection string")
	}
	if queryConfig != nil && parsed.Database != queryConfig.Database {
		return nil, fmt.Errorf("DATABASE_MIGRATION_DSN must target the same database as DATABASE_DSN")
	}
	if cfg.DatabaseConnectTimeout > 0 {
		parsed.ConnectTimeout = cfg.DatabaseConnectTimeout
	}
	parsed.DefaultQueryExecMode = pgx.QueryExecModeCacheStatement
	if cfg.DatabaseSSLRootCert != "" {
		if err := configureRootCertificate(parsed, cfg.DatabaseSSLRootCert); err != nil {
			return nil, err
		}
	}
	if cfg.IsProduction() {
		if parsed.TLSConfig == nil {
			return nil, fmt.Errorf("PostgreSQL TLS must be enabled in production")
		}
		if !postgresTLSVerificationEnabled(parsed) {
			return nil, fmt.Errorf("PostgreSQL certificate verification must be enabled in production")
		}
	}
	return parsed, nil
}

// PortString is exposed only for deterministic tests and diagnostics; it does
// not expose credentials or the full connection string.
func (p ConnectionProfile) PortString() string {
	if p.connConfig == nil {
		return ""
	}
	return strconv.Itoa(int(p.connConfig.Port))
}
