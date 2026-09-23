package composition

import (
	"context"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	actionapplication "github.com/domainry/domainry-runtime/runtime/application/action"
	pipelineapplication "github.com/domainry/domainry-runtime/runtime/application/pipeline"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	actionbusiness "github.com/domainry/domainry-runtime/runtime/domain/action/service"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
)

// CanonicalSchemaProvider exposes the immutable project schema to internal
// composition. Authorization filters are applied only at the consuming
// application boundary.
type CanonicalSchemaProvider interface {
	Schema() appschemamodel.ApplicationSchemaSnapshot
}

func newRecordValidationService(schema CanonicalSchemaProvider, repository recordrepository.RecordRepository, access pipelineRecordAccess, identity identitysdk.Projection) *recordservice.RecordValidationDomainService {
	object := func(_ context.Context, key string) (definitionmodel.ObjectSchema, bool) {
		value, ok := schemaObjectMap(schema.Schema().Objects)[key]
		return value, ok
	}
	return recordservice.NewRecordValidationDomainService(recordservice.RecordValidationDependencies{
		Repository: repository, Object: object, CanAccess: access.canAccessRecord,
		CanAccessPersisted: access.canAccessPersistedRecord, Identity: identity,
	})
}

func newRecordStateMachineEffects() *recordapplication.RecordStateMachineEffectApplicationService {
	return recordapplication.NewRecordStateMachineEffectApplicationService(recordapplication.RecordStateMachineEffectDependencies{
		ApplySelfPatch: func(_ context.Context, transition, data map[string]any, sourceRecordID string, principal principalmodel.Principal) bool {
			return actionbusiness.ActionApplyTransitionSelfPatch(transition, data, sourceRecordID, principal)
		},
	})
}

func workflowSchemaByKey(_ context.Context, schema CanonicalSchemaProvider, key string) (definitionmodel.WorkflowSchema, bool) {
	for _, value := range schema.Schema().Workflows {
		if value.Key == key {
			return value, true
		}
	}
	return definitionmodel.WorkflowSchema{}, false
}

func newActionPreconditionService(pipeline *pipelineapplication.PipelineApplicationService) *actionapplication.ActionPreconditionApplicationService {
	return actionapplication.NewActionPreconditionApplicationService(actionapplication.ActionPreconditionDependencies{
		PipelineStage: func(ctx context.Context, workspaceID, stageID string) (recordmodel.Record, error) {
			return pipeline.GetStage(ctx, workspaceID, stageID)
		},
	})
}

type recordQueryPolicy interface {
	objectForAction(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error)
	ensureReportSnapshotAccess(definitionmodel.ObjectSchema, string, principalmodel.Principal) error
	normalizeListQuery(definitionmodel.ObjectSchema, recordmodel.RecordListQuery, principalmodel.Principal) recordmodel.RecordListQuery
	canAccessRecord(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool
	canWriteRecordScope(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool
}

type recordReadPolicyAdapter struct{ policy recordQueryPolicy }

type recordQueryPolicyAdapter struct {
	service *recordservice.RecordQueryPolicyDomainService
}

var (
	_ recordQueryPolicy    = recordQueryPolicyAdapter{}
	_ pipelineRecordAccess = recordQueryPolicyAdapter{}
)

func (a recordQueryPolicyAdapter) objectForAction(principal principalmodel.Principal, objectKey, action string) (definitionmodel.ObjectSchema, error) {
	return a.service.ObjectForAction(principal, objectKey, action)
}

func (a recordQueryPolicyAdapter) ensureReportSnapshotAccess(object definitionmodel.ObjectSchema, action string, principal principalmodel.Principal) error {
	return a.service.EnsureReportSnapshotAccess(object, action, principal)
}
