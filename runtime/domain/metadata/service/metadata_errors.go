package service

import (
	"errors"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
)

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
	return &apperror.AppError{
		Kind:   apperror.KindInternal,
		Code:   "backend.internal",
		Params: map[string]string{"operation": operation},
	}
}

func wrapMetadataError(err error) error {
	if err == nil {
		return nil
	}
	var appErr *apperror.AppError
	if errors.As(err, &appErr) {
		return err
	}
	var versionConflict *metadatamodel.MetadataDefinitionConflictError
	if errors.As(err, &versionConflict) {
		return conflict(
			"backend.metadata.definition_version_conflict",
			"resource_type", versionConflict.ResourceType,
			"resource_key", versionConflict.ResourceKey,
			"expected_hash", versionConflict.ExpectedHash,
			"current_hash", versionConflict.CurrentHash,
		)
	}
	return &apperror.AppError{
		Kind:   apperror.KindInternal,
		Code:   "backend.internal",
		Params: map[string]string{"operation": "metadata repository operation"},
		Err:    err,
	}
}

func valueOrDefault(value, fallback string) string {
	if value = strings.TrimSpace(value); value != "" {
		return value
	}
	return fallback
}
