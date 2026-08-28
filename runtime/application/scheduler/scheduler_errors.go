package scheduler

import (
	"errors"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func workflowWorkerPrincipal() principalmodel.Principal {
	principal := principalmodel.NewSystemPrincipal("scheduler:worker", principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "scheduler worker dispatch"), "*")
	principal.WorkspaceID = principalmodel.InstallationWorkspaceID
	return principal
}

func schedulerWorkspaceID(principal principalmodel.Principal) string {
	if workspaceID, err := principalmodel.NewWorkspaceID(principal.WorkspaceID); err == nil {
		return workspaceID.String()
	}
	if principal.SystemScope.Valid() {
		return principalmodel.InstallationWorkspaceID
	}
	return ""
}

func badRequest(code string, params ...string) error {
	return schedulerError(apperror.KindBadRequest, code, nil, params...)
}
func forbidden(code string, params ...string) error {
	return schedulerError(apperror.KindForbidden, code, nil, params...)
}
func notFound(code string, params ...string) error {
	return schedulerError(apperror.KindNotFound, code, nil, params...)
}
func conflict(code string, params ...string) error {
	return schedulerError(apperror.KindConflict, code, nil, params...)
}
func internalError(operation string, err error) error {
	return schedulerError(apperror.KindInternal, "backend.internal", err, "operation", operation)
}

func schedulerError(kind apperror.ErrorKind, code string, err error, params ...string) error {
	values := map[string]string{}
	for index := 0; index+1 < len(params); index += 2 {
		if key := strings.TrimSpace(params[index]); key != "" {
			values[key] = params[index+1]
		}
	}
	if len(values) == 0 {
		values = nil
	}
	return &apperror.AppError{Kind: kind, Code: code, Params: values, Err: err}
}

func serviceErrorCode(err error) string {
	if err == nil {
		return ""
	}
	var appErr *apperror.AppError
	if errors.As(err, &appErr) && strings.TrimSpace(appErr.Code) != "" {
		return appErr.Code
	}
	return "backend.internal"
}

func valueOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
