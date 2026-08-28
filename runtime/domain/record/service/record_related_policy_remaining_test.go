package service

import (
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestRelatedPolicyValidatorsSkipNonBlockingPolicies(t *testing.T) {
	validator := NewRecordRelatedPolicyValidator(RecordRelatedPolicyDependencies{})
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace"}}
	data := map[string]any{"amount": 10, "target_object": "missing", "target_record_id": "record", "starts_at": "bad", "ends_at": "bad"}

	tests := []struct {
		name     string
		validate func(definitionmodel.ObjectSchema) error
		policy   definitionmodel.ValidationSchema
	}{
		{name: "record", policy: definitionmodel.ValidationSchema{Type: "related_record_status", Severity: "warning"}, validate: func(object definitionmodel.ObjectSchema) error {
			return validator.ValidateRecordPolicies(t.Context(), object, data, "update", principal)
		}},
		{name: "numeric", policy: definitionmodel.ValidationSchema{Type: "related_numeric_limit", Severity: "warning"}, validate: func(object definitionmodel.ObjectSchema) error {
			return validator.ValidateNumericLimitPolicies(t.Context(), object, data, "update", principal)
		}},
		{name: "blocking", policy: definitionmodel.ValidationSchema{Type: "blocking_related_records", Severity: "warning"}, validate: func(object definitionmodel.ObjectSchema) error {
			return validator.ValidateBlockingRecordPolicies(t.Context(), object, data, "update", principal)
		}},
		{name: "dynamic", policy: definitionmodel.ValidationSchema{Type: "dynamic_reference_exists", Severity: "warning"}, validate: func(object definitionmodel.ObjectSchema) error {
			return validator.ValidateDynamicReferencePolicies(t.Context(), object, data, "update", principal)
		}},
		{name: "time", policy: temporalExclusionTestPolicy("schedule", "warning"), validate: func(object definitionmodel.ObjectSchema) error {
			return validator.ValidateTimeOverlapPolicies(t.Context(), object, data, "record", "update", principal)
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.validate(definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{test.policy}}); err != nil {
				t.Fatalf("non-blocking policy returned %v", err)
			}
		})
	}
}

func TestValidateRecordPoliciesMissingAndErrorBranches(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace"}}
	repository := &relatedPolicyRepositoryProbe{}
	validator := newRelatedPolicyValidatorForTest(repository, map[string]definitionmodel.ObjectSchema{"account": relatedPolicyObject("account")}, nil)
	object := recordPolicyRelationObject(definitionmodel.ValidationSchema{
		Key: "status", Type: "related_status_guard", Config: map[string]any{"relation_field": "account_id", "status_field": "state", "allowed_statuses": []any{"active"}},
	})

	if err := validator.ValidateRecordPolicies(t.Context(), object, map[string]any{}, "update", principal); err != nil {
		t.Fatalf("missing optional relation rejected: %v", err)
	}

	fault := errors.New("related status fault")
	repository.err = fault
	err := validator.ValidateRecordPolicies(t.Context(), object, map[string]any{"account_id": "account-1"}, "update", principal)
	assertRecordCause(t, err, fault)

	repository.err, repository.found = nil, true
	repository.record = recordmodel.Record{ID: "account-1", Data: map[string]any{"state": "active"}}
	if err := validator.ValidateRecordPolicies(t.Context(), object, map[string]any{"account_id": "account-1"}, "update", principal); err != nil {
		t.Fatalf("custom status field rejected: %v", err)
	}
}

