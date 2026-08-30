package appschema

import (
	"context"

	metadataauthoring "github.com/domainry/domainry-runtime/runtime/domain/appschema/contract"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func (s *ApplicationSchemaApplicationService) normalizeAndValidateFieldMetadataMutation(ctx context.Context, req appschemamodel.ApplicationDefinitionUpsertRequest) (appschemamodel.ApplicationDefinitionUpsertRequest, error) {
	return ApplicationSchemaNormalizeFieldMutation(ctx, req, s.runtime.Schema().Objects, metadataauthoring.ApplicationSchemaAuthoringFieldTypes(), func(ctx context.Context, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		return s.records.ListRecords(ctx, principalmodel.InstallationWorkspaceID, object, query)
	})
}

// ReferenceGraph is a read-only projection used by human and model clients to
// prepare the same workspace-scoped system draft. Definition mutations are
// intentionally absent from ApplicationSchemaApplicationService: reviewed Change Plan
// publication is the only production authoring command boundary.
func (s *ApplicationSchemaApplicationService) ReferenceGraph(ctx context.Context, principal principalmodel.Principal) (changeplanmodel.ReferenceGraph, error) {
	if err := metadataAuthorizeQuery(principal); err != nil {
		return changeplanmodel.ReferenceGraph{}, err
	}
	if s.references == nil {
		return changeplanmodel.ReferenceGraph{}, metadataInternalError("resolve metadata reference graph")
	}
	return s.references.Graph(ctx, principal)
}
