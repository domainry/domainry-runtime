package service

import (
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"

	"context"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

type RecordUniquenessValidator struct {
	repository recordrepository.RecordRepository
}

func NewRecordUniquenessValidator(repository recordrepository.RecordRepository) *RecordUniquenessValidator {
	return &RecordUniquenessValidator{repository: repository}
}

func (s *RecordUniquenessValidator) ValidateDuplicateIdentity(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, currentID string, data map[string]any) error {
	// Retain the legacy validation port for composed consumers. All declared
	// uniqueness is enforced by ValidateUnique; field names must not add hidden
	// constraints (or exemptions for particular objects) before that validation.
	return nil
}

func (s *RecordUniquenessValidator) ValidateUnique(ctx context.Context, workspaceID, objectKey string, object definitionmodel.ObjectSchema, currentID string, data map[string]any) error {
	for _, field := range object.Fields {
		if !field.Unique || recordvalidation.RecordIsEmptyValue(data[field.Key]) {
			continue
		}
		exists, err := s.repository.UniqueExists(ctx, workspaceID, objectKey, field.Key, currentID, data[field.Key])
		if err != nil {
			return recordInternalError("check unique field", err)
		}
		if exists {
			return recordServiceError(apperror.KindBadRequest, "backend.unique.field", nil, "field", field.Key)
		}
	}
	for _, validation := range object.Validations {
		validationType := strings.TrimSpace(validation.Type)
		if validationType != "composite_unique" {
			continue
		}
		if len(validation.Fields) == 0 {
			continue
		}
		filters := map[string]any{}
		complete := true
		for _, fieldKey := range validation.Fields {
			fieldKey = strings.TrimSpace(fieldKey)
			if fieldKey == "" || recordvalidation.RecordIsEmptyValue(data[fieldKey]) {
				complete = false
				break
			}
			filters[fieldKey] = data[fieldKey]
		}
		if !complete {
			continue
		}
		page, err := s.repository.ListRecords(ctx, workspaceID, object, recordmodel.RecordListQuery{Page: 1, PageSize: 10, AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted, Filters: filters})
		if err != nil {
			return recordInternalError("check unique combination", err)
		}
		for _, existing := range page.Items {
			if existing.ID != currentID {
				return recordServiceError(apperror.KindBadRequest, "backend.unique.combination", nil, "validation", validation.Key)
			}
		}
	}
	conditionalPolicies, err := recordvalidation.RecordConditionalUniquePolicies(object)
	if err != nil {
		return recordInternalError("parse conditional unique", err)
	}
	for _, policy := range conditionalPolicies {
		if !recordvalidation.RecordConditionalUniqueApplies(policy, data) {
			continue
		}
		baseFilters := map[string]any{}
		complete := true
		for _, fieldKey := range policy.Fields {
			if recordvalidation.RecordIsEmptyValue(data[fieldKey]) {
				complete = false
				break
			}
			baseFilters[fieldKey] = data[fieldKey]
		}
		if !complete {
			continue
		}
		for _, conditionValue := range policy.ConditionValues {
			filters := make(map[string]any, len(baseFilters)+1)
			for key, value := range baseFilters {
				filters[key] = value
			}
			filters[policy.ConditionField] = conditionValue
			page, listErr := s.repository.ListRecords(ctx, workspaceID, object, recordmodel.RecordListQuery{Page: 1, PageSize: 10, AuthorizationMode: recordmodel.RecordQueryAuthorizationUnrestricted, Filters: filters})
			if listErr != nil {
				return recordInternalError("check conditional unique combination", listErr)
			}
			for _, existing := range page.Items {
				if existing.ID != currentID {
					code := policy.Message
					if code == "" {
						code = "backend.unique.conditional"
					}
					return recordServiceError(apperror.KindBadRequest, code, nil, "validation", policy.Key)
				}
			}
		}
	}
	return nil
}