func TestValidateNumericLimitPoliciesPropagatesRelationAndMatchFailures(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace"}}
	fault := errors.New("numeric relation fault")
	repository := &relatedPolicyRepositoryProbe{err: fault}
	objects := map[string]definitionmodel.ObjectSchema{"budget": relatedPolicyObject("budget"), "source": relatedPolicyObject("source")}
	validator := newRelatedPolicyValidatorForTest(repository, objects, nil)

	direct := numericRelationObject(numericLimitValidation(map[string]any{"value_field": "amount", "relation_field": "budget_id", "target_field": "remaining"}))
	err := validator.ValidateNumericLimitPolicies(t.Context(), direct, map[string]any{"amount": 10, "budget_id": "budget-1"}, "create", principal)
	assertRecordCause(t, err, fault)

	match := numericRelationObject(numericLimitValidation(map[string]any{
		"value_field": "amount", "source_relation_field": "source_id", "source_field": "code",
		"target_object": "budget", "target_match_field": "code", "target_field": "remaining",
	}))
	err = validator.ValidateNumericLimitPolicies(t.Context(), match, map[string]any{"amount": 10, "source_id": "source-1"}, "create", principal)
	assertRecordCause(t, err, fault)

	repository.err, repository.found = nil, true
	repository.record = recordmodel.Record{ID: "source-1", Data: map[string]any{"code": "A"}}
	repository.pages = map[int]recordmodel.RecordPageResult{1: {}}
	err = validator.ValidateNumericLimitPolicies(t.Context(), match, map[string]any{"amount": 10, "source_id": "source-1"}, "create", principal)
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.policy.related_record_missing", nil)

	match.Validations[0].Config["source_relation_field"] = ""
	repository.pages[1] = recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "budget-A", Data: map[string]any{"remaining": 5.0}}}}
	err = validator.ValidateNumericLimitPolicies(t.Context(), match, map[string]any{"amount": 10, "code": "A"}, "create", principal)
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.expense.budget_exceeded", nil)
}

func TestValidateBlockingRecordPoliciesBranches(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace"}}
	policy := definitionmodel.ValidationSchema{Key: "no_tasks", Type: "no_related_records", Config: map[string]any{}}
	repository := &relatedPolicyRepositoryProbe{pages: map[int]recordmodel.RecordPageResult{1: {}}}
	objects := map[string]definitionmodel.ObjectSchema{"task": relatedPolicyObject("task")}
	allow := true
	validator := newRelatedPolicyValidatorForTest(repository, objects, &allow)

	err := validator.ValidateBlockingRecordPolicies(t.Context(), definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{policy}}, nil, "delete", principal)
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.policy.relation_target_unavailable", nil)

	policy.Config["object_key"] = "missing"
	err = validator.ValidateBlockingRecordPolicies(t.Context(), definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{policy}}, nil, "delete", principal)
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.policy.relation_target_unavailable", map[string]string{"object": "missing"})

	policy.Config["object_key"] = "task"
	fault := errors.New("blocking list fault")
	repository.err = fault
	err = validator.ValidateBlockingRecordPolicies(t.Context(), definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{policy}}, nil, "delete", principal)
	assertRecordCause(t, err, fault)

	repository.err = nil
	policy.Config["filters"] = []any{map[string]any{"field": ""}, map[string]any{"field": "owner_id", "source_field": "missing"}}
	repository.pages = map[int]recordmodel.RecordPageResult{
		1: {HasNext: true, Items: []recordmodel.Record{{ID: "hidden", Data: map[string]any{"status": "active"}}}},
		2: {},
	}
	allow = false
	if err := validator.ValidateBlockingRecordPolicies(t.Context(), definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{policy}}, nil, "delete", principal); err != nil {
		t.Fatalf("non-blocking pages rejected: %v", err)
	}
	if len(repository.queries) < 2 || repository.queries[len(repository.queries)-1].Page != 2 {
		t.Fatalf("blocking pagination = %#v", repository.queries)
	}
}

func TestValidateDynamicReferencePoliciesMissingValuesAndTarget(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace"}}
	policy := definitionmodel.ValidationSchema{Key: "dynamic", Type: "dynamic_record_reference", Message: "backend.dynamic.invalid", Config: map[string]any{"object_field": "kind", "record_field": "id"}}
	validator := newRelatedPolicyValidatorForTest(&relatedPolicyRepositoryProbe{}, map[string]definitionmodel.ObjectSchema{}, nil)
	object := definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{policy}}

	if err := validator.ValidateDynamicReferencePolicies(t.Context(), object, map[string]any{"kind": nil, "id": "record"}, "create", principal); err != nil {
		t.Fatalf("empty dynamic reference rejected: %v", err)
	}
	err := validator.ValidateDynamicReferencePolicies(t.Context(), object, map[string]any{"kind": "missing", "id": "record"}, "create", principal)
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.dynamic.invalid", map[string]string{"object": "missing"})
}

