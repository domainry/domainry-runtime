package validation

import (
	"strings"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestValidateActionAssurancePolicyIsTypedAndFieldBound(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "payment", Fields: []definitionmodel.FieldSchema{{Key: "approval_version"}, {Key: "approval_hash"}, {Key: "created_by"}}}
	validAction := definitionmodel.ActionSchema{Key: "payment.refund", ObjectKey: "payment", AssurancePolicy: &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{"normal_login", "recent_reauth", "otp", "maker_checker", "workflow_approval"}, RecentReauthMaxAgeSeconds: 300, ApprovalVersionField: "approval_version", ApprovalHashField: "approval_hash", MakerField: "created_by"}}
	valid := newValidationState(manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{object}, Actions: []definitionmodel.ActionSchema{validAction}}, nil)
	valid.validateActions()
	if len(valid.errs) != 0 {
		t.Fatalf("valid assurance rejected: %v", valid.errs)
	}
	invalidAction := validAction
	invalidAction.AssurancePolicy = &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{"otp", "otp", "magic", "workflow_approval"}, RecentReauthMaxAgeSeconds: 30, ApprovalVersionField: "missing"}
	invalid := newValidationState(manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{object}, Actions: []definitionmodel.ActionSchema{invalidAction}}, nil)
	invalid.validateActions()
	message := ValidationErrors(invalid.errs).Error()
	for _, expected := range []string{"duplicate method", "unknown method", "requires recent_reauth", "unknown field \"missing\"", "approval_hash_field: is required"} {
		if !strings.Contains(message, expected) {
			t.Fatalf("missing %q in %s", expected, message)
		}
	}
}

func TestValidateActionAssurancePolicyCoversEmptyBoundsAndMethodSpecificFields(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "payment", Fields: []definitionmodel.FieldSchema{
		{Key: "approval_version"}, {Key: "approval_hash"}, {Key: "created_by"},
	}}
	cases := []struct {
		name   string
		policy *definitionmodel.ActionAssurancePolicy
	}{
		{name: "empty methods", policy: &definitionmodel.ActionAssurancePolicy{}},
		{name: "recent reauth too low", policy: &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceRecentReauth}}},
		{name: "recent reauth too high", policy: &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceRecentReauth}, RecentReauthMaxAgeSeconds: 86401}},
		{name: "no recent reauth and zero age", policy: &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceNormalLogin}}},
		{name: "approval version without method", policy: &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceNormalLogin}, ApprovalVersionField: "approval_version"}},
		{name: "approval hash without method", policy: &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceNormalLogin}, ApprovalHashField: "approval_hash"}},
		{name: "maker field without method", policy: &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceNormalLogin}, MakerField: "created_by"}},
		{name: "maker field required", policy: &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceMakerChecker}}},
		{name: "maker field unknown", policy: &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceMakerChecker}, MakerField: "missing"}},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			action := definitionmodel.ActionSchema{Key: "payment.refund", ObjectKey: "payment", AssurancePolicy: test.policy}
			state := newValidationState(manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{object}}, nil)
			state.validateActionAssurancePolicy("actions[payment.refund]", action)
			if len(state.errs) == 0 && test.name != "no recent reauth and zero age" {
				t.Fatal("expected assurance policy diagnostic")
			}
		})
	}
}
