package appschema

import (
	"context"

	metadatasdk "github.com/domainry/domainry-metadata-sdk"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	metadatadomain "github.com/domainry/domainry-runtime/runtime/domain/appschema/service"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"
	recordprojection "github.com/domainry/domainry-runtime/runtime/domain/record/projection"
)

type ApplicationSchemaQueryApplicationService struct {
	*metadatadomain.ApplicationSchemaDomainService
}

type ApplicationSchemaProvider interface {
	SchemaForPrincipal(context.Context, principalmodel.Principal) appschemamodel.ApplicationSchemaSnapshot
}

func NewApplicationSchemaQueryApplicationService(schema ApplicationSchemaProvider, localization metadatasdk.Localization) *ApplicationSchemaQueryApplicationService {
	return &ApplicationSchemaQueryApplicationService{ApplicationSchemaDomainService: metadatadomain.NewApplicationSchemaDomainService(schema, localization)}
}

func (s *ApplicationSchemaQueryApplicationService) FeaturePermissions(ctx context.Context, principal principalmodel.Principal) (recordcontract.RecordFeaturePermissionSnapshot, error) {
	if err := metadataAuthorizeQuery(principal); err != nil {
		return recordcontract.RecordFeaturePermissionSnapshot{}, err
	}
	snapshot := s.Snapshot(ctx)
	return recordprojection.RecordBuildFeaturePermissions(snapshot.Objects, snapshot.Actions, snapshot.Workflows, principal)
}
