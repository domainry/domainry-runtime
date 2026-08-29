package metadata

import (
	"context"

	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	metadataauthoring "github.com/domainry/domainry-runtime/runtime/domain/metadata/contract"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func (s *ApplicationSchemaService) normalizeAndValidateFieldMetadataMutation(ctx context.Context, req metadatamodel.MetadataDefinitionUpsertRequest) (metadatamodel.MetadataDefinitionUpsertRequest, error) {
	return MetadataNormalizeFieldMutation(ctx, req, s.runtime.Schema().Objects, metadataauthoring.MetadataAuthoringFieldTypes(), func(ctx context.Context, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
		return s.records.ListRecords(ctx, principalmodel.InstallationWorkspaceID, object, query)
	})
}

// ReferenceGraph is a read-only projection used by human and model clients to
// prepare the same workspace-scoped system draft. Definition mutations are
// intentionally absent from ApplicationSchemaService: reviewed Change Plan
// publication is the only production authoring command boundary.
func (s *ApplicationSchemaService) ReferenceGraph(ctx context.Context, principal principalmodel.Principal) (changeplanmodel.ReferenceGraph, error) {
	if err := metadataAuthorizeQuery(principal); err != nil {
		return changeplanmodel.ReferenceGraph{}, err
	}
	if s.references == nil {
		return changeplanmodel.ReferenceGraph{}, metadataInternalError("resolve metadata reference graph")
	}
	return s.references.Graph(ctx, principal)
}
