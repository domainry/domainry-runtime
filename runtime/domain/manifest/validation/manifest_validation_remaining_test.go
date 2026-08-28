package validation

import (
	"strings"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestValidateObjectLifecyclePolicyRequiresCompleteGenericContract(t *testing.T) {
	objects := []definitionmodel.ObjectSchema{
		{Key: "mutable", LifecyclePolicy: &definitionmodel.ObjectLifecyclePolicy{Mode: definitionmodel.ObjectLifecycleMutable}},
		{Key: "append", LifecyclePolicy: &definitionmodel.ObjectLifecyclePolicy{Mode: definitionmodel.ObjectLifecycleAppendOnly}},
		{Key: "soft", Fields: []definitionmodel.FieldSchema{{Key: "status"}}, LifecyclePolicy: &definitionmodel.ObjectLifecyclePolicy{Mode: definitionmodel.ObjectLifecycleSoftDeleteOnly}},
		{Key: "locked", Fields: []definitionmodel.FieldSchema{{Key: "status"}}, LifecyclePolicy: &definitionmodel.ObjectLifecyclePolicy{Mode: definitionmodel.ObjectLifecycleImmutableAfterState, StateField: "status", ImmutableStates: []string{"confirmed", "confirmed", ""}}},
		{Key: "unknown", LifecyclePolicy: &definitionmodel.ObjectLifecyclePolicy{Mode: "archive_only"}},
	}
	state := newValidationState(manifestmodel.ManifestSchema{Objects: objects}, nil)
	state.validateObjects()
	message := ValidationErrors(state.errs).Error()
	for _, expected := range []string{"soft_delete_only requires field \"deleted_at\"", "soft_delete_only requires field \"deleted_by\"", "duplicate state \"confirmed\"", "must not be empty", "must be mutable, soft_delete_only, append_only, or immutable_after_state"} {
		if !strings.Contains(message, expected) {
			t.Fatalf("missing %q in %s", expected, message)
		}
	}
	valid := newValidationState(manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: "settlement", Fields: []definitionmodel.FieldSchema{{Key: "status"}}, LifecyclePolicy: &definitionmodel.ObjectLifecyclePolicy{Mode: definitionmodel.ObjectLifecycleImmutableAfterState, StateField: "status", ImmutableStates: []string{"confirmed"}}}}}, nil)
	valid.validateObjects()
	if len(valid.errs) != 0 {
		t.Fatalf("valid lifecycle rejected: %v", valid.errs)
	}
}

func TestValidateObjectLedgerPolicyRequiresCanonicalAppendOnlySchema(t *testing.T) {
	invalid := definitionmodel.ObjectSchema{Key: "entry", LifecyclePolicy: &definitionmodel.ObjectLifecyclePolicy{Mode: definitionmodel.ObjectLifecycleMutable}, LedgerPolicy: &definitionmodel.ObjectLedgerPolicy{Integrity: "md5", Signature: "rsa"}, Fields: []definitionmodel.FieldSchema{{Key: "amount", Type: "number"}}}
	state := newValidationState(manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{invalid}}, nil)
	state.validateObjects()
	message := ValidationErrors(state.errs).Error()
	for _, expected := range []string{"requires lifecycle_policy.mode append_only", "must be sha256_chain", "must be none or hmac_sha256", "field \"amount\" must have type currency", "requires canonical field \"account_id\""} {
		if !strings.Contains(message, expected) {
			t.Fatalf("missing %q in %s", expected, message)
		}
	}
	fields := []definitionmodel.FieldSchema{}
	for _, key := range []string{"account_id", "business_key", "entry_kind", "direction", "balance_bucket", "currency", "source_reference", "actor_id", "rule_version", "reversal_of", "previous_hash", "entry_hash", "signature"} {
		fields = append(fields, definitionmodel.FieldSchema{Key: key, Type: "text"})
	}
	fields = append(fields, definitionmodel.FieldSchema{Key: "amount", Type: "currency"}, definitionmodel.FieldSchema{Key: "occurred_at", Type: "datetime"}, definitionmodel.FieldSchema{Key: "sequence", Type: "number"})
	valid := newValidationState(manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: "entry", Fields: fields, LifecyclePolicy: &definitionmodel.ObjectLifecyclePolicy{Mode: definitionmodel.ObjectLifecycleAppendOnly}, LedgerPolicy: &definitionmodel.ObjectLedgerPolicy{Integrity: definitionmodel.ObjectLedgerIntegritySHA256Chain, Signature: definitionmodel.ObjectLedgerSignatureHMACSHA256}}}}, nil)
	valid.validateObjects()
	if len(valid.errs) != 0 {
		t.Fatalf("valid ledger rejected: %v", valid.errs)
	}
}

func TestValidateObjectExportAssurancePolicyIsClosedAndBusinessNeutral(t *testing.T) {
	invalid := definitionmodel.ObjectSchema{Key: "customer", ExportAssurancePolicy: &definitionmodel.ActionAssurancePolicy{
		RequiredMethods: []string{"otp", "otp", "unknown"}, RecentReauthMaxAgeSeconds: 300, MakerField: "created_by",
	}}
	state := newValidationState(manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{invalid}}, nil)
	state.validateObjects()
	message := ValidationErrors(state.errs).Error()
	for _, expected := range []string{"duplicate method", "unknown method", "requires recent_reauth", "record-bound action selector fields are not valid"} {
		if !strings.Contains(message, expected) {
			t.Fatalf("missing %q in %s", expected, message)
		}
	}
	valid := newValidationState(manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: "customer", ExportAssurancePolicy: &definitionmodel.ActionAssurancePolicy{
		RequiredMethods: []string{definitionmodel.ActionAssuranceRecentReauth, definitionmodel.ActionAssuranceOTP, definitionmodel.ActionAssuranceWorkflowApproval}, RecentReauthMaxAgeSeconds: 300,
	}}}}, nil)
	valid.validateObjects()
	if len(valid.errs) != 0 {
		t.Fatalf("valid export assurance rejected: %v", valid.errs)
	}
}

