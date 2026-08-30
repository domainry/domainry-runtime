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
	ListDefinitions(ctx context.Context, scope principalmodel.SystemScope, resourceType string) ([]appschemamodel.ApplicationDefinition, error)
	GetDefinition(ctx context.Context, scope principalmodel.SystemScope, resourceType, resourceKey string) (appschemamodel.ApplicationDefinition, bool, error)
	ListLocalizedTexts(ctx context.Context, workspaceID string, query appschemamodel.LocalizedTextQuery) ([]appschemamodel.LocalizedText, error)
}
