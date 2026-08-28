package service

import (
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestRelatedPolicyOperationAndConditionShortCircuits(t *testing.T) {
	validator := NewRecordRelatedPolicyValidator(RecordRelatedPolicyDependencies{})
	principal := principalmodel.Principal{}
	data := map[string]any{"state": "inactive", "amount": 10, "target_object": "customer", "target_record_id": "record", "starts_at": "bad", "ends_at": "bad"}
	tests := []struct {
		typeName string
		call     func(definitionmodel.ObjectSchema) error
	}{
		{"related_record_status", func(o definitionmodel.ObjectSchema) error {
			return validator.ValidateRecordPolicies(t.Context(), o, data, "update", principal)
		}},
		{"related_numeric_limit", func(o definitionmodel.ObjectSchema) error {
			return validator.ValidateNumericLimitPolicies(t.Context(), o, data, "update", principal)
		}},
		{"blocking_related_records", func(o definitionmodel.ObjectSchema) error {
			return validator.ValidateBlockingRecordPolicies(t.Context(), o, data, "update", principal)
		}},
		{"dynamic_reference_exists", func(o definitionmodel.ObjectSchema) error {
			return validator.ValidateDynamicReferencePolicies(t.Context(), o, data, "update", principal)
		}},
		{"temporal_exclusion", func(o definitionmodel.ObjectSchema) error {
			return validator.ValidateTimeOverlapPolicies(t.Context(), o, data, "record", "update", principal)
		}},
	}
	for _, test := range tests {
		policy := definitionmodel.ValidationSchema{Type: test.typeName, Config: map[string]any{"operations": []any{"create"}}}
		if test.typeName == "temporal_exclusion" {
			policy = temporalExclusionTestPolicy("schedule", "")
			policy.Config["operations"] = []any{"create"}
		}
		if err := test.call(definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{policy}}); err != nil {
			t.Fatalf("%s operation skip: %v", test.typeName, err)
		}
		if test.typeName == "related_record_status" {
			continue
		}
		policy.Config = map[string]any{"when_field": "state", "value": "active"}
		if test.typeName == "temporal_exclusion" {
			policy.Config = temporalExclusionTestPolicy("schedule", "").Config
			policy.Config["when_field"], policy.Config["value"] = "state", "active"
		}
		if err := test.call(definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{policy}}); err != nil {
			t.Fatalf("%s condition skip: %v", test.typeName, err)
		}
	}
}

func TestRelatedPolicyFieldFallbackAndLookupShortCircuits(t *testing.T) {
	repository := &relatedPolicyRepositoryProbe{found: true, record: recordmodel.Record{Data: map[string]any{"locked": false}}}
	validator := newRelatedPolicyValidatorForTest(repository, map[string]definitionmodel.ObjectSchema{"account": relatedPolicyObject("account")}, nil)
	principal := principalmodel.Principal{}
	object := recordPolicyRelationObject(definitionmodel.ValidationSchema{Type: "related_record_status"})
	if err := validator.ValidateRecordPolicies(t.Context(), object, nil, "update", principal); err != nil {
		t.Fatal(err)
	}
	object.Validations = []definitionmodel.ValidationSchema{{Type: "related_boolean_false", FieldKey: "account_id", Config: map[string]any{"related_field": "locked"}}}
	if err := validator.ValidateRecordPolicies(t.Context(), object, map[string]any{"account_id": "account-1"}, "update", principal); err != nil {
		t.Fatal(err)
	}
	for _, config := range []map[string]any{
		{"target_object": "account"},
		{"target_object": "account", "source_field": "code"},
	} {
		_, _, _, found, err := validator.relatedRecordForValidation(t.Context(), object, nil, definitionmodel.ValidationSchema{Config: config}, "", principal)
		if err != nil || found {
			t.Fatalf("lookup short circuit config=%#v found=%v err=%v", config, found, err)
		}
	}
	if _, _, err := validator.relatedRecord(t.Context(), object, map[string]any{"account_id": ""}, "account_id", principal); err == nil {
		t.Fatal("empty related record id accepted")
	}
}

