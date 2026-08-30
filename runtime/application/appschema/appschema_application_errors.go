package appschema

import (
	"errors"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func metadataInstallationScope(purpose string) principalmodel.SystemScope {
	return principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, purpose)
}

func metadataAuthorizeQuery(principal principalmodel.Principal) error {
	if _, err := principalmodel.NewWorkspaceQueryScope(principal.WorkspaceID); !principal.Known || err != nil {
		return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required", Err: err}
	}
	return nil
}

func metadataAuthorizeCommand(principal principalmodel.Principal) error {
	if _, err := principalmodel.NewWorkspaceCommandScope(principal.WorkspaceID); !principal.Known || err != nil {
		return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required", Err: err}
	}
	return nil
}

func metadataAuthorizeWorkspaceQuery(workspaceID string) error {
	if _, err := principalmodel.NewWorkspaceQueryScope(workspaceID); err != nil {
		return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required", Err: err}
	}
	return nil
}

func badRequest(code string, params ...string) error {
	return metadataError(apperror.KindBadRequest, code, params...)
}
func forbidden(code string, params ...string) error {
	return metadataError(apperror.KindForbidden, code, params...)
}
func notFound(code string, params ...string) error {
	return metadataError(apperror.KindNotFound, code, params...)
}
func conflict(code string, params ...string) error {
	return metadataError(apperror.KindConflict, code, params...)
}

func metadataError(kind apperror.ErrorKind, code string, params ...string) error {
	values := map[string]string{}
	for index := 0; index+1 < len(params); index += 2 {
		if key := strings.TrimSpace(params[index]); key != "" {
			values[key] = params[index+1]
		}
	}
	if len(values) == 0 {
		values = nil
	}
	return &apperror.AppError{Kind: kind, Code: code, Params: apperror.SanitizeParams(values)}
}

func metadataInternalError(operation string) error {
	return &apperror.AppError{Kind: apperror.KindInternal, Code: "backend.internal", Params: map[string]string{"operation": operation}}
}

func metadataInternalErrorWithCause(operation string, err error) error {
	return &apperror.AppError{Kind: apperror.KindInternal, Code: "backend.internal", Params: map[string]string{"operation": operation}, Err: err}
}

func wrapMetadataError(err error) error {
	if err == nil {
		return nil
	}
	var appErr *apperror.AppError
	if errors.As(err, &appErr) {
		return err
	}
	var schemaMismatch *appschemamodel.ApplicationSchemaPhysicalSchemaMismatchError
	if errors.As(err, &schemaMismatch) {
		return conflict(schemaMismatch.ErrorCode(), "object_key", schemaMismatch.ObjectKey, "column_key", schemaMismatch.ColumnKey, "expected_type", schemaMismatch.ExpectedType, "actual_type", schemaMismatch.ActualType)
	}
	return &apperror.AppError{Kind: apperror.KindInternal, Code: "backend.internal", Params: map[string]string{"operation": "metadata repository operation"}, Err: err}
}

func valueOrDefault(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}
