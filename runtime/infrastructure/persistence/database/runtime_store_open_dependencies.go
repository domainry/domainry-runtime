package database

import (
	"context"
	"database/sql"

	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/postgres"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-runtime/runtime/platform/secrets"
	"github.com/domainry/domainry-runtime/runtime/platform/telemetry"
)

type runtimePostgresProfile interface {
	Open(*telemetry.SQLMetrics) (*sql.DB, error)
	OpenMigration(*telemetry.SQLMetrics) (*sql.DB, error)
	ProbeWithBackoff(context.Context, *sql.DB) (postgres.Capabilities, error)
	ValidateRuntimeCapabilities(postgres.Capabilities, postgres.Capabilities) error
	Profile() *postgres.ConnectionProfile
}

type runtimePostgresProfileAdapter struct{ profile postgres.ConnectionProfile }

func (adapter runtimePostgresProfileAdapter) Open(metrics *telemetry.SQLMetrics) (*sql.DB, error) {
	return adapter.profile.Open(metrics)
}
func (adapter runtimePostgresProfileAdapter) OpenMigration(metrics *telemetry.SQLMetrics) (*sql.DB, error) {
	return adapter.profile.OpenMigration(metrics)
}
func (adapter runtimePostgresProfileAdapter) ProbeWithBackoff(ctx context.Context, db *sql.DB) (postgres.Capabilities, error) {
	return adapter.profile.ProbeWithBackoff(ctx, db)
}
func (adapter runtimePostgresProfileAdapter) ValidateRuntimeCapabilities(query, migrator postgres.Capabilities) error {
	return adapter.profile.ValidateRuntimeCapabilities(query, migrator)
}
func (adapter runtimePostgresProfileAdapter) Profile() *postgres.ConnectionProfile {
	profile := adapter.profile
	return &profile
}

type runtimeOpenDependencies struct {
	dialect         func(string) (dialect, error)
	postgresProfile func(config.Config) (runtimePostgresProfile, error)
	observedSQL     func(string, string, string, *telemetry.SQLMetrics) (*sql.DB, error)
	keyRing         func(secrets.Key, ...secrets.Key) (secrets.KeyProvider, error)
}

func defaultRuntimeOpenDependencies() runtimeOpenDependencies {
	return runtimeOpenDependencies{
		dialect: dialectFor,
		postgresProfile: func(cfg config.Config) (runtimePostgresProfile, error) {
			profile, err := postgres.NewConnectionProfile(cfg)
			if err != nil {
				return nil, err
			}
			return runtimePostgresProfileAdapter{profile: profile}, nil
		},
		observedSQL: telemetry.OpenObservedSQL,
		keyRing: func(active secrets.Key, decryptOnly ...secrets.Key) (secrets.KeyProvider, error) {
			return secrets.NewMemoryKeyRing(active, decryptOnly...)
		},
	}
}
