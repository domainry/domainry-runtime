package service

import (
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"context"
	"fmt"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

type RecordRelatedPolicyDependencies struct {
	Repository recordrepository.RecordRepository
	Object     func(context.Context, string) (definitionmodel.ObjectSchema, bool)
	CanAccess  func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool
}

type RecordRelatedPolicyValidator struct {
	dependencies RecordRelatedPolicyDependencies
}

func NewRecordRelatedPolicyValidator(dependencies RecordRelatedPolicyDependencies) *RecordRelatedPolicyValidator {
	return &RecordRelatedPolicyValidator{dependencies: dependencies}
}

func (v *RecordRelatedPolicyValidator) ValidateRecordPolicies(ctx context.Context, object definitionmodel.ObjectSchema, data map[string]any, operation string, principal principalmodel.Principal) error {
	for _, validation := range object.Validations {
		validationType := strings.TrimSpace(validation.Type)
		if validationType != "related_record_status" && validationType != "related_status_guard" && validationType != "related_boolean_false" && validationType != "related_field_boolean_false" {
			continue
		}
		if !recordvalidation.RecordValidationBlocks(validation) || !recordvalidation.RecordPolicyAppliesToOperation(validation, operation) {
			continue
		}
		fieldKey := strings.TrimSpace(validation.FieldKey)
		if fieldKey == "" {
			fieldKey = recordvalidation.RecordConfigString(validation.Config["field"])
		}
		if fieldKey == "" {
			fieldKey = recordvalidation.RecordConfigString(validation.Config["relation_field"])
		}
		if fieldKey == "" && len(validation.Fields) > 0 {
			fieldKey = strings.TrimSpace(validation.Fields[0])
		}
		targetKey, _, related, found, err := v.relatedRecordForValidation(ctx, object, data, validation, fieldKey, principal)
		if err != nil {
			return err
		}
		if !found {
			continue
		}
		if validationType == "related_boolean_false" || validationType == "related_field_boolean_false" {
			relatedField := recordvalidation.RecordConfigString(validation.Config["related_field"])
			if relatedField == "" {
				relatedField = recordvalidation.RecordConfigString(validation.Config["field_key"])
			}
			if relatedField == "" {
				return recordStateMachineError(apperror.KindBadRequest, "backend.policy.related_field_required", "policy", validation.Key)
			}
			value, ok := recordvalidation.RecordBoolValue(related.Data[relatedField])
			if ok && value {
				return recordStateMachineError(apperror.KindBadRequest, recordvalidation.RecordMessageCode(validation.Message, "backend.policy.related_boolean_false"), "object", targetKey, "field", relatedField)
			}
			continue
		}
		statusField := relatedValueOrDefault(recordvalidation.RecordConfigString(validation.Config["status_field"]), "status")
		status := strings.TrimSpace(fmt.Sprint(related.Data[statusField]))
		allowedStatuses := recordStringListFromAny(validation.Config["allowed_statuses"])
		if len(allowedStatuses) == 0 {
			allowedStatuses = recordStringListFromAny(validation.Config["must_statuses"])
		}
		if len(allowedStatuses) > 0 && !recordvalidation.RecordContainsText(allowedStatuses, status) {
			return recordStateMachineError(apperror.KindBadRequest, recordvalidation.RecordMessageCode(validation.Message, "backend.policy.related_status_not_allowed"), "object", targetKey, "status", status)
		}
		if recordvalidation.RecordContainsText(recordStringListFromAny(validation.Config["disallowed_statuses"]), status) || recordvalidation.RecordContainsText(recordStringListFromAny(validation.Config["must_not_statuses"]), status) {
			return recordStateMachineError(apperror.KindBadRequest, recordvalidation.RecordMessageCode(validation.Message, "backend.policy.related_status_not_allowed"), "object", targetKey, "status", status)
		}
	}
	return nil
}

func (v *RecordRelatedPolicyValidator) ValidateNumericLimitPolicies(ctx context.Context, object definitionmodel.ObjectSchema, data map[string]any, operation string, principal principalmodel.Principal) error {
	for _, validation := range object.Validations {
		validationType := strings.TrimSpace(validation.Type)
		if validationType != "related_numeric_limit" && validationType != "related_numeric_guard" && validationType != "related_field_numeric_limit" {
			continue
		}
		if !recordvalidation.RecordValidationBlocks(validation) || !recordvalidation.RecordPolicyAppliesToOperation(validation, operation) || !recordvalidation.RecordPolicyConditionMatches(validation, data) {
			continue
		}
		valueField := recordvalidation.RecordRelatedNumericValueField(validation)
		if valueField == "" {
			return recordStateMachineError(apperror.KindBadRequest, "backend.policy.related_field_required", "policy", validation.Key)
		}
		value, ok := recordvalidation.RecordNumericValue(data[valueField])
		if !ok {
			return recordStateMachineError(apperror.KindBadRequest, "backend.policy.related_numeric_value_required", "policy", validation.Key, "field", valueField)
		}
		if !recordvalidation.RecordRelatedNumericLimitValueInScope(validation, value) {
			continue
		}
		if relationField := recordvalidation.RecordConfigString(validation.Config["relation_field"]); relationField != "" {
			targetObject, targetRecord, err := v.relatedRecord(ctx, object, data, relationField, principal)
			if err != nil {
				return err
			}
			if err := validateRelatedNumericTarget(validation, valueField, value, targetObject, targetRecord); err != nil {
				return err
			}
			continue
		}
		sourceData := data
		if sourceRelationField := recordvalidation.RecordConfigString(validation.Config["source_relation_field"]); sourceRelationField != "" {
			_, sourceRecord, err := v.relatedRecord(ctx, object, data, sourceRelationField, principal)
			if err != nil {
				return err
			}
			sourceData = sourceRecord.Data
		}
		matchSourceField := recordvalidation.RecordConfigString(validation.Config["source_field"])
		if matchSourceField == "" {
			matchSourceField = recordvalidation.RecordConfigString(validation.Config["match_source_field"])
		}
		if matchSourceField == "" {
			return recordStateMachineError(apperror.KindBadRequest, "backend.policy.related_field_required", "policy", validation.Key)
		}
		matchValue := sourceData[matchSourceField]
		if recordvalidation.RecordIsEmptyValue(matchValue) {
			return recordStateMachineError(apperror.KindBadRequest, "backend.policy.related_field_required", "policy", validation.Key, "field", matchSourceField)
		}
		targetObject, targetRecord, err := v.targetRecordByMatch(ctx, validation, matchValue, principal)
		if err != nil {
			return err
		}
		if err := validateRelatedNumericTarget(validation, valueField, value, targetObject, targetRecord); err != nil {
			return err
		}
	}
	return nil
}

func (v *RecordRelatedPolicyValidator) ValidateBlockingRecordPolicies(ctx context.Context, object definitionmodel.ObjectSchema, data map[string]any, operation string, principal principalmodel.Principal) error {
	for _, validation := range object.Validations {
		validationType := strings.TrimSpace(validation.Type)
		if validationType != "blocking_related_records" && validationType != "no_related_records" {
			continue
		}
		if !recordvalidation.RecordValidationBlocks(validation) || !recordvalidation.RecordPolicyAppliesToOperation(validation, operation) || !recordvalidation.RecordPolicyConditionMatches(validation, data) {
			continue
		}
		targetKey := recordvalidation.RecordConfigString(validation.Config["target_object"])
		if targetKey == "" {
			targetKey = recordvalidation.RecordConfigString(validation.Config["object_key"])
		}
		if targetKey == "" {
			return recordStateMachineError(apperror.KindBadRequest, "backend.policy.relation_target_unavailable", "policy", validation.Key, "object", "")
		}
		targetObject, ok := v.object(ctx, targetKey)
		if !ok {
			return recordStateMachineError(apperror.KindBadRequest, "backend.policy.relation_target_unavailable", "policy", validation.Key, "object", targetKey)
		}
		found, err := v.blockingRecordExists(ctx, targetObject, validation, data, principal)
		if err != nil {
			return err
		}
		if found {
			return recordStateMachineError(apperror.KindBadRequest, recordvalidation.RecordPolicyMessageCode(validation.Message, "backend.policy.related_status_not_allowed"), "object", targetKey)
		}
	}
	return nil
}

func (v *RecordRelatedPolicyValidator) ValidateDynamicReferencePolicies(ctx context.Context, object definitionmodel.ObjectSchema, data map[string]any, operation string, principal principalmodel.Principal) error {
	for _, validation := range object.Validations {
		validationType := strings.TrimSpace(validation.Type)
		if validationType != "dynamic_reference_exists" && validationType != "dynamic_record_reference" {
			continue
		}
		if !recordvalidation.RecordValidationBlocks(validation) || !recordvalidation.RecordPolicyAppliesToOperation(validation, operation) || !recordvalidation.RecordPolicyConditionMatches(validation, data) {
			continue
		}
		objectField := relatedValueOrDefault(recordvalidation.RecordConfigString(validation.Config["object_field"]), "target_object")
		recordField := relatedValueOrDefault(recordvalidation.RecordConfigString(validation.Config["record_field"]), "target_record_id")
		targetKey := strings.TrimSpace(fmt.Sprint(data[objectField]))
		recordID := strings.TrimSpace(fmt.Sprint(data[recordField]))
		if targetKey == "" || targetKey == "<nil>" || recordID == "" || recordID == "<nil>" {
			continue
		}
		targetObject, ok := v.object(ctx, targetKey)
		if !ok {
			return recordStateMachineError(apperror.KindBadRequest, recordvalidation.RecordPolicyMessageCode(validation.Message, "backend.policy.relation_target_unavailable"), "policy", validation.Key, "object", targetKey)
		}
		record, found, err := v.dependencies.Repository.GetRecord(ctx, principal.WorkspaceID, targetObject, recordID)
		if err != nil {
			return relatedPolicyInternalError("check dynamic reference", err)
		}
		if !found {
			return recordStateMachineError(apperror.KindBadRequest, recordvalidation.RecordPolicyMessageCode(validation.Message, "backend.policy.related_record_missing"), "policy", validation.Key, "object", targetKey)
		}
		if !v.canAccess(principal, targetObject, record) {
			return recordStateMachineError(apperror.KindForbidden, "backend.record.outside_scope")
		}
	}
	return nil
}

func (v *RecordRelatedPolicyValidator) ValidateTimeOverlapPolicies(ctx context.Context, object definitionmodel.ObjectSchema, data map[string]any, recordID string, operation string, principal principalmodel.Principal) error {
	policies, err := recordvalidation.RecordTemporalExclusionPolicies(object)
	if err != nil {
		return err
	}
	for _, policy := range policies {
		applies, err := recordvalidation.RecordTemporalExclusionCandidate(policy, data, operation)
		if err != nil {
			return err
		}
		if !applies {
			continue
		}
		// RecordTemporalExclusionCandidate parsed the same immutable data and
		// only returns applies=true for a complete, valid range.
		start, end, _, _ := recordvalidation.RecordTimeOverlapRange(data, policy.StartField, policy.EndField)
		filters := map[string]any{}
		for _, field := range policy.ScopeFields {
			filters[field] = data[field]
		}
		for page := 1; ; page++ {
			result, err := v.dependencies.Repository.ListRecords(ctx, principal.WorkspaceID, object, recordmodel.RecordListQuery{Page: page, PageSize: 200, Filters: filters})
			if err != nil {
				return relatedPolicyInternalError("check time overlap policy", err)
			}
			for _, candidate := range result.Items {
				if strings.TrimSpace(candidate.ID) == strings.TrimSpace(recordID) || !v.canAccess(principal, object, candidate) || (policy.StatusField != "" && recordvalidation.RecordContainsText(policy.ExcludedStatuses, strings.TrimSpace(fmt.Sprint(candidate.Data[policy.StatusField])))) || !recordvalidation.RecordTimeOverlapScopeMatches(policy.Definition, data, candidate.Data) {
					continue
				}
				candidateStart, candidateEnd, candidateOK, err := recordvalidation.RecordTimeOverlapRange(candidate.Data, policy.StartField, policy.EndField)
				if err != nil {
					return err
				}
				if candidateOK && start.Before(candidateEnd) && candidateStart.Before(end) {
					return recordStateMachineError(apperror.KindConflict, policy.ErrorCode, "policy", policy.Key, "conflicting_record_id", candidate.ID)
				}
			}
			if !result.HasNext {
				break
			}
		}
	}
	return nil
}

func (v *RecordRelatedPolicyValidator) blockingRecordExists(ctx context.Context, targetObject definitionmodel.ObjectSchema, validation definitionmodel.ValidationSchema, data map[string]any, principal principalmodel.Principal) (bool, error) {
	equalityFilters := map[string]any{}
	for _, filter := range recordvalidation.RecordMapSlice(validation.Config["filters"]) {
		fieldKey := recordvalidation.RecordConfigString(filter["field"])
		if fieldKey == "" {
			continue
		}
		if value, ok := recordvalidation.RecordPolicyFilterExpectedValue(filter, data); ok {
			equalityFilters[fieldKey] = value
		}
	}
	for page := 1; ; page++ {
		result, err := v.dependencies.Repository.ListRecords(ctx, principal.WorkspaceID, targetObject, recordmodel.RecordListQuery{Page: page, PageSize: 200, Filters: equalityFilters})
		if err != nil {
			return false, relatedPolicyInternalError("check blocking related records", err)
		}
		for _, record := range result.Items {
			if v.canAccess(principal, targetObject, record) && recordvalidation.RecordMatchesPolicyFilters(record, validation, data) {
				return true, nil
			}
		}
		if !result.HasNext {
			return false, nil
		}
	}
}

func validateRelatedNumericTarget(validation definitionmodel.ValidationSchema, valueField string, value float64, targetObject definitionmodel.ObjectSchema, targetRecord recordmodel.Record) error {
	targetField := recordvalidation.RecordRelatedNumericTargetField(validation)
	if targetField == "" {
		return recordStateMachineError(apperror.KindBadRequest, "backend.policy.related_field_required", "policy", validation.Key)
	}
	targetValue, ok := recordvalidation.RecordNumericValue(targetRecord.Data[targetField])
	if !ok {
		return recordStateMachineError(apperror.KindBadRequest, "backend.policy.related_numeric_value_required", "policy", validation.Key, "field", targetField)
	}
	operator := strings.ToLower(relatedValueOrDefault(recordvalidation.RecordConfigString(validation.Config["operator"]), "gte"))
	if !recordvalidation.RecordRelatedNumericLimitSatisfied(targetValue, value, operator) {
		return recordStateMachineError(apperror.KindBadRequest, recordvalidation.RecordMessageCode(validation.Message, "backend.policy.related_numeric_limit"), "object", targetObject.Key, "field", targetField, "value_field", valueField)
	}
	return nil
}

func (v *RecordRelatedPolicyValidator) relatedRecordForValidation(ctx context.Context, object definitionmodel.ObjectSchema, data map[string]any, validation definitionmodel.ValidationSchema, fieldKey string, principal principalmodel.Principal) (string, definitionmodel.ObjectSchema, recordmodel.Record, bool, error) {
	targetKey := recordvalidation.RecordConfigString(validation.Config["target_object"])
	sourceField := recordvalidation.RecordConfigString(validation.Config["source_field"])
	targetMatchField := recordvalidation.RecordConfigString(validation.Config["target_match_field"])
	if targetKey != "" && sourceField != "" && targetMatchField != "" {
		return v.lookupRelatedRecord(ctx, validation, data, principal, targetKey, sourceField, targetMatchField)
	}
	if fieldKey == "" || recordvalidation.RecordIsEmptyValue(data[fieldKey]) {
		return "", definitionmodel.ObjectSchema{}, recordmodel.Record{}, false, nil
	}
	field, ok := recordvalidation.RecordRelationField(object, fieldKey)
	if !ok {
		return "", definitionmodel.ObjectSchema{}, recordmodel.Record{}, false, recordStateMachineError(apperror.KindBadRequest, "backend.policy.relation_field_missing", "policy", validation.Key, "object", object.Key, "field", fieldKey)
	}
	targetKey = recordvalidation.RecordRelationTarget(field)
	targetObject, ok := v.object(ctx, targetKey)
	if !ok {
		return "", definitionmodel.ObjectSchema{}, recordmodel.Record{}, false, recordStateMachineError(apperror.KindBadRequest, "backend.policy.relation_target_unavailable", "policy", validation.Key, "object", targetKey)
	}
	recordID := strings.TrimSpace(fmt.Sprint(data[fieldKey]))
	related, found, err := v.dependencies.Repository.GetRecord(ctx, principal.WorkspaceID, targetObject, recordID)
	if err != nil {
		return "", definitionmodel.ObjectSchema{}, recordmodel.Record{}, false, relatedPolicyInternalError("check related record policy", err)
	}
	if !found {
		return "", definitionmodel.ObjectSchema{}, recordmodel.Record{}, false, recordStateMachineError(apperror.KindBadRequest, "backend.policy.related_record_missing", "policy", validation.Key, "object", targetKey)
	}
	if !v.canAccess(principal, targetObject, related) {
		return "", definitionmodel.ObjectSchema{}, recordmodel.Record{}, false, recordStateMachineError(apperror.KindForbidden, "backend.record.outside_scope")
	}
	return targetKey, targetObject, related, true, nil
}

func (v *RecordRelatedPolicyValidator) lookupRelatedRecord(ctx context.Context, validation definitionmodel.ValidationSchema, data map[string]any, principal principalmodel.Principal, targetKey, sourceField, targetMatchField string) (string, definitionmodel.ObjectSchema, recordmodel.Record, bool, error) {
	targetObject, ok := v.object(ctx, targetKey)
	if !ok {
		return "", definitionmodel.ObjectSchema{}, recordmodel.Record{}, false, recordStateMachineError(apperror.KindBadRequest, "backend.policy.relation_target_unavailable", "policy", validation.Key, "object", targetKey)
	}
	sourceValue := data[sourceField]
	if recordvalidation.RecordIsEmptyValue(sourceValue) {
		return targetKey, targetObject, recordmodel.Record{}, false, nil
	}
	page, err := v.dependencies.Repository.ListRecords(ctx, principal.WorkspaceID, targetObject, recordmodel.RecordListQuery{Page: 1, PageSize: 200, Filters: map[string]any{targetMatchField: sourceValue}})
	if err != nil {
		return "", definitionmodel.ObjectSchema{}, recordmodel.Record{}, false, relatedPolicyInternalError("lookup related record policy", err)
	}
	systemLookup := false
	if value, ok := recordvalidation.RecordBoolValue(validation.Config["system_lookup"]); ok {
		systemLookup = value
	}
	for _, record := range page.Items {
		if !systemLookup && !v.canAccess(principal, targetObject, record) {
			continue
		}
		return targetKey, targetObject, record, true, nil
	}
	if recordvalidation.RecordPolicyAllowsMissingRelatedLookup(validation) {
		return targetKey, targetObject, recordmodel.Record{}, false, nil
	}
	return "", definitionmodel.ObjectSchema{}, recordmodel.Record{}, false, recordStateMachineError(apperror.KindBadRequest, "backend.policy.related_record_missing", "policy", validation.Key, "object", targetKey)
}

func (v *RecordRelatedPolicyValidator) relatedRecord(ctx context.Context, object definitionmodel.ObjectSchema, data map[string]any, relationFieldKey string, principal principalmodel.Principal) (definitionmodel.ObjectSchema, recordmodel.Record, error) {
	field, ok := recordvalidation.RecordRelationField(object, relationFieldKey)
	if !ok {
		return definitionmodel.ObjectSchema{}, recordmodel.Record{}, recordStateMachineError(apperror.KindBadRequest, "backend.policy.relation_field_missing", "object", object.Key, "field", relationFieldKey)
	}
	targetKey := recordvalidation.RecordRelationTarget(field)
	targetObject, ok := v.object(ctx, targetKey)
	if !ok {
		return definitionmodel.ObjectSchema{}, recordmodel.Record{}, recordStateMachineError(apperror.KindBadRequest, "backend.policy.relation_target_unavailable", "object", targetKey)
	}
	recordID := strings.TrimSpace(fmt.Sprint(data[relationFieldKey]))
	if recordID == "" || recordID == "<nil>" {
		return definitionmodel.ObjectSchema{}, recordmodel.Record{}, recordStateMachineError(apperror.KindBadRequest, "backend.policy.related_record_missing", "object", targetKey)
	}
	record, found, err := v.dependencies.Repository.GetRecord(ctx, principal.WorkspaceID, targetObject, recordID)
	if err != nil {
		return definitionmodel.ObjectSchema{}, recordmodel.Record{}, relatedPolicyInternalError("check related numeric policy", err)
	}
	if !found {
		return definitionmodel.ObjectSchema{}, recordmodel.Record{}, recordStateMachineError(apperror.KindBadRequest, "backend.policy.related_record_missing", "object", targetKey)
	}
	if !v.canAccess(principal, targetObject, record) {
		return definitionmodel.ObjectSchema{}, recordmodel.Record{}, recordStateMachineError(apperror.KindForbidden, "backend.record.outside_scope")
	}
	return targetObject, record, nil
}

func (v *RecordRelatedPolicyValidator) targetRecordByMatch(ctx context.Context, validation definitionmodel.ValidationSchema, matchValue any, principal principalmodel.Principal) (definitionmodel.ObjectSchema, recordmodel.Record, error) {
	targetKey := recordvalidation.RecordConfigString(validation.Config["target_object"])
	if targetKey == "" {
		targetKey = recordvalidation.RecordConfigString(validation.Config["object_key"])
	}
	if targetKey == "" {
		return definitionmodel.ObjectSchema{}, recordmodel.Record{}, recordStateMachineError(apperror.KindBadRequest, "backend.policy.relation_target_unavailable", "policy", validation.Key, "object", "")
	}
	targetObject, ok := v.object(ctx, targetKey)
	if !ok {
		return definitionmodel.ObjectSchema{}, recordmodel.Record{}, recordStateMachineError(apperror.KindBadRequest, "backend.policy.relation_target_unavailable", "policy", validation.Key, "object", targetKey)
	}
	matchField := recordvalidation.RecordConfigString(validation.Config["target_match_field"])
	if matchField == "" {
		matchField = recordvalidation.RecordConfigString(validation.Config["match_field"])
	}
	if matchField == "" {
		return definitionmodel.ObjectSchema{}, recordmodel.Record{}, recordStateMachineError(apperror.KindBadRequest, "backend.policy.related_field_required", "policy", validation.Key)
	}
	page, err := v.dependencies.Repository.ListRecords(ctx, principal.WorkspaceID, targetObject, recordmodel.RecordListQuery{Page: 1, PageSize: 200, Filters: map[string]any{matchField: matchValue}})
	if err != nil {
		return definitionmodel.ObjectSchema{}, recordmodel.Record{}, relatedPolicyInternalError("find related numeric target", err)
	}
	for _, record := range page.Items {
		if v.canAccess(principal, targetObject, record) {
			return targetObject, record, nil
		}
	}
	return definitionmodel.ObjectSchema{}, recordmodel.Record{}, recordStateMachineError(apperror.KindBadRequest, "backend.policy.related_record_missing", "policy", validation.Key, "object", targetKey)
}

func (v *RecordRelatedPolicyValidator) object(ctx context.Context, key string) (definitionmodel.ObjectSchema, bool) {
	if v.dependencies.Object == nil {
		return definitionmodel.ObjectSchema{}, false
	}
	return v.dependencies.Object(ctx, key)
}

func (v *RecordRelatedPolicyValidator) canAccess(principal principalmodel.Principal, object definitionmodel.ObjectSchema, record recordmodel.Record) bool {
	return v.dependencies.CanAccess == nil || v.dependencies.CanAccess(principal, object, record)
}

func relatedValueOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return fallback
}

func relatedPolicyInternalError(operation string, err error) error {
	return &apperror.AppError{Kind: apperror.KindInternal, Code: "backend.internal", Params: map[string]string{"operation": operation}, Err: err}
}
