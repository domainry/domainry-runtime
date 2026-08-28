package service

import (
	"context"

	collectionplatform "github.com/domainry/domainry-foundation/collection"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	metadatarepository "github.com/domainry/domainry-runtime/runtime/domain/metadata/repository"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

type CapabilityAuthoringSchemaProvider interface {
	SchemaForPrincipal(context.Context, principalmodel.Principal) metadatamodel.MetadataSchemaSnapshot
}

// MetadataSchemaDomainService owns read-only schema and permission projections.
// Its dependency is the immutable snapshot contract, not RuntimeServices state.
// MetadataSchemaDomainService exposes the current authoring schema.
type MetadataSchemaDomainService struct {
	schema   CapabilityAuthoringSchemaProvider
	metadata metadatarepository.MetadataRepository
}

func NewMetadataSchemaDomainService(schema CapabilityAuthoringSchemaProvider, metadata metadatarepository.MetadataRepository) *MetadataSchemaDomainService {
	return &MetadataSchemaDomainService{schema: schema, metadata: metadata}
}

func (s *MetadataSchemaDomainService) Snapshot(ctx context.Context) metadatamodel.MetadataSchemaSnapshot {
	return s.schema.SchemaForPrincipal(ctx, principalmodel.Principal{})
}

func (s *MetadataSchemaDomainService) ForPrincipal(ctx context.Context, principal principalmodel.Principal) metadatamodel.MetadataSchemaSnapshot {
	return s.schema.SchemaForPrincipal(ctx, principal)
}

func (s *MetadataSchemaDomainService) ObjectMap(ctx context.Context) map[string]definitionmodel.ObjectSchema {
	return collectionplatform.IndexBy(s.Snapshot(ctx).Objects, func(object definitionmodel.ObjectSchema) string { return object.Key })
}
