package changeplan

import (
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	changeplanrepository "github.com/domainry/domainry-runtime/runtime/domain/changeplan/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"context"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"

	"strconv"

	"github.com/domainry/domainry-foundation/apperror"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

type ReferenceRuntime interface {
	WorkflowProcesses(context.Context, principalmodel.Principal, workflowmodel.WorkflowProcessFilter) ([]workflowmodel.WorkflowProcessInstance, error)
	PublishedSchedulerDefinitions(context.Context, principalmodel.Principal) ([]recordmodel.Record, error)
	SnapshotObjectRecords(context.Context, string, principalmodel.Principal, int) ([]recordmodel.Record, error)
	ListIntegrationOutboxMessages(context.Context, string, string, int, principalmodel.Principal) ([]integrationmodel.IntegrationOutboxMessage, error)
}

// ChangePlanReferenceApplicationService resolves change-plan dependency graphs.
type ChangePlanReferenceApplicationService struct {
	schema   func(context.Context, principalmodel.Principal) ReferenceSchema
	runtime  ReferenceRuntime
	evidence changeplanrepository.ChangePlanEvidenceRepository
}

func NewChangePlanReferenceApplicationService(schema func(context.Context, principalmodel.Principal) ReferenceSchema, runtime ReferenceRuntime, evidence changeplanrepository.ChangePlanEvidenceRepository) *ChangePlanReferenceApplicationService {
	return &ChangePlanReferenceApplicationService{schema: schema, runtime: runtime, evidence: evidence}
}

func (s *ChangePlanReferenceApplicationService) Graph(ctx context.Context, principal principalmodel.Principal) (changeplanmodel.ReferenceGraph, error) {
	if err := changePlanAuthorizeQuery(principal); err != nil {
		return changeplanmodel.ReferenceGraph{}, err
	}
	if !principal.HasPermission("workspace.admin") {
		return changeplanmodel.ReferenceGraph{}, &apperror.AppError{Kind: apperror.KindForbidden, Code: "auth.permission_denied"}
	}
	builder := newChangePlanReferenceGraphBuilder()
	snapshot := ReferenceSchema{}
	if s.schema != nil {
		snapshot = s.schema(ctx, principal)
	}
	AddSchemaReferences(builder, snapshot)
	AddIdentityProfileReferences(builder, snapshot)
	AddActionReferences(builder, snapshot)
	AddWorkflowReferences(builder, snapshot)
	AddAutomationReferences(builder, snapshot)
	AddReportIntegrationReferences(builder, snapshot)
	AddPresentationReferences(builder, snapshot)
	if err := s.addSeedProvenanceReferences(ctx, builder); err != nil {
		return changeplanmodel.ReferenceGraph{}, err
	}
	if err := s.addSchedulerReferences(ctx, builder, principal); err != nil {
		return changeplanmodel.ReferenceGraph{}, err
	}
	if err := s.addRuntimeEvidenceReferences(ctx, builder, principal); err != nil {
		return changeplanmodel.ReferenceGraph{}, err
	}
	return builder.Graph(), nil
}

func referenceInternalError(operation string, err error) error {
	return &apperror.AppError{Kind: apperror.KindInternal, Code: "backend.internal", Params: map[string]string{"operation": operation}, Err: err}
}

func stringIndex(index int) string { return strconv.Itoa(index) }
