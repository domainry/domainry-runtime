package repository

import (
	"context"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type ApplicationSchemaRepository interface {
	SnapshotRevision(ctx context.Context, scope principalmodel.SystemScope) (string, error)
	LoadManifest(ctx context.Context, scope principalmodel.SystemScope) (manifestmodel.ManifestSchema, error)
	SyncManifest(ctx context.Context, scope principalmodel.SystemScope, manifest manifestmodel.ManifestSchema) error
	MigrationPlan(ctx context.Context, scope principalmodel.SystemScope, manifest manifestmodel.ManifestSchema) ([]appschemamodel.ApplicationSchemaMigrationStep, error)
	PublishDefinition(ctx context.Context, scope principalmodel.SystemScope, resourceType, resourceKey string, req appschemamodel.ApplicationDefinitionUpsertRequest, audit auditmodel.AuditEvent) (appschemamodel.ApplicationDefinition, error)
	CompleteDefinitionRefresh(ctx context.Context, scope principalmodel.SystemScope, resourceType, resourceKey, schemaHash, errorText string) error
	ApplyDefinitionMutations(ctx context.Context, scope principalmodel.SystemScope, mutations []appschemamodel.ApplicationDefinitionMutation, audits []auditmodel.AuditEvent, publication *changeplanmodel.BusinessChangePlanPublication) ([]appschemamodel.ApplicationDefinition, error)
	DisableDefinition(ctx context.Context, scope principalmodel.SystemScope, resourceType, resourceKey string) error
	ListDefinitions(ctx context.Context, scope principalmodel.SystemScope, resourceType string) ([]appschemamodel.ApplicationDefinition, error)
	GetDefinition(ctx context.Context, scope principalmodel.SystemScope, resourceType, resourceKey string) (appschemamodel.ApplicationDefinition, bool, error)
	ListDefinitionVersions(ctx context.Context, scope principalmodel.SystemScope, resourceType, resourceKey string) ([]appschemamodel.ApplicationDefinitionVersion, error)
	RollbackDefinition(ctx context.Context, scope principalmodel.SystemScope, resourceType, resourceKey string, req appschemamodel.ApplicationDefinitionRollbackRequest, audit auditmodel.AuditEvent) (appschemamodel.ApplicationDefinition, error)
	ListLocalizedTexts(ctx context.Context, workspaceID string, query appschemamodel.LocalizedTextQuery) ([]appschemamodel.LocalizedText, error)
	UpsertLocalizedText(ctx context.Context, workspaceID string, req appschemamodel.LocalizedTextUpsertRequest) (appschemamodel.LocalizedText, error)
}

// DefinitionMutationRepository atomically persists a reviewed definition
// mutation set and its mandatory audit/publication evidence.
type DefinitionMutationRepository interface {
	ApplyDefinitionMutations(context.Context, principalmodel.SystemScope, []appschemamodel.ApplicationDefinitionMutation, []auditmodel.AuditEvent, *changeplanmodel.BusinessChangePlanPublication) ([]appschemamodel.ApplicationDefinition, error)
}
