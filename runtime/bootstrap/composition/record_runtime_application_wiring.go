package composition

import (
	"context"

	apperror "github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func (r recordApplicationRuntimeAdapter) Prepare(ctx context.Context, objectKey string, record recordmodel.Record, before map[string]any, principal principalmodel.Principal, trigger string) ([]workflowmodel.WorkflowExecution, []workflowmodel.WorkflowRunSummary, error) {
	return workflowapplication.WorkflowPrepareTriggerIntents(ctx, r.records.workflows, objectKey, record, before, principal, trigger)
}

func resolveIdentityPrincipal(ctx context.Context, target identitysdk.PrincipalResolver, userID, roleKey string) (principalmodel.Principal, error) {
	if target == nil {
		return principalmodel.Principal{}, apperror.New(
			apperror.KindInternal,
			"identity.principal_resolver_unavailable",
			nil,
			nil,
		)
	}
	resolution, err := target.Resolve(ctx, identitysdk.PrincipalResolutionRequest{
		SubjectID: identitysdk.SubjectID(userID), RoleKey: roleKey,
	})
	if err != nil {
		return principalmodel.Principal{}, err
	}
	resolution.Principal.AccessBundle = &resolution.AccessBundle
	return principalmodel.NewPrincipalFromIdentity(resolution.Principal, ""), nil
}

func (r recordApplicationRuntimeAdapter) Execute(ctx context.Context, intents []workflowmodel.WorkflowExecution, principal principalmodel.Principal) {
	executeCommittedWorkflowIntents(ctx, r.records, intents, principal)
}

func updateRecordMutationWithContext(ctx context.Context, services *runtimeAssembly, _ recordrepository.RecordRepository, objectKey, recordID string, patch map[string]any, principal principalmodel.Principal) (recordmodel.Record, error) {
	if services == nil || services.recordApplicationService == nil {
		return recordmodel.Record{}, apperror.New(apperror.KindInternal, "backend.internal", nil, map[string]string{"operation": "update record mutation without assembled record application service"})
	}
	return services.recordApplicationService.UpdateRecord(ctx, objectKey, recordID, patch, principal)
}
