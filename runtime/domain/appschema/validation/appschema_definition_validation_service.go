package validation

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type ApplicationDefinitionPayloadValidator func(ctx context.Context, resourceType, resourceKey string, payload json.RawMessage) (json.RawMessage, []appschemamodel.ApplicationDefinitionValidationIssue, error)

// ApplicationSchemaValidateDefinitionRequest owns the common metadata authoring contract. Domain
// packages remain responsible for validating and normalizing their payloads via
// the injected validator.
func ApplicationSchemaValidateDefinitionRequest(
	ctx context.Context,
	resourceType string,
	resourceKey string,
	payload json.RawMessage,
	principal principalmodel.Principal,
	validatePayload ApplicationDefinitionPayloadValidator,
) (appschemamodel.ApplicationDefinitionValidationResult, error) {
	if !principal.Known || !principal.HasPermission("workspace.admin") {
		return appschemamodel.ApplicationDefinitionValidationResult{}, forbidden("auth.permission_denied")
	}

	resourceType = strings.TrimSpace(resourceType)
	resourceKey = strings.TrimSpace(resourceKey)
	result := appschemamodel.ApplicationDefinitionValidationResult{
		ResourceType: resourceType,
		ResourceKey:  resourceKey,
		Errors:       []appschemamodel.ApplicationDefinitionValidationIssue{},
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
	for _, candidate := range ApplicationSchemaBusinessResourceTypes() {
		if candidate == resourceType {
			return true
		}
	}
	return false
}

func definitionValidationFailure(result appschemamodel.ApplicationDefinitionValidationResult, err error) appschemamodel.ApplicationDefinitionValidationResult {
	result.Valid = false
	code, params := definitionValidationError(err)
	fieldPath := ApplicationDefinitionValidationErrorFieldPath(code, params)
	if fieldPath == "" {
		fieldPath = "definition"
	}
	result.Errors = []appschemamodel.ApplicationDefinitionValidationIssue{NewApplicationDefinitionValidationIssue(code, fieldPath, "", "", params)}
	return result
}

func definitionValidationError(err error) (string, map[string]string) {
	var appErr *apperror.AppError
	if errors.As(err, &appErr) {
		return appErr.ErrorCode(), appErr.ErrorParams()
	}
	return "backend.internal", nil
}