func TestRelatedPolicyTimeAndBlockingCandidateShortCircuits(t *testing.T) {
	principal := principalmodel.Principal{}
	policy := temporalExclusionTestPolicy("schedule", "")
	object := definitionmodel.ObjectSchema{Key: "booking", Validations: []definitionmodel.ValidationSchema{policy}}
	data := map[string]any{"owner": "room-1", "starts_at": "2026-07-19T10:00:00Z", "ends_at": "2026-07-19T11:00:00Z"}
	repository := &relatedPolicyRepositoryProbe{pages: map[int]recordmodel.RecordPageResult{1: {Items: []recordmodel.Record{
		{ID: "scope", Data: map[string]any{"owner": "room-2", "starts_at": "2026-07-19T10:00:00Z", "ends_at": "2026-07-19T11:00:00Z"}},
		{ID: "missing", Data: map[string]any{"owner": "room-1"}},
		{ID: "before", Data: map[string]any{"owner": "room-1", "starts_at": "2026-07-19T08:00:00Z", "ends_at": "2026-07-19T09:00:00Z"}},
	}}}}
	validator := newRelatedPolicyValidatorForTest(repository, nil, nil)
	if err := validator.ValidateTimeOverlapPolicies(t.Context(), object, data, "current", "create", principal); err != nil {
		t.Fatal(err)
	}
	blockingRepository := &relatedPolicyRepositoryProbe{pages: map[int]recordmodel.RecordPageResult{1: {Items: []recordmodel.Record{{ID: "nonmatch", Data: map[string]any{"status": "closed"}}}}}}
	blocking := newRelatedPolicyValidatorForTest(blockingRepository, map[string]definitionmodel.ObjectSchema{"task": relatedPolicyObject("task")}, nil)
	validation := definitionmodel.ValidationSchema{Config: map[string]any{"filters": []any{map[string]any{"field": "status", "value": "active"}}}}
	if found, err := blocking.blockingRecordExists(t.Context(), relatedPolicyObject("task"), validation, nil, principal); err != nil || found {
		t.Fatalf("blocking nonmatch found=%v err=%v", found, err)
	}
}

func TestRelatedPolicyLateTypeAliasesAndEmptyDynamicOperands(t *testing.T) {
	validator := NewRecordRelatedPolicyValidator(RecordRelatedPolicyDependencies{})
	principal := principalmodel.Principal{}
	for _, typeName := range []string{"related_numeric_guard", "related_field_numeric_limit"} {
		object := definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{{Type: typeName, Severity: "warning"}}}
		if err := validator.ValidateNumericLimitPolicies(t.Context(), object, nil, "create", principal); err != nil {
			t.Fatalf("numeric alias %s: %v", typeName, err)
		}
	}
	object := definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{temporalExclusionTestPolicy("schedule", "warning")}}
	if err := validator.ValidateTimeOverlapPolicies(t.Context(), object, nil, "", "create", principal); err != nil {
		t.Fatalf("time alias: %v", err)
	}
	dynamic := definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{{Type: "dynamic_reference_exists"}}}
	for _, data := range []map[string]any{
		{"target_object": "", "target_record_id": "record"},
		{"target_object": "customer", "target_record_id": ""},
		{"target_object": "customer", "target_record_id": nil},
	} {
		if err := validator.ValidateDynamicReferencePolicies(t.Context(), dynamic, data, "create", principal); err != nil {
			t.Fatalf("empty dynamic %#v: %v", data, err)
		}
	}
}

func temporalExclusionTestPolicy(key, severity string) definitionmodel.ValidationSchema {
	return definitionmodel.ValidationSchema{Key: key, Type: "temporal_exclusion", Severity: severity, Config: map[string]any{
		"start_field": "starts_at", "end_field": "ends_at", "scope_fields": []any{"owner"}, "status_field": "status", "excluded_statuses": []any{"void"},
	}}
}
