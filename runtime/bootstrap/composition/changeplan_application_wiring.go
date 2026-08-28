package composition

import (
	"context"
	actionapplication "github.com/domainry/domainry-runtime/runtime/application/action"
	changeplanapplication "github.com/domainry/domainry-runtime/runtime/application/changeplan"
	metadataapplication "github.com/domainry/domainry-runtime/runtime/application/metadata"
	auditrepository "github.com/domainry/domainry-runtime/runtime/domain/audit/repository"
	changeplanrepository "github.com/domainry/domainry-runtime/runtime/domain/changeplan/repository"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	metadatarepository "github.com/domainry/domainry-runtime/runtime/domain/metadata/repository"
	metadatavalidation "github.com/domainry/domainry-runtime/runtime/domain/metadata/validation"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func newChangePlanApplicationService(repository changeplanrepository.ChangePlanRepository, metadata metadatarepository.MetadataRepository, audit auditrepository.AuditRepository, runtime *metadataapplication.MetadataApplicationService, actionServices ...*actionapplication.ActionApplicationService) *changeplanapplication.ChangePlanApplicationService {
	var runtimePort changeplanapplication.Runtime
	if runtime != nil {
		runtimePort = changePlanMetadataRuntimeAdapter{metadata: runtime}
	}
	_ = actionServices
	return changeplanapplication.NewChangePlanApplicationService(repository, metadata, audit, runtimePort)
}

type changePlanMetadataRuntimeAdapter struct {
	metadata *metadataapplication.MetadataApplicationService
}

func (adapter changePlanMetadataRuntimeAdapter) ValidateMetadataDefinitionPayload(ctx context.Context, resourceType, resourceKey string, request metadatamodel.MetadataDefinitionUpsertRequest) (metadatamodel.MetadataDefinitionUpsertRequest, error) {
	if resourceType == "dictionary" {
		if err := metadatavalidation.MetadataValidateDictionaryDefinition(resourceKey, request.Payload); err != nil {
			return metadatamodel.MetadataDefinitionUpsertRequest{}, err
		}
	}
	return adapter.metadata.ValidateMetadataDefinitionPayload(ctx, resourceType, request)
}

func (adapter changePlanMetadataRuntimeAdapter) CanonicalizeMetadataCandidate(ctx context.Context, mutations []metadatamodel.MetadataDefinitionMutation) ([]metadatamodel.MetadataDefinitionMutation, error) {
	return adapter.metadata.CanonicalizeMetadataCandidate(ctx, mutations)
}

func (adapter changePlanMetadataRuntimeAdapter) ReloadMetadata(ctx context.Context, principal principalmodel.Principal) (string, error) {
	snapshot, err := adapter.metadata.ReloadMetadata(ctx, principal)
	return snapshot.SchemaHash, err
}
