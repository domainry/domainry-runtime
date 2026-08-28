package service

import (
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func numericRelationObject(validations ...definitionmodel.ValidationSchema) definitionmodel.ObjectSchema {
	return definitionmodel.ObjectSchema{
		Key: "expense",
		Fields: []definitionmodel.FieldSchema{
			{Key: "budget_id", Type: "relation", Config: map[string]any{"object_key": "budget"}},
			{Key: "source_id", Type: "relation", Config: map[string]any{"object_key": "source"}},
		},
		Validations: validations,
	}
}

func numericLimitValidation(config map[string]any) definitionmodel.ValidationSchema {
	return definitionmodel.ValidationSchema{Key: "budget_limit", Type: "related_numeric_limit", Message: "backend.expense.budget_exceeded", Config: config}
}

func TestValidateNumericLimitPoliciesDirectRelation(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace"}}
	repository := &relatedPolicyRepositoryProbe{found: true, record: recordmodel.Record{ID: "budget-1", Data: map[string]any{"remaining": 100.0}}}
	validator := newRelatedPolicyValidatorForTest(repository, map[string]definitionmodel.ObjectSchema{"budget": relatedPolicyObject("budget")}, nil)

	object := numericRelationObject(numericLimitValidation(map[string]any{"relation_field": "budget_id", "target_field": "remaining"}))
	err := validator.ValidateNumericLimitPolicies(t.Context(), object, map[string]any{"budget_id": "budget-1"}, "create", principal)
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.policy.related_field_required", map[string]string{"policy": "budget_limit"})

	object.Validations[0].Config["value_field"] = "amount"
	err = validator.ValidateNumericLimitPolicies(t.Context(), object, map[string]any{"budget_id": "budget-1", "amount": "not-a-number"}, "create", principal)
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.policy.related_numeric_value_required", map[string]string{"field": "amount"})

	object.Validations[0].Config["value_min"] = 50
	if err := validator.ValidateNumericLimitPolicies(t.Context(), object, map[string]any{"budget_id": "budget-1", "amount": 25}, "create", principal); err != nil {
		t.Fatalf("out-of-scope numeric value rejected: %v", err)
	}
	delete(object.Validations[0].Config, "value_min")

	if err := validator.ValidateNumericLimitPolicies(t.Context(), object, map[string]any{"budget_id": "budget-1", "amount": 80}, "create", principal); err != nil {
		t.Fatalf("within-limit value rejected: %v", err)
	}
	err = validator.ValidateNumericLimitPolicies(t.Context(), object, map[string]any{"budget_id": "budget-1", "amount": 120}, "create", principal)
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.expense.budget_exceeded", map[string]string{"object": "budget", "field": "remaining", "value_field": "amount"})
}

func TestValidateNumericLimitPoliciesMatchLookupAndSourceRelation(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace"}}
	repository := &relatedPolicyRepositoryProbe{
		found:  true,
		record: recordmodel.Record{ID: "source-1", Data: map[string]any{"code": "A"}},
		pages:  map[int]recordmodel.RecordPageResult{1: {Items: []recordmodel.Record{{ID: "budget-A", Data: map[string]any{"remaining": 100.0}}}}},
	}
	validator := newRelatedPolicyValidatorForTest(repository, map[string]definitionmodel.ObjectSchema{
		"budget": relatedPolicyObject("budget"), "source": relatedPolicyObject("source"),
	}, nil)

	base := map[string]any{"value_field": "amount", "target_object": "budget", "target_match_field": "code", "target_field": "remaining"}
	object := numericRelationObject(numericLimitValidation(base))
	err := validator.ValidateNumericLimitPolicies(t.Context(), object, map[string]any{"amount": 10}, "create", principal)
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.policy.related_field_required", map[string]string{"policy": "budget_limit"})

	base["source_field"] = "code"
	err = validator.ValidateNumericLimitPolicies(t.Context(), object, map[string]any{"amount": 10}, "create", principal)
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.policy.related_field_required", map[string]string{"field": "code"})

	if err := validator.ValidateNumericLimitPolicies(t.Context(), object, map[string]any{"amount": 10, "code": "A"}, "create", principal); err != nil {
		t.Fatalf("direct match source rejected: %v", err)
	}

	base["source_relation_field"] = "source_id"
	if err := validator.ValidateNumericLimitPolicies(t.Context(), object, map[string]any{"amount": 10, "source_id": "source-1"}, "create", principal); err != nil {
		t.Fatalf("related match source rejected: %v", err)
	}

	base["source_field"] = ""
	base["match_source_field"] = "code"
	if err := validator.ValidateNumericLimitPolicies(t.Context(), object, map[string]any{"amount": 10, "source_id": "source-1"}, "create", principal); err != nil {
		t.Fatalf("fallback match source rejected: %v", err)
	}
}

func TestValidateRelatedNumericTargetBranches(t *testing.T) {
	validation := numericLimitValidation(map[string]any{})
	targetObject := relatedPolicyObject("budget")
	target := recordmodel.Record{Data: map[string]any{}}

	err := validateRelatedNumericTarget(validation, "amount", 10, targetObject, target)
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.policy.related_field_required", nil)

	validation.Config["target_field"] = "remaining"
	err = validateRelatedNumericTarget(validation, "amount", 10, targetObject, target)
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.policy.related_numeric_value_required", map[string]string{"field": "remaining"})

	target.Data["remaining"] = 5
	err = validateRelatedNumericTarget(validation, "amount", 10, targetObject, target)
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.expense.budget_exceeded", nil)

	validation.Config["operator"] = "lt"
	target.Data["remaining"] = 5
	if err := validateRelatedNumericTarget(validation, "amount", 10, targetObject, target); err != nil {
		t.Fatalf("satisfied target rejected: %v", err)
	}
}

func TestRelatedRecordBranches(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace"}}
	object := numericRelationObject()
	repository := &relatedPolicyRepositoryProbe{}
	allow := true
	objects := map[string]definitionmodel.ObjectSchema{"budget": relatedPolicyObject("budget")}
	validator := newRelatedPolicyValidatorForTest(repository, objects, &allow)

	_, _, err := validator.relatedRecord(t.Context(), object, nil, "missing", principal)
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.policy.relation_field_missing", nil)

	delete(objects, "budget")
	_, _, err = validator.relatedRecord(t.Context(), object, map[string]any{"budget_id": "budget-1"}, "budget_id", principal)
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.policy.relation_target_unavailable", nil)
	objects["budget"] = relatedPolicyObject("budget")

	_, _, err = validator.relatedRecord(t.Context(), object, map[string]any{"budget_id": nil}, "budget_id", principal)
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.policy.related_record_missing", nil)

	fault := errors.New("get relation fault")
	repository.err = fault
	_, _, err = validator.relatedRecord(t.Context(), object, map[string]any{"budget_id": "budget-1"}, "budget_id", principal)
	assertRecordCause(t, err, fault)
	repository.err = nil

	repository.found = false
	_, _, err = validator.relatedRecord(t.Context(), object, map[string]any{"budget_id": "budget-1"}, "budget_id", principal)
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.policy.related_record_missing", nil)

	repository.found, repository.record = true, recordmodel.Record{ID: "budget-1"}
	allow = false
	_, _, err = validator.relatedRecord(t.Context(), object, map[string]any{"budget_id": "budget-1"}, "budget_id", principal)
	assertRecordAppError(t, err, apperror.KindForbidden, "backend.record.outside_scope", nil)

	allow = true
	targetObject, record, err := validator.relatedRecord(t.Context(), object, map[string]any{"budget_id": "budget-1"}, "budget_id", principal)
	if err != nil || targetObject.Key != "budget" || record.ID != "budget-1" {
		t.Fatalf("related record = %#v %#v err=%v", targetObject, record, err)
	}
}

func TestTargetRecordByMatchBranches(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace"}}
	repository := &relatedPolicyRepositoryProbe{pages: map[int]recordmodel.RecordPageResult{1: {Items: []recordmodel.Record{{ID: "budget-A"}}}}}
	allow := true
	objects := map[string]definitionmodel.ObjectSchema{"budget": relatedPolicyObject("budget")}
	validator := newRelatedPolicyValidatorForTest(repository, objects, &allow)
	validation := definitionmodel.ValidationSchema{Key: "match", Config: map[string]any{}}

	_, _, err := validator.targetRecordByMatch(t.Context(), validation, "A", principal)
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.policy.relation_target_unavailable", nil)

	validation.Config["object_key"] = "missing"
	_, _, err = validator.targetRecordByMatch(t.Context(), validation, "A", principal)
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.policy.relation_target_unavailable", nil)

	validation.Config["object_key"] = "budget"
	_, _, err = validator.targetRecordByMatch(t.Context(), validation, "A", principal)
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.policy.related_field_required", nil)

	validation.Config["match_field"] = "code"
	fault := errors.New("match list fault")
	repository.err = fault
	_, _, err = validator.targetRecordByMatch(t.Context(), validation, "A", principal)
	assertRecordCause(t, err, fault)
	repository.err = nil

	allow = false
	_, _, err = validator.targetRecordByMatch(t.Context(), validation, "A", principal)
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.policy.related_record_missing", nil)

	allow = true
	object, record, err := validator.targetRecordByMatch(t.Context(), validation, "A", principal)
	if err != nil || object.Key != "budget" || record.ID != "budget-A" || repository.queries[len(repository.queries)-1].Filters["code"] != "A" {
		t.Fatalf("match result = %#v %#v queries=%#v err=%v", object, record, repository.queries, err)
	}
}
