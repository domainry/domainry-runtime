package postgres

import (
	"context"
	"crypto/x509"
	"database/sql"
	"errors"
	"net"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

type CapabilityError struct {
	Kind string
}

func (e CapabilityError) Error() string {
	return "PostgreSQL capability check failed (" + e.Kind + ")"
}

const (
	FailureDNS                = "dns"
	FailureNetworkIPv4        = "network_ipv4"
	FailureNetworkIPv6        = "network_ipv6"
	FailureTLS                = "tls"
	FailureAuthentication     = "authentication"
	FailurePoolExhausted      = "pool_exhausted"
	FailureServerUnavailable  = "server_unavailable"
	FailureSchemaIncompatible = "schema_incompatible"
	FailureUnknown            = "unknown"
)

// SafeStatus is the only connection-profile shape intended for health/status
// output. It deliberately has no host, user, database, DSN, or credential.
type SafeStatus struct {
	Backend             string `json:"backend"`
	Mode                string `json:"mode"`
	Schema              string `json:"schema"`
	MaxOpenConns        int    `json:"max_open_conns"`
	MaxIdleConns        int    `json:"max_idle_conns"`
	ServerMaxConns      int    `json:"server_max_connections,omitempty"`
	ReservedConns       int    `json:"reserved_connections,omitempty"`
	RuntimeReplicaCount int    `json:"runtime_replica_count"`
	TLS                 bool   `json:"tls"`
	TLSVerified         bool   `json:"tls_verified"`
	PreparedStatements  bool   `json:"prepared_statements"`
	MigrationConfigured bool   `json:"migration_configured"`
	MigrationMode       string `json:"migration_mode"`
	RLSEnabled          bool   `json:"rls_enabled"`
}

func (p ConnectionProfile) SafeStatus() SafeStatus {
	return SafeStatus{
		Backend:             p.Backend,
		Mode:                p.Mode,
		Schema:              p.Schema,
		MaxOpenConns:        p.MaxOpenConns,
		MaxIdleConns:        p.MaxIdleConns,
		ServerMaxConns:      p.ServerMaxConns,
		ReservedConns:       p.ReservedConns,
		RuntimeReplicaCount: p.RuntimeReplicaCount,
		TLS:                 p.TLS,
		TLSVerified:         p.TLSVerified,
		PreparedStatements:  p.PreparedStatements,
		MigrationConfigured: p.MigrationConfigured,
		MigrationMode:       p.MigrationMode,
		RLSEnabled:          p.RLSEnabled,
	}
}

// Capabilities captures connection-scoped facts needed by readiness without
// exposing server identity or credentials in public status payloads.
type Capabilities struct {
	ServerVersion string
	Database      string
	User          string
	CurrentSchema string
	TLS           bool
	ReadOnly      bool
	InRecovery    bool
	SchemaExists  bool
	SchemaUsage   bool
	SchemaCreate  bool
	AdvisoryLocks bool
}

// Probe verifies the PostgreSQL features Runtime relies on. It does not create
// objects and is safe for both the query role and the migrator role.
func (p ConnectionProfile) Probe(ctx context.Context, db *sql.DB) (Capabilities, error) {
	if db == nil {
		return Capabilities{}, errors.New("PostgreSQL database is required")
	}
	const query = `SELECT
current_setting('server_version'),
current_database(),
current_user,
COALESCE(current_schema(), ''),
EXISTS (SELECT 1 FROM pg_stat_ssl WHERE pid = pg_backend_pid() AND ssl),
current_setting('default_transaction_read_only') = 'on',
pg_is_in_recovery(),
EXISTS (SELECT 1 FROM pg_namespace WHERE nspname = $1),
COALESCE(has_schema_privilege(current_user, (SELECT oid FROM pg_namespace WHERE nspname = $1), 'USAGE'), false),
COALESCE(has_schema_privilege(current_user, (SELECT oid FROM pg_namespace WHERE nspname = $1), 'CREATE'), false),
to_regprocedure('pg_try_advisory_xact_lock(bigint)') IS NOT NULL`
	var capability Capabilities
	err := db.QueryRowContext(ctx, query, p.Schema).Scan(
		&capability.ServerVersion,
		&capability.Database,
		&capability.User,
		&capability.CurrentSchema,
		&capability.TLS,
		&capability.ReadOnly,
		&capability.InRecovery,
		&capability.SchemaExists,
		&capability.SchemaUsage,
		&capability.SchemaCreate,
		&capability.AdvisoryLocks,
	)
	if err != nil {
		return Capabilities{}, err
	}
	return capability, nil
}

// ProbeWithBackoff absorbs short pool/max-client bursts without retrying
// authentication, TLS, schema, or network-profile failures.
func (p ConnectionProfile) ProbeWithBackoff(ctx context.Context, db *sql.DB) (Capabilities, error) {
	return probeWithPoolBackoff(ctx, 3, p.InitialPoolRetryBackoff(), func(ctx context.Context) (Capabilities, error) {
		return p.Probe(ctx, db)
	}, waitForPoolRetry)
}

func (p ConnectionProfile) InitialPoolRetryBackoff() time.Duration { return 100 * time.Millisecond }

func probeWithPoolBackoff(ctx context.Context, attempts int, initial time.Duration, probe func(context.Context) (Capabilities, error), wait func(context.Context, time.Duration) error) (Capabilities, error) {
	if attempts < 1 {
		attempts = 1
	}
	if initial <= 0 {
		initial = 100 * time.Millisecond
	}
	for attempt := 1; ; attempt++ {
		capability, err := probe(ctx)
		if err == nil {
			return capability, nil
		}
		if ClassifyConnectionFailure(err) != FailurePoolExhausted || attempt == attempts {
			return Capabilities{}, err
		}
		delay := initial << (attempt - 1)
		if delay > time.Second {
			delay = time.Second
		}
		if err := wait(ctx, delay); err != nil {
			return Capabilities{}, err
		}
	}
}

func waitForPoolRetry(ctx context.Context, delay time.Duration) error {
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// ValidateRuntimeCapabilities verifies only the PostgreSQL behavior Runtime
// actually needs. Hosting-provider roles and product features are outside this
// adapter's contract.
func (p ConnectionProfile) ValidateRuntimeCapabilities(query, migrator Capabilities) error {
	if query.Database == "" {
		return CapabilityError{Kind: FailureSchemaIncompatible}
	}
	if query.ReadOnly || query.InRecovery {
		return CapabilityError{Kind: "read_only"}
	}
	if p.TLS && !query.TLS {
		return CapabilityError{Kind: FailureTLS}
	}
	if p.MigrationConfigured {
		if migrator.Database == "" || query.Database != migrator.Database {
			return CapabilityError{Kind: FailureSchemaIncompatible}
		}
		if migrator.ReadOnly || migrator.InRecovery {
			return CapabilityError{Kind: "read_only"}
		}
		if p.TLS && !migrator.TLS {
			return CapabilityError{Kind: FailureTLS}
		}
	}
	return nil
}

func ClassifyConnectionFailure(err error) string {
	if err == nil {
		return ""
	}
	var dnsError *net.DNSError
	if errors.As(err, &dnsError) {
		return FailureDNS
	}
	var certificateError x509.UnknownAuthorityError
	if errors.As(err, &certificateError) {
		return FailureTLS
	}
	var networkError *net.OpError
	if errors.As(err, &networkError) {
		if address, ok := networkError.Addr.(*net.TCPAddr); ok && address.IP != nil {
			if address.IP.To4() == nil {
				return FailureNetworkIPv6
			}
			return FailureNetworkIPv4
		}
	}
	var postgresError *pgconn.PgError
	if errors.As(err, &postgresError) {
		switch postgresError.Code {
		case "28P01", "28000":
			return FailureAuthentication
		case "53300", "53400":
			return FailurePoolExhausted
		case "3D000", "3F000", "42P01":
			return FailureSchemaIncompatible
		case "57P01", "57P02", "57P03":
			return FailureServerUnavailable
		}
	}
	message := strings.ToLower(err.Error())
	switch {
	case strings.Contains(message, "certificate"), strings.Contains(message, "tls"), strings.Contains(message, "ssl"):
		return FailureTLS
	case strings.Contains(message, "network is unreachable"), strings.Contains(message, "no route to host"):
		return FailureNetworkIPv4
	case strings.Contains(message, "server is unavailable"), strings.Contains(message, "database is unavailable"):
		return FailureServerUnavailable
	case strings.Contains(message, "too many connections"), strings.Contains(message, "max client connections"):
		return FailurePoolExhausted
	default:
		return FailureUnknown
	}
}