func TestValidateTimeOverlapPoliciesErrorAndNoConflictBranches(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace"}}
	policy := temporalExclusionTestPolicy("schedule", "")
	object := definitionmodel.ObjectSchema{Key: "booking", Validations: []definitionmodel.ValidationSchema{policy}}
	repository := &relatedPolicyRepositoryProbe{pages: map[int]recordmodel.RecordPageResult{1: {}}}
	validator := newRelatedPolicyValidatorForTest(repository, nil, nil)

	err := validator.ValidateTimeOverlapPolicies(t.Context(), object, map[string]any{"starts_at": "bad", "ends_at": "2026-07-19T12:00:00Z"}, "", "create", principal)
	if err == nil {
		t.Fatal("invalid time range was accepted")
	}
	if err := validator.ValidateTimeOverlapPolicies(t.Context(), object, map[string]any{}, "", "create", principal); err != nil {
		t.Fatalf("missing time range rejected: %v", err)
	}
	if err := validator.ValidateTimeOverlapPolicies(t.Context(), object, map[string]any{"status": "void"}, "", "create", principal); err != nil {
		t.Fatalf("custom cancelled status rejected: %v", err)
	}

	data := map[string]any{"owner": "room-1", "starts_at": "2026-07-19T10:00:00Z", "ends_at": "2026-07-19T11:00:00Z"}
	fault := errors.New("overlap list fault")
	repository.err = fault
	err = validator.ValidateTimeOverlapPolicies(t.Context(), object, data, "", "create", principal)
	assertRecordCause(t, err, fault)

	repository.err = nil
	repository.pages[1] = recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "candidate", Data: map[string]any{"owner": "room-1", "starts_at": "bad", "ends_at": "2026-07-19T12:00:00Z"}}}}
	err = validator.ValidateTimeOverlapPolicies(t.Context(), object, data, "", "create", principal)
	if err == nil {
		t.Fatal("invalid candidate range was accepted")
	}

	repository.pages[1] = recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "candidate", Data: map[string]any{"owner": "room-1", "starts_at": "2026-07-19T11:00:00Z", "ends_at": "2026-07-19T12:00:00Z"}}}}
	if err := validator.ValidateTimeOverlapPolicies(t.Context(), object, data, "", "create", principal); err != nil {
		t.Fatalf("non-overlapping range rejected: %v", err)
	}
}

func TestRelatedPolicyDefaultDependencies(t *testing.T) {
	validator := NewRecordRelatedPolicyValidator(RecordRelatedPolicyDependencies{})
	if _, ok := validator.object(t.Context(), "missing"); ok {
		t.Fatal("nil object dependency unexpectedly resolved object")
	}
	if !validator.canAccess(principalmodel.Principal{}, definitionmodel.ObjectSchema{}, recordmodel.Record{}) {
		t.Fatal("nil access dependency must allow access")
	}
}

func TestRelatedTimeOverlapRemainingPolicyShapes(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace"}}
	validator := newRelatedPolicyValidatorForTest(&relatedPolicyRepositoryProbe{}, nil, nil)
	malformed := definitionmodel.ObjectSchema{Key: "booking", Validations: []definitionmodel.ValidationSchema{{Type: "temporal_exclusion", Config: map[string]any{}}}}
	if err := validator.ValidateTimeOverlapPolicies(t.Context(), malformed, map[string]any{}, "", "create", principal); err == nil {
		t.Fatal("malformed temporal policy accepted")
	}

	policy := temporalExclusionTestPolicy("schedule", "")
	object := definitionmodel.ObjectSchema{Key: "booking", Validations: []definitionmodel.ValidationSchema{policy}}
	if err := validator.ValidateTimeOverlapPolicies(t.Context(), object, map[string]any{"owner": "room", "starts_at": "", "ends_at": ""}, "", "create", principal); err != nil {
		t.Fatalf("empty candidate range err=%v", err)
	}

	policy.Config["status_field"] = ""
	policy.Config["excluded_statuses"] = []any{}
	object.Validations = []definitionmodel.ValidationSchema{policy}
	repository := &relatedPolicyRepositoryProbe{pages: map[int]recordmodel.RecordPageResult{1: {Items: []recordmodel.Record{{
		ID: "candidate", Data: map[string]any{"owner": "room", "starts_at": "2026-07-19T12:00:00Z", "ends_at": "2026-07-19T13:00:00Z"},
	}}}}}
	validator = newRelatedPolicyValidatorForTest(repository, nil, nil)
	if err := validator.ValidateTimeOverlapPolicies(t.Context(), object, map[string]any{"owner": "room", "starts_at": "2026-07-19T10:00:00Z", "ends_at": "2026-07-19T11:00:00Z"}, "", "create", principal); err != nil {
		t.Fatalf("status-free temporal policy err=%v", err)
	}
}
