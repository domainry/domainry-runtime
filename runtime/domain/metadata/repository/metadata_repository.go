package repository

import (
	"context"

	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type MetadataRepository interface {
	SnapshotRevision(ctx context.Context, scope principalmodel.SystemScope) (string, error)
	LoadManifest(ctx context.Context, scope principalmodel.SystemScope) (manifestmodel.ManifestSchema, error)
	SyncManifest(ctx context.Context, scope principalmodel.SystemScope, manifest manifestmodel.ManifestSchema) error
	MigrationPlan(ctx context.Context, scope principalmodel.SystemScope, manifest manifestmodel.ManifestSchema) ([]metadatamodel.MetadataMigrationStep, error)
	PublishDefinition(ctx context.Context, scope principalmodel.SystemScope, resourceType, resourceKey string, req metadatamodel.MetadataDefinitionUpsertRequest, audit auditmodel.AuditEvent) (metadatamodel.MetadataDefinition, error)
	CompleteDefinitionRefresh(ctx context.Context, scope principalmodel.SystemScope, resourceType, resourceKey, schemaHash, errorText string) error
	ApplyDefinitionMutations(ctx context.Context, scope principalmodel.SystemScope, mutations []metadatamodel.MetadataDefinitionMutation, audits []auditmodel.AuditEvent, publication *changeplanmodel.BusinessChangePlanPublication) ([]metadatamodel.MetadataDefinition, error)
	DisableDefinition(ctx context.Context, scope principalmodel.SystemScope, resourceType, resourceKey string) error
	ListDefinitions(ctx context.Context, scope principalmodel.SystemScope, resourceType string) ([]metadatamodel.MetadataDefinition, error)
	GetDefinition(ctx context.Context, scope principalmodel.SystemScope, resourceType, resourceKey string) (metadatamodel.MetadataDefinition, bool, error)
	ListDefinitionVersions(ctx context.Context, scope principalmodel.SystemScope, resourceType, resourceKey string) ([]metadatamodel.MetadataDefinitionVersion, error)
	RollbackDefinition(ctx context.Context, scope principalmodel.SystemScope, resourceType, resourceKey string, req metadatamodel.MetadataDefinitionRollbackRequest, audit auditmodel.AuditEvent) (metadatamodel.MetadataDefinition, error)
	ListLocalizedTexts(ctx context.Context, workspaceID string, query metadatamodel.LocalizedTextQuery) ([]metadatamodel.LocalizedText, error)
	UpsertLocalizedText(ctx context.Context, workspaceID string, req metadatamodel.LocalizedTextUpsertRequest) (metadatamodel.LocalizedText, error)
}

// DefinitionMutationRepository atomically persists a reviewed definition
// mutation set and its mandatory audit/publication evidence.
type DefinitionMutationRepository interface {
	ApplyDefinitionMutations(context.Context, principalmodel.SystemScope, []metadatamodel.MetadataDefinitionMutation, []auditmodel.AuditEvent, *changeplanmodel.BusinessChangePlanPublication) ([]metadatamodel.MetadataDefinition, error)
}
