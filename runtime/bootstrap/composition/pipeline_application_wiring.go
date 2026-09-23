package composition

import (
	"context"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	actionapplication "github.com/domainry/domainry-runtime/runtime/application/action"
	pipelineapplication "github.com/domainry/domainry-runtime/runtime/application/pipeline"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	transactionmodel "github.com/domainry/domainry-runtime/runtime/domain/transaction/model"
)

type pipelineRecordAccess interface {
	canAccessRecord(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool
	canAccessPersistedRecord(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) (bool, error)
}

// newPipelineApplicationService is composition-only wiring. Pipeline behavior
// lives in application/pipeline and receives only the ports it owns.
func newPipelineApplicationService(
	schema CanonicalSchemaProvider,
	repository recordrepository.RecordRepository,
	access pipelineRecordAccess,
) *pipelineapplication.PipelineApplicationService {
	return pipelineapplication.NewPipelineApplicationService(pipelineapplication.PipelineDependencies{
		Repository: repository,
		Object: func(_ context.Context, key string) (definitionmodel.ObjectSchema, bool) {
			object, ok := schemaObjectMap(schema.Schema().Objects)[key]
			return object, ok
		},
		CanAccess: func(principal principalmodel.Principal, object definitionmodel.ObjectSchema, record recordmodel.Record) bool {
			return access == nil || access.canAccessRecord(principal, object, record)
		},
	})
}

func newPipelineTransitionApplicationService(records *runtimeAssembly) *pipelineapplication.PipelineTransitionApplicationService {
	workflowRuntime := recordApplicationRuntimeAdapter{records: records}
	return pipelineapplication.NewPipelineTransitionApplicationService(pipelineapplication.PipelineTransitionDependencies{
		Pipeline:        records.PipelineApplicationService,
		ActionAllowed:   actionapplication.ActionAllowed,
		ObjectForAction: records.RecordQueryPolicyDomainService.ObjectForAction,
		GetRecord: func(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, recordID string) (recordmodel.Record, bool, error) {
			if records.recordRepo == nil {
				return recordmodel.Record{}, false, nil
			}
			return records.recordRepo.GetRecord(ctx, workspaceID, object, recordID)
		},
		CanAccess:         records.RecordQueryPolicyDomainService.CanAccessRecord,
		CheckPrecondition: records.ActionPreconditionApplicationService.Check,
		Audit: func(ctx context.Context, event, objectKey, recordID string, principal principalmodel.Principal, summary string, before, after, metadata map[string]any) {
			records.auditApplicationService.AppendWithMetadata(ctx, auditmodel.EventFamilyBusinessEntity, event, objectKey, recordID, principal, summary, before, after, metadata)
		},
		PlanUpdate: records.recordApplicationService.PlanUpdateMutation,
		PlanCreate: records.recordApplicationService.PlanCreateMutation,
		CommitPlans: func(ctx context.Context, plans []transactionmodel.MutationPlan) error {
			return records.mutationKernel.CommitBatch(ctx, plans, nil)
		},
		ExecuteWorkflows: workflowRuntime.Execute,
	})
}
