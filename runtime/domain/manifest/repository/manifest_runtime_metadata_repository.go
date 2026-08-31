package repository

import (
	"context"

	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type ManifestRuntimeMetadataRepository interface {
	SyncManifestProjection(context.Context, principalmodel.SystemScope, manifestmodel.ManifestSchema) error
	LoadManifest(context.Context, principalmodel.SystemScope) (manifestmodel.ManifestSchema, error)
	SyncManifest(context.Context, principalmodel.SystemScope, manifestmodel.ManifestSchema) error
}
