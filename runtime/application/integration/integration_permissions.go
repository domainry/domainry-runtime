package integration

import (
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

const (
	PermissionCatalogView      = "integration.catalog.view"
	PermissionConnectionManage = "integration.connection.manage"
	PermissionSecretManage     = "integration.secret.manage"
	PermissionConnectionTest   = "integration.connection.test"
	PermissionInvoke           = "integration.invoke"
	PermissionRetry            = "integration.retry"
	PermissionAuditView        = "integration.audit.view"
)

func integrationAuthorizeQuery(principal principalmodel.Principal) error {
	if _, err := principalmodel.NewWorkspaceQueryScope(principal.WorkspaceID); !principal.Known || err != nil {
		return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required", Err: err}
	}
	return nil
}

func integrationAuthorizeCommand(principal principalmodel.Principal) error {
	if _, err := principalmodel.NewWorkspaceCommandScope(principal.WorkspaceID); !principal.Known || err != nil {
		return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required", Err: err}
	}
	return nil
}

func integrationAuthorizeWorkspaceQuery(workspaceID string) error {
	if _, err := principalmodel.NewWorkspaceQueryScope(workspaceID); err != nil {
		return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required", Err: err}
	}
	return nil
}

func integrationAuthorizeWorkspaceCommand(workspaceID string) error {
	if _, err := principalmodel.NewWorkspaceCommandScope(workspaceID); err != nil {
		return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required", Err: err}
	}
	return nil
}

func HasPermission(principal principalmodel.Principal, permission string) bool {
	return principal.Known && principal.HasPermission(permission)
}

func HasAnyPermission(principal principalmodel.Principal, permissions ...string) bool {
	for _, permission := range permissions {
		if HasPermission(principal, permission) {
			return true
		}
	}
	return false
}
