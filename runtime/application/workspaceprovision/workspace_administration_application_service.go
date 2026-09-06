package workspaceprovision

import (
	"context"
	"errors"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workspaceprovisionmodel "github.com/domainry/domainry-runtime/runtime/domain/workspaceprovision/model"
	workspaceprovisionrepository "github.com/domainry/domainry-runtime/runtime/domain/workspaceprovision/repository"
	workspaceprovisionvalidation "github.com/domainry/domainry-runtime/runtime/domain/workspaceprovision/validation"
)

const (
	WorkspaceAdministratorRoleKey                   = "tenant_admin"
	ListWorkspacesActionKey                         = "runtime.workspaceprovision.list_workspaces"
	SuspendWorkspaceActionKey                       = "runtime.workspaceprovision.suspend_workspace"
	ReactivateWorkspaceActionKey                    = "runtime.workspaceprovision.reactivate_workspace"
	UpdateWorkspaceCommercialConfigurationActionKey = "runtime.workspaceprovision.update_commercial_configuration"
	workspaceCatalogDefaultPageSize                 = 50
	workspaceCatalogMaxPageSize                     = 100
)

type WorkspaceAdministrationApplicationService struct {
	repository workspaceprovisionrepository.WorkspaceAdministrationRepository
	cursors    WorkspaceAdministrationCursorCodec
}

func NewWorkspaceAdministrationApplicationService(repository workspaceprovisionrepository.WorkspaceAdministrationRepository, cursors WorkspaceAdministrationCursorCodec) *WorkspaceAdministrationApplicationService {
	return &WorkspaceAdministrationApplicationService{repository: repository, cursors: cursors}
}

func (service *WorkspaceAdministrationApplicationService) List(ctx context.Context, principal principalmodel.Principal, query workspaceprovisionmodel.CatalogQuery) (workspaceprovisionmodel.CatalogPage, error) {
	if err := authorizeWorkspaceAdministration(principal, ListWorkspacesActionKey); err != nil {
		return workspaceprovisionmodel.CatalogPage{}, err
	}
	if service == nil || service.repository == nil || service.cursors == nil {
		return workspaceprovisionmodel.CatalogPage{}, workspaceAdministrationError(workspaceprovisionmodel.ErrAdministrationUnavailable)
	}
	pageSize := query.PageSize
	if pageSize == 0 {
		pageSize = workspaceCatalogDefaultPageSize
	}
	if pageSize < 1 || pageSize > workspaceCatalogMaxPageSize {
		return workspaceprovisionmodel.CatalogPage{}, workspaceAdministrationError(workspaceprovisionmodel.ErrInvalid)
	}
	binding := workspaceAdministrationCursorBinding(principal, ListWorkspacesActionKey)
	after := ""
	if strings.TrimSpace(query.Cursor) != "" {
		var err error
		after, err = service.cursors.Open(query.Cursor, binding)
		if err != nil {
			return workspaceprovisionmodel.CatalogPage{}, workspaceAdministrationError(workspaceprovisionmodel.ErrInvalidCursor)
		}
	}
	items, more, err := service.repository.ListWorkspaceCatalog(ctx, after, pageSize)
	if err != nil {
		return workspaceprovisionmodel.CatalogPage{}, workspaceAdministrationError(err)
	}
	page := workspaceprovisionmodel.CatalogPage{Items: items}
	if more {
		if len(items) == 0 {
			return workspaceprovisionmodel.CatalogPage{}, workspaceAdministrationError(workspaceprovisionmodel.ErrAdministrationUnavailable)
		}
		page.NextCursor, err = service.cursors.Seal(items[len(items)-1].CanonicalCode, binding)
		if err != nil {
			return workspaceprovisionmodel.CatalogPage{}, workspaceAdministrationError(workspaceprovisionmodel.ErrAdministrationUnavailable)
		}
	}
	return page, nil
}

func (service *WorkspaceAdministrationApplicationService) Suspend(ctx context.Context, principal principalmodel.Principal, canonicalCode string, expectedRevision int, idempotencyKey string) (workspaceprovisionmodel.LifecycleResult, error) {
	return service.setStatus(ctx, principal, canonicalCode, expectedRevision, idempotencyKey, SuspendWorkspaceActionKey, workspaceprovisionmodel.WorkspaceStatusSuspended)
}

func (service *WorkspaceAdministrationApplicationService) Reactivate(ctx context.Context, principal principalmodel.Principal, canonicalCode string, expectedRevision int, idempotencyKey string) (workspaceprovisionmodel.LifecycleResult, error) {
	return service.setStatus(ctx, principal, canonicalCode, expectedRevision, idempotencyKey, ReactivateWorkspaceActionKey, workspaceprovisionmodel.WorkspaceStatusActive)
}

