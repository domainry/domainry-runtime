package service

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func relatedPolicyObject(key string) definitionmodel.ObjectSchema {
	return definitionmodel.ObjectSchema{Key: key}
}

func newRelatedPolicyValidatorForTest(repository *relatedPolicyRepositoryProbe, objects map[string]definitionmodel.ObjectSchema, allow *bool) *RecordRelatedPolicyValidator {
	return NewRecordRelatedPolicyValidator(RecordRelatedPolicyDependencies{
		Repository: repository,
		Object: func(_ context.Context, key string) (definitionmodel.ObjectSchema, bool) {
			object, ok := objects[key]
			return object, ok
		},
		CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool {
			return allow == nil || *allow
		},
	})
}

func recordPolicyRelationObject(validations ...definitionmodel.ValidationSchema) definitionmodel.ObjectSchema {
	return definitionmodel.ObjectSchema{
		Key:         "invoice",
		Fields:      []definitionmodel.FieldSchema{{Key: "account_id", Type: "relation", Config: map[string]any{"object_key": "account"}}},
		Validations: validations,
	}
}

func TestValidateRecordPoliciesRelatedStatusAndFieldResolution(t *testing.T) {
	repository := &relatedPolicyRepositoryProbe{record: recordmodel.Record{ID: "account-1", Data: map[string]any{"status": "active"}}, found: true}
	validator := newRelatedPolicyValidatorForTest(repository, map[string]definitionmodel.ObjectSchema{"account": relatedPolicyObject("account")}, nil)
	object := recordPolicyRelationObject(
		definitionmodel.ValidationSchema{Key: "ignored", Type: "unrelated"},
		definitionmodel.ValidationSchema{Key: "field_key", Type: "related_record_status", FieldKey: "account_id", Config: map[string]any{"allowed_statuses": []any{"active"}}},
		definitionmodel.ValidationSchema{Key: "field_config", Type: "related_status_guard", Config: map[string]any{"field": "account_id", "must_statuses": []any{"active"}}},
		definitionmodel.ValidationSchema{Key: "relation_config", Type: "related_record_status", Config: map[string]any{"relation_field": "account_id", "disallowed_statuses": []any{"disabled"}}},
		definitionmodel.ValidationSchema{Key: "fields", Type: "related_record_status", Fields: []string{"account_id"}, Config: map[string]any{"must_not_statuses": []any{"closed"}}},
	)
	if err := validator.ValidateRecordPolicies(t.Context(), object, map[string]any{"account_id": "account-1"}, "update", principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace"}}); err != nil {
		t.Fatalf("valid related statuses rejected: %v", err)
	}

	repository.record.Data["status"] = "blocked"
	err := validator.ValidateRecordPolicies(t.Context(), object, map[string]any{"account_id": "account-1"}, "update", principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace"}})
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.policy.related_status_not_allowed", map[string]string{"object": "account", "status": "blocked"})

	repository.record.Data["status"] = "disabled"
	object.Validations = []definitionmodel.ValidationSchema{{Key: "deny", Type: "related_record_status", FieldKey: "account_id", Message: "backend.invoice.account_disabled", Config: map[string]any{"disallowed_statuses": []any{"disabled"}}}}
	err = validator.ValidateRecordPolicies(t.Context(), object, map[string]any{"account_id": "account-1"}, "update", principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace"}})
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.invoice.account_disabled", map[string]string{"status": "disabled"})

	object.Validations[0].Config = map[string]any{"must_not_statuses": []any{"disabled"}}
	err = validator.ValidateRecordPolicies(t.Context(), object, map[string]any{"account_id": "account-1"}, "update", principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace"}})
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.invoice.account_disabled", nil)
}

func TestValidateRecordPoliciesRelatedBoolean(t *testing.T) {
	repository := &relatedPolicyRepositoryProbe{record: recordmodel.Record{ID: "account-1", Data: map[string]any{"locked": true}}, found: true}
	validator := newRelatedPolicyValidatorForTest(repository, map[string]definitionmodel.ObjectSchema{"account": relatedPolicyObject("account")}, nil)
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace"}}

	object := recordPolicyRelationObject(definitionmodel.ValidationSchema{Key: "missing_field", Type: "related_boolean_false", FieldKey: "account_id"})
	err := validator.ValidateRecordPolicies(t.Context(), object, map[string]any{"account_id": "account-1"}, "update", principal)
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.policy.related_field_required", map[string]string{"policy": "missing_field"})

	object.Validations = []definitionmodel.ValidationSchema{{Key: "not_locked", Type: "related_field_boolean_false", FieldKey: "account_id", Message: "backend.invoice.account_locked", Config: map[string]any{"field_key": "locked"}}}
	err = validator.ValidateRecordPolicies(t.Context(), object, map[string]any{"account_id": "account-1"}, "update", principal)
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.invoice.account_locked", map[string]string{"object": "account", "field": "locked"})

	repository.record.Data["locked"] = false
	if err := validator.ValidateRecordPolicies(t.Context(), object, map[string]any{"account_id": "account-1"}, "update", principal); err != nil {
		t.Fatalf("false related boolean rejected: %v", err)
	}
	repository.record.Data["locked"] = "unknown"
	if err := validator.ValidateRecordPolicies(t.Context(), object, map[string]any{"account_id": "account-1"}, "update", principal); err != nil {
		t.Fatalf("non-boolean related value rejected: %v", err)
	}
}

func TestRelatedRecordForValidationFailuresAndSuccess(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace"}}
	object := recordPolicyRelationObject()
	validation := definitionmodel.ValidationSchema{Key: "policy"}
	repository := &relatedPolicyRepositoryProbe{}
	allow := true
	objects := map[string]definitionmodel.ObjectSchema{"account": relatedPolicyObject("account")}
	validator := newRelatedPolicyValidatorForTest(repository, objects, &allow)

	if _, _, _, found, err := validator.relatedRecordForValidation(t.Context(), object, nil, validation, "", principal); err != nil || found {
		t.Fatalf("empty relation: found=%v err=%v", found, err)
	}
	_, _, _, _, err := validator.relatedRecordForValidation(t.Context(), object, map[string]any{"missing": "id"}, validation, "missing", principal)
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.policy.relation_field_missing", nil)

	delete(objects, "account")
	_, _, _, _, err = validator.relatedRecordForValidation(t.Context(), object, map[string]any{"account_id": "account-1"}, validation, "account_id", principal)
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.policy.relation_target_unavailable", nil)
	objects["account"] = relatedPolicyObject("account")

	fault := errors.New("record lookup fault")
	repository.err = fault
	_, _, _, _, err = validator.relatedRecordForValidation(t.Context(), object, map[string]any{"account_id": "account-1"}, validation, "account_id", principal)
	assertRecordCause(t, err, fault)
	repository.err = nil

	repository.found = false
	_, _, _, _, err = validator.relatedRecordForValidation(t.Context(), object, map[string]any{"account_id": "account-1"}, validation, "account_id", principal)
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.policy.related_record_missing", nil)

	repository.found, repository.record = true, recordmodel.Record{ID: "account-1"}
	allow = false
	_, _, _, _, err = validator.relatedRecordForValidation(t.Context(), object, map[string]any{"account_id": "account-1"}, validation, "account_id", principal)
	assertRecordAppError(t, err, apperror.KindForbidden, "backend.record.outside_scope", nil)

	allow = true
	targetKey, targetObject, related, found, err := validator.relatedRecordForValidation(t.Context(), object, map[string]any{"account_id": "account-1"}, validation, "account_id", principal)
	if err != nil || !found || targetKey != "account" || targetObject.Key != "account" || related.ID != "account-1" {
		t.Fatalf("related record = %q %#v %#v found=%v err=%v", targetKey, targetObject, related, found, err)
	}
}

func TestLookupRelatedRecordBranches(t *testing.T) {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace"}}
	repository := &relatedPolicyRepositoryProbe{pages: map[int]recordmodel.RecordPageResult{1: {Items: []recordmodel.Record{{ID: "account-1"}}}}}
	allow := false
	objects := map[string]definitionmodel.ObjectSchema{"account": relatedPolicyObject("account")}
	validator := newRelatedPolicyValidatorForTest(repository, objects, &allow)
	validation := definitionmodel.ValidationSchema{Key: "lookup", Config: map[string]any{}}

	delete(objects, "account")
	_, _, _, _, err := validator.lookupRelatedRecord(t.Context(), validation, map[string]any{"code": "A"}, principal, "account", "code", "external_code")
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.policy.relation_target_unavailable", nil)
	objects["account"] = relatedPolicyObject("account")

	_, _, _, found, err := validator.lookupRelatedRecord(t.Context(), validation, nil, principal, "account", "code", "external_code")
	if err != nil || found {
		t.Fatalf("empty source lookup: found=%v err=%v", found, err)
	}

	fault := errors.New("list fault")
	repository.err = fault
	_, _, _, _, err = validator.lookupRelatedRecord(t.Context(), validation, map[string]any{"code": "A"}, principal, "account", "code", "external_code")
	assertRecordCause(t, err, fault)
	repository.err = nil

	validation.Config["allow_missing"] = true
	_, _, _, found, err = validator.lookupRelatedRecord(t.Context(), validation, map[string]any{"code": "A"}, principal, "account", "code", "external_code")
	if err != nil || found {
		t.Fatalf("allowed missing lookup: found=%v err=%v", found, err)
	}
	delete(validation.Config, "allow_missing")
	_, _, _, _, err = validator.lookupRelatedRecord(t.Context(), validation, map[string]any{"code": "A"}, principal, "account", "code", "external_code")
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.policy.related_record_missing", nil)

	validation.Config["system_lookup"] = true
	key, object, record, found, err := validator.lookupRelatedRecord(t.Context(), validation, map[string]any{"code": "A"}, principal, "account", "code", "external_code")
	if err != nil || !found || key != "account" || object.Key != "account" || record.ID != "account-1" {
		t.Fatalf("system lookup result: %q %#v %#v found=%v err=%v", key, object, record, found, err)
	}

	validation.Config["system_lookup"] = false
	allow = true
	_, _, _, found, err = validator.lookupRelatedRecord(t.Context(), validation, map[string]any{"code": "A"}, principal, "account", "code", "external_code")
	if err != nil || !found {
		t.Fatalf("accessible lookup: found=%v err=%v", found, err)
	}
}

func TestRelatedRecordForValidationUsesMatchLookup(t *testing.T) {
	repository := &relatedPolicyRepositoryProbe{pages: map[int]recordmodel.RecordPageResult{1: {Items: []recordmodel.Record{{ID: "account-1"}}}}}
	validator := newRelatedPolicyValidatorForTest(repository, map[string]definitionmodel.ObjectSchema{"account": relatedPolicyObject("account")}, nil)
	validation := definitionmodel.ValidationSchema{Key: "by_code", Config: map[string]any{"target_object": "account", "source_field": "account_code", "target_match_field": "external_code"}}
	key, _, record, found, err := validator.relatedRecordForValidation(t.Context(), definitionmodel.ObjectSchema{}, map[string]any{"account_code": "A"}, validation, "", principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace"}})
	if err != nil || !found || key != "account" || record.ID != "account-1" || repository.queries[0].Filters["external_code"] != "A" {
		t.Fatalf("match lookup = %q %#v found=%v queries=%#v err=%v", key, record, found, repository.queries, err)
	}
}

func assertRecordCause(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want cause %v", err, want)
	}
}
