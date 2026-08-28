package validation

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type MetadataDefinitionPayloadValidator func(ctx context.Context, resourceType, resourceKey string, payload json.RawMessage) (json.RawMessage, []metadatamodel.MetadataDefinitionValidationIssue, error)

// MetadataValidateDefinitionRequest owns the common metadata authoring contract. Domain
// packages remain responsible for validating and normalizing their payloads via
// the injected validator.
func MetadataValidateDefinitionRequest(
	ctx context.Context,
	resourceType string,
	resourceKey string,
	payload json.RawMessage,
	principal principalmodel.Principal,
	validatePayload MetadataDefinitionPayloadValidator,
) (metadatamodel.MetadataDefinitionValidationResult, error) {
	if !principal.Known || !principal.HasPermission("workspace.admin") {
		return metadatamodel.MetadataDefinitionValidationResult{}, forbidden("auth.permission_denied")
	}

	resourceType = strings.TrimSpace(resourceType)
	resourceKey = strings.TrimSpace(resourceKey)
	result := metadatamodel.MetadataDefinitionValidationResult{
		ResourceType: resourceType,
		ResourceKey:  resourceKey,
		Errors:       []metadatamodel.MetadataDefinitionValidationIssue{},
	}
	if resourceType == "workflow" {
		return definitionValidationFailure(result, badRequest("backend.workflow.lifecycle_api_required")), nil
	}
	if !businessResourceTypeExists(resourceType) {
		return definitionValidationFailure(result, badRequest("backend.metadata.resource_type_invalid", "resource_type", resourceType)), nil
	}
	if resourceKey == "" || len(payload) == 0 {
		return definitionValidationFailure(result, badRequest("backend.metadata.definition_identity_required", "resource_type", resourceType)), nil
	}
	if validatePayload == nil {
		return definitionValidationFailure(result, badRequest("backend.metadata.definition_validator_required")), nil
	}

	normalized, issues, err := validatePayload(ctx, resourceType, resourceKey, payload)
	if err != nil {
		return definitionValidationFailure(result, err), nil
	}
	if len(issues) > 0 {
		result.Errors = issues
		return result, nil
	}
	result.Valid = true
	result.NormalizedPayload = append(json.RawMessage(nil), normalized...)
	return result, nil
}

func businessResourceTypeExists(resourceType string) bool {
	for _, candidate := range MetadataBusinessResourceTypes() {
		if candidate == resourceType {
			return true
		}
	}
	return false
}

func definitionValidationFailure(result metadatamodel.MetadataDefinitionValidationResult, err error) metadatamodel.MetadataDefinitionValidationResult {
	result.Valid = false
	code, params := definitionValidationError(err)
	fieldPath := MetadataDefinitionValidationErrorFieldPath(code, params)
	if fieldPath == "" {
		fieldPath = "definition"
	}
	result.Errors = []metadatamodel.MetadataDefinitionValidationIssue{NewMetadataDefinitionValidationIssue(code, fieldPath, "", "", params)}
	return result
}

func definitionValidationError(err error) (string, map[string]string) {
	var appErr *apperror.AppError
	if errors.As(err, &appErr) {
		return appErr.ErrorCode(), appErr.ErrorParams()
	}
	return "backend.internal", nil
}
