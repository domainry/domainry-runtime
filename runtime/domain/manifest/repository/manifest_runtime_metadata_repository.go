package repository

import (
	"context"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type ManifestRuntimeMetadataRepository interface {
	SyncManifestProjection(context.Context, principalmodel.SystemScope, manifestmodel.ManifestSchema) error
	LoadManifest(context.Context, principalmodel.SystemScope) (manifestmodel.ManifestSchema, error)
	SyncManifest(context.Context, principalmodel.SystemScope, manifestmodel.ManifestSchema) error
	// LoadPreviousManifest returns nil when the database holds no projection yet.
	LoadPreviousManifest(context.Context, principalmodel.SystemScope) (*manifestmodel.ManifestSchema, error)
	// UpgradePlan evaluates the physical changes from previous (nil on a fresh
	// database) to next without mutating the database.
	UpgradePlan(context.Context, principalmodel.SystemScope, *manifestmodel.ManifestSchema, manifestmodel.ManifestSchema) (appschemamodel.ApplicationSchemaUpgradePlan, error)
	// ApplyUpgrade executes a non-blocking plan with backup and receipts and
	// returns the plan enriched with execution diagnostics.
	ApplyUpgrade(context.Context, principalmodel.SystemScope, appschemamodel.ApplicationSchemaUpgradePlan, manifestmodel.ManifestSchema) (appschemamodel.ApplicationSchemaUpgradePlan, error)
}
