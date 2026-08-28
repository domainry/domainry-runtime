package agent

import (
	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func badRequest(code string) error {
	return &apperror.AppError{Kind: apperror.KindBadRequest, Code: code}
}

func notFound(code string) error {
	return &apperror.AppError{Kind: apperror.KindNotFound, Code: code}
}

func agentQueryWorkspace(principal principalmodel.Principal) (string, error) {
	if !principal.Known {
		return "", &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required"}
	}
	scope, err := principalmodel.NewWorkspaceQueryScope(principal.WorkspaceID)
	if err != nil {
		return "", &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required", Err: err}
	}
	return scope.WorkspaceID().String(), nil
}

func agentCommandWorkspace(principal principalmodel.Principal) (string, error) {
	if !principal.Known {
		return "", &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required"}
	}
	scope, err := principalmodel.NewWorkspaceCommandScope(principal.WorkspaceID)
	if err != nil {
		return "", &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required", Err: err}
	}
	return scope.WorkspaceID().String(), nil
}
