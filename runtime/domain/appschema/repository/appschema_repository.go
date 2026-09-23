package repository

import (
	"context"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type ApplicationSchemaRepository interface {
	SnapshotRevision(ctx context.Context, scope principalmodel.SystemScope) (string, error)
}

// ApplicationExecutionConfigurationReader reads the immutable configuration
// needed by an Action without loading the full metadata definition catalog.
type ApplicationExecutionConfigurationReader interface {
	ExecutionConfiguration(context.Context, principalmodel.SystemScope) (appschemamodel.ApplicationExecutionConfiguration, error)
}
