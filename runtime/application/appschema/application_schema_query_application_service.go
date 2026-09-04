package appschema

import (
	"context"
	"strings"

	metadatasdk "github.com/domainry/domainry-metadata-sdk"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	metadatadomain "github.com/domainry/domainry-runtime/runtime/domain/appschema/service"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordprojection "github.com/domainry/domainry-runtime/runtime/domain/record/projection"
)

// metadataObjectRecordCountAction is owned by runtime:metadata and is the
// exact Action/Permission for the Runtime-hosted record-count use case.
const metadataObjectRecordCountAction = "runtime.appschema.metadata_object_record_count"

func (s *ApplicationSchemaApplicationService) CurrentManifest(ctx context.Context, principal principalmodel.Principal) (manifestmodel.ManifestSchema, error) {
	if err := metadataAuthorizeQuery(principal); err != nil {
		return manifestmodel.ManifestSchema{}, err
	}
	manifest, err := s.repository.LoadManifest(ctx, metadataInstallationScope("load current Runtime manifest for global validation"))
	return manifest, wrapMetadataError(err)
}

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

func (s *ApplicationSchemaApplicationService) ApplicationSchemaMigrationPlan(ctx context.Context, principal principalmodel.Principal) ([]appschemamodel.ApplicationSchemaMigrationStep, error) {
	if err := metadataAuthorizeQuery(principal); err != nil {
		return nil, err
	}
	manifest, err := s.repository.LoadManifest(ctx, metadataInstallationScope("load metadata migration manifest"))
	if err != nil {
		return nil, err
	}
	steps, err := s.repository.MigrationPlan(ctx, metadataInstallationScope("plan metadata migration"), manifest)
	return steps, wrapMetadataError(err)
}

// ApplicationSchemaObjectRecordCount exposes only the aggregate needed to validate
// Schema changes. It deliberately bypasses business record data scopes so a
// metadata administrator never has to receive record payloads merely to decide
// whether a required field needs a default value.
func (s *ApplicationSchemaApplicationService) ApplicationSchemaObjectRecordCount(ctx context.Context, objectKey string, principal principalmodel.Principal) (int, error) {
	if err := metadataAuthorizeQuery(principal); err != nil {
		return 0, err
	}
	if !principal.HasExactPermission(metadataObjectRecordCountAction) {
		return 0, forbidden("auth.permission_denied")
	}
	objectKey = strings.TrimSpace(objectKey)
	var objectFound bool
	var object definitionmodel.ObjectSchema
	for _, candidate := range s.runtime.Schema().Objects {
		if candidate.Key == objectKey {
			object, objectFound = candidate, true
			break
		}
	}
	if !objectFound {
		return 0, notFound("backend.metadata.object_not_found", "object", objectKey)
	}
	if s.records == nil {
		return 0, metadataInternalError("count metadata object records")
	}
	page, err := s.records.ListRecords(ctx, principalmodel.InstallationWorkspaceID, object, recordmodel.RecordListQuery{Page: 1, PageSize: 1, AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted})
	if err != nil {
		return 0, wrapMetadataError(err)
	}
	return page.Total, nil
}
