package database

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/domainry/domainry-foundation/telemetry"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/postgres"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

type runtimeConnectionResult struct {
	database             *sql.DB
	migrationDatabase    *sql.DB
	dsn                  string
	databaseSchema       string
	postgresProfile      *postgres.ConnectionProfile
	postgresCapabilities postgres.Capabilities
	migratorCapabilities postgres.Capabilities
}

type runtimeConnectionStrategy func(context.Context, databaseEngine, config.Config, runtimeOpenDependencies, *telemetry.SQLMetrics) (runtimeConnectionResult, error)

var runtimeConnectionStrategies = map[string]runtimeConnectionStrategy{
	"postgres": openRuntimePostgresConnection,
}

func openRuntimeConnection(ctx context.Context, selected databaseEngine, cfg config.Config, dependencies runtimeOpenDependencies, metrics *telemetry.SQLMetrics) (runtimeConnectionResult, error) {
	strategy := openRuntimeStandardConnection
	if registered, found := runtimeConnectionStrategies[string(selected.Name())]; found {
		strategy = registered
	}
	return strategy(ctx, selected, cfg, dependencies, metrics)
}

func openRuntimeStandardConnection(_ context.Context, selected databaseEngine, cfg config.Config, dependencies runtimeOpenDependencies, metrics *telemetry.SQLMetrics) (runtimeConnectionResult, error) {
	dsn, err := selected.DSN(cfg)
	if err != nil {
		return runtimeConnectionResult{}, err
	}
	database, err := dependencies.observedSQL(selected.SQLDriver(), dsn, "runtime", metrics)
	if err != nil {
		return runtimeConnectionResult{}, err
	}
	return runtimeConnectionResult{database: database, dsn: dsn}, nil
}

func openRuntimePostgresConnection(ctx context.Context, _ databaseEngine, cfg config.Config, dependencies runtimeOpenDependencies, metrics *telemetry.SQLMetrics) (runtimeConnectionResult, error) {
	connection, err := dependencies.postgresProfile(cfg)
	if err != nil {
		return runtimeConnectionResult{}, err
	}
	database, err := connection.Open(metrics)
	if err != nil {
		return runtimeConnectionResult{}, err
	}
	result := runtimeConnectionResult{database: database, postgresProfile: connection.Profile()}
	result.databaseSchema = result.postgresProfile.Schema
	if result.postgresProfile.MigrationConfigured {
		result.migrationDatabase, err = connection.OpenMigration(metrics)
		if err != nil {
			_ = database.Close()
			return runtimeConnectionResult{}, err
		}
		if err := result.migrationDatabase.PingContext(ctx); err != nil {
			_ = result.migrationDatabase.Close()
			_ = database.Close()
			return runtimeConnectionResult{}, fmt.Errorf("connect postgres migration database (%s)", postgres.ClassifyConnectionFailure(err))
		}
	}
	result.postgresCapabilities, err = connection.ProbeWithBackoff(ctx, database)
	if err != nil {
		result.close()
		return runtimeConnectionResult{}, fmt.Errorf("probe postgres query connection (%s)", postgres.ClassifyConnectionFailure(err))
	}
	if result.migrationDatabase != nil {
		result.migratorCapabilities, err = connection.ProbeWithBackoff(ctx, result.migrationDatabase)
		if err != nil {
			result.close()
			return runtimeConnectionResult{}, fmt.Errorf("probe postgres migration connection (%s)", postgres.ClassifyConnectionFailure(err))
		}
	}
	if err := connection.ValidateRuntimeCapabilities(result.postgresCapabilities, result.migratorCapabilities); err != nil {
		result.close()
		return runtimeConnectionResult{}, err
	}
	return result, nil
}

func (result runtimeConnectionResult) close() {
	if result.migrationDatabase != nil {
		_ = result.migrationDatabase.Close()
	}
	if result.database != nil {
		_ = result.database.Close()
	}
}