func TestObjectLifecyclePoliciesCoverGenericBoundaryShapes(t *testing.T) {
	objects := []definitionmodel.ObjectSchema{
		{Key: "export_empty", ExportAssurancePolicy: &definitionmodel.ActionAssurancePolicy{}},
		{Key: "reauth_low", ExportAssurancePolicy: &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceRecentReauth}, RecentReauthMaxAgeSeconds: 0}},
		{Key: "reauth_high", ExportAssurancePolicy: &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceRecentReauth}, RecentReauthMaxAgeSeconds: 86401}},
		{Key: "approval_version", ExportAssurancePolicy: &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceNormalLogin}, ApprovalVersionField: "version"}},
		{Key: "approval_hash", ExportAssurancePolicy: &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceNormalLogin}, ApprovalHashField: "hash"}},
		{Key: "ledger_without_lifecycle", LedgerPolicy: &definitionmodel.ObjectLedgerPolicy{Integrity: definitionmodel.ObjectLedgerIntegritySHA256Chain, Signature: ""}},
		{Key: "ledger_none", LifecyclePolicy: &definitionmodel.ObjectLifecyclePolicy{Mode: definitionmodel.ObjectLifecycleAppendOnly}, LedgerPolicy: &definitionmodel.ObjectLedgerPolicy{Integrity: definitionmodel.ObjectLedgerIntegritySHA256Chain, Signature: definitionmodel.ObjectLedgerSignatureNone}},
		{Key: "mutable_state", LifecyclePolicy: &definitionmodel.ObjectLifecyclePolicy{Mode: definitionmodel.ObjectLifecycleMutable, StateField: "status"}},
		{Key: "append_states", LifecyclePolicy: &definitionmodel.ObjectLifecyclePolicy{Mode: definitionmodel.ObjectLifecycleAppendOnly, ImmutableStates: []string{"locked"}}},
		{Key: "soft_state", Fields: []definitionmodel.FieldSchema{{Key: "status"}, {Key: "deleted_at"}, {Key: "deleted_by"}}, LifecyclePolicy: &definitionmodel.ObjectLifecyclePolicy{Mode: definitionmodel.ObjectLifecycleSoftDeleteOnly, StateField: "status"}},
		{Key: "soft_states", Fields: []definitionmodel.FieldSchema{{Key: "status"}, {Key: "deleted_at"}, {Key: "deleted_by"}}, LifecyclePolicy: &definitionmodel.ObjectLifecyclePolicy{Mode: definitionmodel.ObjectLifecycleSoftDeleteOnly, ImmutableStates: []string{"deleted"}}},
		{Key: "immutable_blank", LifecyclePolicy: &definitionmodel.ObjectLifecyclePolicy{Mode: definitionmodel.ObjectLifecycleImmutableAfterState}},
		{Key: "immutable_missing", LifecyclePolicy: &definitionmodel.ObjectLifecyclePolicy{Mode: definitionmodel.ObjectLifecycleImmutableAfterState, StateField: "missing", ImmutableStates: []string{"locked"}}},
	}
	state := newValidationState(manifestmodel.ManifestSchema{Objects: objects}, nil)
	state.validateObjects()
	message := ValidationErrors(state.errs).Error()
	for _, expected := range []string{
		"must contain at least one method", "must be between 1 and 86400", "record-bound action selector fields",
		"requires lifecycle_policy.mode append_only", "state_field and immutable_states", "is required", "unknown field",
	} {
		if !strings.Contains(message, expected) {
			t.Fatalf("missing %q in %s", expected, message)
		}
	}
}

func TestManifestReviewCoversRestrictiveAndNonDestructiveEdges(t *testing.T) {
	previous := profileReviewManifest()
	previous.IdentityProfileExtensions[0].RequiredPermissions = []string{"employee_profile.read"}
	next := cloneManifestForReviewTest(t, previous)
	next.IdentityProfileExtensions[0].RequiredPermissions = []string{"employee_profile.read", "employee_profile.sensitive.read"}
	result := ReviewManifestUpdate(previous, next, ReviewOptions{})
	if result.HasBlockers() || !reviewChangesContainKind(result.Changes, "restrict_identity_profile_visibility") {
		t.Fatalf("restrictive Profile visibility change=%+v", result)
	}

	valid := loadFixtureManifest(t, "domain-only-minimal.json")
	if err := ValidateManifestUpdate(valid, valid, ReviewOptions{}); err != nil {
		t.Fatalf("unchanged valid manifest rejected: %v", err)
	}

	fieldPrevious := manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}}}
	fieldNext := cloneManifestForReviewTest(t, fieldPrevious)
	fieldNext.Objects[0].Fields[0].Required = true
	fieldReview := ReviewManifestUpdate(fieldPrevious, fieldNext, ReviewOptions{})
	if !fieldReview.HasBlockers() || !reviewChangesContainKind(fieldReview.Blockers, "add_required_without_default") {
		t.Fatalf("required-field change=%+v", fieldReview)
	}

}
