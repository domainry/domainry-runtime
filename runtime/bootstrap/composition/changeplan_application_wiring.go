package composition

import (
	"context"
	actionapplication "github.com/domainry/domainry-runtime/runtime/application/action"
	appschemaapplication "github.com/domainry/domainry-runtime/runtime/application/appschema"
	changeplanapplication "github.com/domainry/domainry-runtime/runtime/application/changeplan"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	appschemarepository "github.com/domainry/domainry-runtime/runtime/domain/appschema/repository"
	appschemavalidation "github.com/domainry/domainry-runtime/runtime/domain/appschema/validation"
	auditrepository "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	changeplanrepository "github.com/domainry/domainry-runtime/runtime/domain/changeplan/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func newChangePlanApplicationService(repository changeplanrepository.ChangePlanRepository, metadata appschemarepository.ApplicationSchemaRepository, audit auditrepository.AuditRepository, runtime *appschemaapplication.ApplicationSchemaApplicationService, actionServices ...*actionapplication.ActionApplicationService) *changeplanapplication.ChangePlanApplicationService {
	var runtimePort changeplanapplication.Runtime
	if runtime != nil {
		runtimePort = changePlanMetadataRuntimeAdapter{metadata: runtime}
	}
	_ = actionServices
	return changeplanapplication.NewChangePlanApplicationService(repository, metadata, audit, runtimePort)
}

type changePlanMetadataRuntimeAdapter struct {
	metadata *appschemaapplication.ApplicationSchemaApplicationService
}

func (adapter changePlanMetadataRuntimeAdapter) ValidateApplicationDefinitionPayload(ctx context.Context, resourceType, resourceKey string, request appschemamodel.ApplicationDefinitionUpsertRequest) (appschemamodel.ApplicationDefinitionUpsertRequest, error) {
	if resourceType == "dictionary" {
		if err := appschemavalidation.ApplicationSchemaValidateDictionaryDefinition(resourceKey, request.Payload); err != nil {
			return appschemamodel.ApplicationDefinitionUpsertRequest{}, err
		}
	}
	return adapter.metadata.ValidateApplicationDefinitionPayload(ctx, resourceType, request)
}

func (adapter changePlanMetadataRuntimeAdapter) CanonicalizeMetadataCandidate(ctx context.Context, mutations []appschemamodel.ApplicationDefinitionMutation) ([]appschemamodel.ApplicationDefinitionMutation, error) {
	return adapter.metadata.CanonicalizeMetadataCandidate(ctx, mutations)
}

func (adapter changePlanMetadataRuntimeAdapter) ReloadApplicationSchema(ctx context.Context, principal principalmodel.Principal) (string, error) {
	snapshot, err := adapter.metadata.ReloadApplicationSchema(ctx, principal)
	return snapshot.SchemaHash, err
}
