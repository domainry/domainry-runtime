package repository

import (
	"context"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type ApplicationSchemaRepository interface {
	SnapshotRevision(ctx context.Context, scope principalmodel.SystemScope) (string, error)
	LoadManifest(ctx context.Context, scope principalmodel.SystemScope) (manifestmodel.ManifestSchema, error)
	SyncManifest(ctx context.Context, scope principalmodel.SystemScope, manifest manifestmodel.ManifestSchema) error
	MigrationPlan(ctx context.Context, scope principalmodel.SystemScope, manifest manifestmodel.ManifestSchema) ([]appschemamodel.ApplicationSchemaMigrationStep, error)
	UpgradePlan(ctx context.Context, scope principalmodel.SystemScope, previous *manifestmodel.ManifestSchema, next manifestmodel.ManifestSchema) (appschemamodel.ApplicationSchemaUpgradePlan, error)
}

// ApplicationExecutionConfigurationReader reads the immutable configuration
// needed by an Action without loading the full metadata definition catalog.
type ApplicationExecutionConfigurationReader interface {
	ExecutionConfiguration(context.Context, principalmodel.SystemScope) (appschemamodel.ApplicationExecutionConfiguration, error)
}
