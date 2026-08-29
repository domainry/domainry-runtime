package postgres

import (
	"context"
	"database/sql"
	"time"

	ormpostgres "github.com/domainry/domainry-orm/postgres"
)

type CapabilityError = ormpostgres.CapabilityError
type Capabilities = ormpostgres.Capabilities

const (
	FailureDNS                = ormpostgres.FailureDNS
	FailureNetworkIPv4        = ormpostgres.FailureNetworkIPv4
	FailureNetworkIPv6        = ormpostgres.FailureNetworkIPv6
	FailureTLS                = ormpostgres.FailureTLS
	FailureAuthentication     = ormpostgres.FailureAuthentication
	FailurePoolExhausted      = ormpostgres.FailurePoolExhausted
	FailureServerUnavailable  = ormpostgres.FailureServerUnavailable
	FailureSchemaIncompatible = ormpostgres.FailureSchemaIncompatible
	FailureUnknown            = ormpostgres.FailureUnknown
)

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
	return SafeStatus{Backend: p.Backend, Mode: p.Mode, Schema: p.Schema, MaxOpenConns: p.MaxOpenConns, MaxIdleConns: p.MaxIdleConns, ServerMaxConns: p.ServerMaxConns, ReservedConns: p.ReservedConns, RuntimeReplicaCount: p.RuntimeReplicaCount, TLS: p.TLS, TLSVerified: p.TLSVerified, PreparedStatements: p.PreparedStatements, MigrationConfigured: p.MigrationConfigured, MigrationMode: p.MigrationMode, RLSEnabled: p.RLSEnabled}
}

func (p ConnectionProfile) Probe(ctx context.Context, db *sql.DB) (Capabilities, error) {
	return ormpostgres.Probe(ctx, db, p.Schema)
}

func (p ConnectionProfile) ProbeWithBackoff(ctx context.Context, db *sql.DB) (Capabilities, error) {
	return ormpostgres.ProbeWithBackoff(ctx, db, p.Schema)
}

func (p ConnectionProfile) InitialPoolRetryBackoff() time.Duration { return 100 * time.Millisecond }

func probeWithPoolBackoff(ctx context.Context, attempts int, initial time.Duration, probe func(context.Context) (Capabilities, error), wait func(context.Context, time.Duration) error) (Capabilities, error) {
	return ormpostgres.RetryPoolProbe(ctx, attempts, initial, probe, wait)
}

func waitForPoolRetry(ctx context.Context, delay time.Duration) error {
	return ormpostgres.WaitForPoolRetry(ctx, delay)
}

func (p ConnectionProfile) ValidateRuntimeCapabilities(query, migrator Capabilities) error {
	return ormpostgres.ValidateCapabilities(p.TLS, p.MigrationConfigured, query, migrator)
}

var ClassifyConnectionFailure = ormpostgres.ClassifyConnectionFailure
