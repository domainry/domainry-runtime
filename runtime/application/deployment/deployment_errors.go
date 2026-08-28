package deployment

import (
	"strings"

	apperror "github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func deploymentAuthorizeQuery(principal principalmodel.Principal) error {
	if _, err := principalmodel.NewWorkspaceQueryScope(principal.WorkspaceID); !principal.Known || err != nil {
		return applicationError(apperror.KindForbidden, "backend.workspace_scope_required", err)
	}
	return nil
}

func deploymentAuthorizeCommand(principal principalmodel.Principal) error {
	if _, err := principalmodel.NewWorkspaceCommandScope(principal.WorkspaceID); !principal.Known || err != nil {
		return applicationError(apperror.KindForbidden, "backend.workspace_scope_required", err)
	}
	return nil
}

func forbidden(code string) error {
	return applicationError(apperror.KindForbidden, code, nil)
}

func internalError(operation string, err error) error {
	return applicationError(apperror.KindInternal, "backend.internal", err, "operation", operation)
}

func applicationError(kind apperror.ErrorKind, code string, err error, params ...string) error {
	values := map[string]string{}
	for index := 0; index+1 < len(params); index += 2 {
		if key := strings.TrimSpace(params[index]); key != "" {
			values[key] = params[index+1]
		}
	}
	if len(values) == 0 {
		values = nil
	}
	return &apperror.AppError{Kind: kind, Code: code, Params: apperror.SanitizeParams(values), Err: err}
}
