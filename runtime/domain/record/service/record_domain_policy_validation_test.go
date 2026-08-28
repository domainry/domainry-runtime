package service

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestRelatedPolicyValidatorOrchestratesDomainPoliciesAndDeniedObserver(t *testing.T) {
	validator := NewRecordRelatedPolicyValidator(RecordRelatedPolicyDependencies{})
	object := definitionmodel.ObjectSchema{Key: "invoice", Validations: []definitionmodel.ValidationSchema{
		{Key: "large_discount", Type: "threshold_permission", FieldKey: "discount", Config: map[string]any{"threshold": 20, "permission": "invoice.approve_discount"}},
		{Key: "locked_total", Type: "immutable_after_status", Fields: []string{"total"}, Config: map[string]any{"statuses": []any{"posted"}}},
	}}
	before := map[string]any{"status": "posted", "total": 100, "discount": 0}
	next := map[string]any{"status": "posted", "total": 120, "discount": 25}
	var deniedType string
	var deniedErr error

	err := validator.ValidateDomainPolicies(t.Context(), object, before, next, "invoice-1", "update", principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, func(err error, policyType string) {
		deniedErr = err
		deniedType = policyType
	})
	assertRecordAppError(t, err, apperror.KindForbidden, "backend.policy.threshold_permission_required", map[string]string{
		"field": "discount", "permission": "invoice.approve_discount",
	})
	if deniedErr != err || deniedType != "threshold_permission" {
		t.Fatalf("denied observer = (%v, %q), want (%v, threshold_permission)", deniedErr, deniedType, err)
	}

	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"invoice.approve_discount"}})
	deniedType = ""
	err = validator.ValidateDomainPolicies(t.Context(), object, before, next, "invoice-1", "update", principal, func(_ error, policyType string) {
		deniedType = policyType
	})
	assertRecordAppError(t, err, apperror.KindForbidden, "backend.policy.immutable_after_status", map[string]string{
		"field": "total", "policy": "locked_total", "status": "posted",
	})
	if deniedType != "immutable_after_status" {
		t.Fatalf("denied type = %q, want immutable_after_status", deniedType)
	}
}

func TestRelatedPolicyValidatorDomainPoliciesAcceptAuthorizedThreshold(t *testing.T) {
	validator := NewRecordRelatedPolicyValidator(RecordRelatedPolicyDependencies{})
	object := definitionmodel.ObjectSchema{Key: "invoice", Validations: []definitionmodel.ValidationSchema{{
		Key: "large_discount", Type: "threshold_permission", FieldKey: "discount", Config: map[string]any{"threshold": 20, "permission": "invoice.approve_discount"},
	}}}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}, accessfixture.Bundle{Permissions: []string{"invoice.approve_discount"}})
	if err := validator.ValidateDomainPolicies(t.Context(), object, nil, map[string]any{"discount": 25}, "invoice-1", "update", principal, nil); err != nil {
		t.Fatalf("authorized policies rejected: %v", err)
	}
}