func (service *WorkspaceAdministrationApplicationService) setStatus(ctx context.Context, principal principalmodel.Principal, canonicalCode string, expectedRevision int, idempotencyKey, actionKey, status string) (workspaceprovisionmodel.LifecycleResult, error) {
	if err := authorizeWorkspaceAdministration(principal, actionKey); err != nil {
		return workspaceprovisionmodel.LifecycleResult{}, err
	}
	if service == nil || service.repository == nil {
		return workspaceprovisionmodel.LifecycleResult{}, workspaceAdministrationError(workspaceprovisionmodel.ErrAdministrationUnavailable)
	}
	canonicalCode, idempotencyKey = workspaceprovisionvalidation.NormalizeCanonicalCode(canonicalCode), strings.TrimSpace(idempotencyKey)
	if workspaceprovisionvalidation.ValidateCanonicalCode(canonicalCode) != nil || expectedRevision < 1 || idempotencyKey == "" {
		return workspaceprovisionmodel.LifecycleResult{}, workspaceAdministrationError(workspaceprovisionmodel.ErrInvalid)
	}
	result, err := service.repository.SetWorkspaceStatus(ctx, workspaceAdministrationActor(principal), canonicalCode, expectedRevision, status, idempotencyKey)
	return result, workspaceAdministrationError(err)
}

func (service *WorkspaceAdministrationApplicationService) UpdateCommercialConfiguration(ctx context.Context, principal principalmodel.Principal, canonicalCode string, request workspaceprovisionmodel.CommercialConfigurationUpdateRequest, idempotencyKey string) (workspaceprovisionmodel.CommercialConfigurationUpdateResult, error) {
	if err := authorizeWorkspaceAdministration(principal, UpdateWorkspaceCommercialConfigurationActionKey); err != nil {
		return workspaceprovisionmodel.CommercialConfigurationUpdateResult{}, err
	}
	if service == nil || service.repository == nil {
		return workspaceprovisionmodel.CommercialConfigurationUpdateResult{}, workspaceAdministrationError(workspaceprovisionmodel.ErrAdministrationUnavailable)
	}
	canonicalCode, idempotencyKey = workspaceprovisionvalidation.NormalizeCanonicalCode(canonicalCode), strings.TrimSpace(idempotencyKey)
	request.Configuration = workspaceprovisionvalidation.NormalizeCommercialConfiguration(request.Configuration)
	if workspaceprovisionvalidation.ValidateCanonicalCode(canonicalCode) != nil || request.ExpectedRevision < 1 || idempotencyKey == "" || workspaceprovisionvalidation.ValidateCommercialConfiguration(request.Configuration) != nil {
		return workspaceprovisionmodel.CommercialConfigurationUpdateResult{}, workspaceAdministrationError(workspaceprovisionmodel.ErrInvalid)
	}
	result, err := service.repository.UpdateWorkspaceCommercialConfiguration(ctx, workspaceAdministrationActor(principal), canonicalCode, request, idempotencyKey)
	return result, workspaceAdministrationError(err)
}

func authorizeWorkspaceAdministration(principal principalmodel.Principal, actionKey string) error {
	if !principal.Known || strings.TrimSpace(principal.RoleKey) != WorkspaceAdministratorRoleKey ||
		strings.TrimSpace(principal.WorkspaceID) == "" || strings.TrimSpace(principal.WorkspaceID) != strings.TrimSpace(principalmodel.InstallationWorkspaceID) ||
		!principal.HasExactPermission(actionKey) {
		return &apperror.AppError{Kind: apperror.KindForbidden, Code: "auth.permission_denied", Err: workspaceprovisionmodel.ErrAdministrationForbidden}
	}
	return nil
}

func workspaceAdministrationActor(principal principalmodel.Principal) workspaceprovisionmodel.AdministrationActor {
	return workspaceprovisionmodel.AdministrationActor{
		WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, RoleKey: principal.RoleKey,
		RequestID: principal.RequestID, AuthorizationRevision: principal.AuthorizationRevision,
	}
}

func workspaceAdministrationCursorBinding(principal principalmodel.Principal, actionKey string) WorkspaceAdministrationCursorBinding {
	return WorkspaceAdministrationCursorBinding{ActionKey: actionKey, SubjectID: principal.UserID, AuthorizationRevision: principal.AuthorizationRevision}
}

func workspaceAdministrationError(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, workspaceprovisionmodel.ErrInvalid), errors.Is(err, workspaceprovisionmodel.ErrInvalidCursor):
		return &apperror.AppError{Kind: apperror.KindBadRequest, Code: "workspace.administration_request_invalid", Err: err}
	case errors.Is(err, workspaceprovisionmodel.ErrWorkspaceNotFound):
		return &apperror.AppError{Kind: apperror.KindNotFound, Code: "workspace.not_found", Err: err}
	case errors.Is(err, workspaceprovisionmodel.ErrIdempotencyConflict), errors.Is(err, workspaceprovisionmodel.ErrRevisionConflict), errors.Is(err, workspaceprovisionmodel.ErrInitialWorkspaceSuspension):
		return &apperror.AppError{Kind: apperror.KindConflict, Code: workspaceAdministrationConflictCode(err), Err: err}
	case errors.Is(err, workspaceprovisionmodel.ErrAdministrationUnavailable):
		return &apperror.AppError{Kind: apperror.KindUnavailable, Code: "workspace.administration_unavailable", Err: err}
	default:
		return err
	}
}

func workspaceAdministrationConflictCode(err error) string {
	switch {
	case errors.Is(err, workspaceprovisionmodel.ErrIdempotencyConflict):
		return "workspace.administration_idempotency_conflict"
	case errors.Is(err, workspaceprovisionmodel.ErrInitialWorkspaceSuspension):
		return "workspace.initial_workspace_suspension_forbidden"
	default:
		return "workspace.revision_conflict"
	}
}
