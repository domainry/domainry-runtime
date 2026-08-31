package workspaceprovision

import (
	"context"
	"errors"

	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workspaceprovisionmodel "github.com/domainry/domainry-runtime/runtime/domain/workspaceprovision/model"
	workspaceprovisionrepository "github.com/domainry/domainry-runtime/runtime/domain/workspaceprovision/repository"
)

const (
	Permission          = "platform.workspace.provision"
	ReconcilePermission = "platform.workspace.roles.reconcile"
)

type WorkspaceProvisionApplicationService struct {
	repository workspaceprovisionrepository.WorkspaceProvisionRepository
}

func NewWorkspaceProvisionApplicationService(repository workspaceprovisionrepository.WorkspaceProvisionRepository) *WorkspaceProvisionApplicationService {
	return &WorkspaceProvisionApplicationService{repository: repository}
}

func (service *WorkspaceProvisionApplicationService) Provision(ctx context.Context, principal principalmodel.Principal, request workspaceprovisionmodel.Request) (workspaceprovisionmodel.Result, error) {
	if !principal.Known || !principal.HasExactPermission(Permission) {
		return workspaceprovisionmodel.Result{}, &apperror.AppError{Kind: apperror.KindForbidden, Code: "auth.permission_denied"}
	}
	if service == nil || service.repository == nil {
		return workspaceprovisionmodel.Result{}, workspaceProvisionError(workspaceprovisionmodel.ErrIdentityUnavailable)
	}
	result, err := service.repository.Provision(ctx, request)
	return result, workspaceProvisionError(err)
}

func (service *WorkspaceProvisionApplicationService) ReconcileWorkspaceRoles(ctx context.Context, principal principalmodel.Principal, workspaceID string) (workspaceprovisionmodel.RoleReconciliationResult, error) {
	if !principal.Known || !principal.HasExactPermission(ReconcilePermission) {
		return workspaceprovisionmodel.RoleReconciliationResult{}, &apperror.AppError{Kind: apperror.KindForbidden, Code: "auth.permission_denied"}
	}
	if service == nil || service.repository == nil {
		return workspaceprovisionmodel.RoleReconciliationResult{}, workspaceProvisionError(workspaceprovisionmodel.ErrIdentityUnavailable)
	}
	result, err := service.repository.ReconcileWorkspaceRoles(ctx, workspaceID)
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
	default:
		return err
	}
}
