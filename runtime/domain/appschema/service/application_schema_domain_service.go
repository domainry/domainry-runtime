package service

import (
	"context"

	collectionplatform "github.com/domainry/domainry-foundation/collection"
	metadatasdk "github.com/domainry/domainry-metadata-sdk"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

type CapabilityAuthoringSchemaProvider interface {
	SchemaForPrincipal(context.Context, principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot
}

// ApplicationSchemaDomainService owns read-only schema and permission projections.
// Its dependency is the immutable snapshot contract, not RuntimeServices state.
// ApplicationSchemaDomainService exposes the current authoring schema.
type ApplicationSchemaDomainService struct {
	schema   CapabilityAuthoringSchemaProvider
	metadata metadatasdk.Localization
}

func NewApplicationSchemaDomainService(schema CapabilityAuthoringSchemaProvider, metadata metadatasdk.Localization) *ApplicationSchemaDomainService {
	return &ApplicationSchemaDomainService{schema: schema, metadata: metadata}
}

func (s *ApplicationSchemaDomainService) Snapshot(ctx context.Context) appschemamodel.ApplicationSchemaSnapshot {
	return s.schema.SchemaForPrincipal(ctx, principalmodel.Principal{})
}

func (s *ApplicationSchemaDomainService) ForPrincipal(ctx context.Context, principal principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot {
	return s.schema.SchemaForPrincipal(ctx, principal)
}

func (s *ApplicationSchemaDomainService) ObjectMap(ctx context.Context) map[string]definitionmodel.ObjectSchema {
	return collectionplatform.IndexBy(s.Snapshot(ctx).Objects, func(object definitionmodel.ObjectSchema) string { return object.Key })
}
