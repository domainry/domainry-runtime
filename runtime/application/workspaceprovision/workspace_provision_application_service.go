package workspaceprovision

import (
	"context"
	"errors"

	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workspaceprovisionmodel "github.com/domainry/domainry-runtime/runtime/domain/workspaceprovision/model"
	workspaceprovisionrepository "github.com/domainry/domainry-runtime/runtime/domain/workspaceprovision/repository"
	workspaceprovisionvalidation "github.com/domainry/domainry-runtime/runtime/domain/workspaceprovision/validation"
)

const (
	ProvisionActionKey = "runtime.workspaceprovision.provision_workspace"
)

type WorkspaceProvisionApplicationService struct {
	repository workspaceprovisionrepository.WorkspaceProvisionRepository
}

func NewWorkspaceProvisionApplicationService(repository workspaceprovisionrepository.WorkspaceProvisionRepository) *WorkspaceProvisionApplicationService {
	return &WorkspaceProvisionApplicationService{repository: repository}
}

func (service *WorkspaceProvisionApplicationService) Provision(ctx context.Context, principal principalmodel.Principal, request workspaceprovisionmodel.Request) (workspaceprovisionmodel.Result, error) {
	if err := authorizeWorkspaceAdministration(principal, ProvisionActionKey); err != nil {
		return workspaceprovisionmodel.Result{}, err
	}
	if service == nil || service.repository == nil {
		return workspaceprovisionmodel.Result{}, workspaceProvisionError(workspaceprovisionmodel.ErrIdentityUnavailable)
	}
	request = workspaceprovisionvalidation.NormalizeRequest(request)
	if err := workspaceprovisionvalidation.ValidateRequest(request); err != nil {
		return workspaceprovisionmodel.Result{}, workspaceProvisionError(err)
	}
	result, err := service.repository.Provision(ctx, request)
	if errors.Is(err, workspaceprovisionmodel.ErrAcceptanceFailure) {
		return workspaceprovisionmodel.Result{}, workspaceProvisionError(err)
	}
	return result, workspaceProvisionError(err)
}

func workspaceProvisionError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, workspaceprovisionmodel.ErrInvalid):
		return &apperror.AppError{Kind: apperror.KindBadRequest, Code: "workspace.provision_request_invalid", Err: err}
	case errors.Is(err, workspaceprovisionmodel.ErrIdempotencyConflict):
		return &apperror.AppError{Kind: apperror.KindConflict, Code: "workspace.provision_idempotency_conflict", Err: err}
	case errors.Is(err, workspaceprovisionmodel.ErrCodeConflict):
		return &apperror.AppError{Kind: apperror.KindConflict, Code: "workspace.canonical_code_conflict", Err: err}
	case errors.Is(err, workspaceprovisionmodel.ErrWorkspaceNotFound):
		return &apperror.AppError{Kind: apperror.KindNotFound, Code: "workspace.not_found", Err: err}
	case errors.Is(err, workspaceprovisionmodel.ErrIdentityUnavailable):
		return &apperror.AppError{Kind: apperror.KindUnavailable, Code: "workspace.identity_atomic_provisioning_unavailable", Err: err}
	case errors.Is(err, workspaceprovisionmodel.ErrLegacyAdjudicationRequired):
		return &apperror.AppError{Kind: apperror.KindConflict, Code: "workspace.legacy_identity_graph_adjudication_required", Err: err}
	case errors.Is(err, workspaceprovisionmodel.ErrAcceptanceFailure):
		return &apperror.AppError{Kind: apperror.KindInternal, Code: "workspace.provision_failed", Err: err}
	default:
		return err
	}
}
