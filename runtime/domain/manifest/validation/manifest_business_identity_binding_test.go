package validation

import (
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"strings"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestManifestBusinessIdentityBindingHasOneStrictTypedContract(t *testing.T) {
	profile := definitionmodel.ObjectSchema{Key: "member_profile", UX: map[string]any{"kind": "identity_profile_extension"}, Fields: []definitionmodel.FieldSchema{
		{Key: "identity_user", Type: "relation", Unique: true, Config: map[string]any{"object_key": "identity_user"}},
		{Key: "status", Type: "text", Required: true}, {Key: "blacklisted", Type: "boolean", Required: true}, {Key: "member_no", Type: "text"}, {Key: "email", Type: "email"},
	}}
	identityUser := definitionmodel.ObjectSchema{Key: "identity_user"}
	valid := profilebindingmodel.Binding{
		ContractVersion: profilebindingmodel.ContractVersion, MinReaderVersion: profilebindingmodel.MinimumReaderVersion,
		ObjectKey: "member_profile", IdentityRelationField: "identity_user", Cardinality: "one_to_one", DefaultVisibility: "when_readable",
		BusinessIdentity: profilebindingmodel.BusinessIdentityBinding{Key: "member", StatusField: "status", ActiveStatusValues: []string{"active"}, BlacklistField: "blacklisted", Claims: []profilebindingmodel.ClaimBinding{{ClaimKey: "member_no", FieldKey: "member_no"}}},
		BindingLifecycle: profilebindingmodel.Lifecycle{AllowUnbound: true, InvitationChannels: []string{"email"}, ClaimProofs: []profilebindingmodel.ClaimProof{{Type: "email", FieldKey: "email"}}},
	}
	state := newValidationState(manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{identityUser, profile}, IdentityProfileExtensions: []profilebindingmodel.Binding{valid}}, nil)
	state.validateIdentityProfileExtensions()
	if len(state.errs) != 0 {
		t.Fatalf("valid business identity binding diagnostics=%v", state.errs)
	}
	identityDuplicate := profile
	identityDuplicate.Fields = append(append([]definitionmodel.FieldSchema(nil), profile.Fields...), definitionmodel.FieldSchema{Key: "worker_no", Type: "text"})
	state = newValidationState(manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{identityUser, identityDuplicate}, IdentityProfileExtensions: []profilebindingmodel.Binding{valid}}, nil)
	state.validateIdentityProfileExtensions()
	if len(state.errs) != 1 || !strings.Contains(state.errs[0].Error(), `Runtime identity_user-owned field "worker_no"`) {
		t.Fatalf("Identity User ownership diagnostics=%v", state.errs)
	}

	invalid := valid
	invalid.ContractVersion = "identity-profile-extension-v1"
	invalid.BusinessIdentity = profilebindingmodel.BusinessIdentityBinding{Key: "", ActiveStatusValues: []string{"active"}, BlacklistField: "member_no", Claims: []profilebindingmodel.ClaimBinding{{ClaimKey: "same", FieldKey: "missing"}, {ClaimKey: "same", FieldKey: "member_no"}}}
	invalid.BindingLifecycle = profilebindingmodel.Lifecycle{InvitationChannels: []string{"email", "email", "carrier_pigeon"}, ClaimProofs: []profilebindingmodel.ClaimProof{{Type: "email", FieldKey: "missing"}, {Type: "email", FieldKey: "email"}, {Type: "unknown"}}}
	state = newValidationState(manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{identityUser, profile}, IdentityProfileExtensions: []profilebindingmodel.Binding{invalid}}, nil)
	state.validateIdentityProfileExtensions()
	joined := ""
	for _, err := range state.errs {
		joined += err.Error() + "\n"
	}
	for _, expected := range []string{"contract_version", "business_identity.key", "requires status_field", "must have type boolean", "duplicate claim_key", "unknown field", "duplicate channel", "unsupported channel", "duplicate claim proof", "must be email, phone or external_idp_subject", "allow_unbound"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing %q in diagnostics:\n%s", expected, joined)
		}
	}
}

func TestManifestIdentityProfileLifecycleRemainingOutcomes(t *testing.T) {
	if err := ValidateIdentityProfileBindings(nil, nil); err != nil {
		t.Fatalf("empty profile binding graph error=%v", err)
	}
	if err := ValidateIdentityProfileBindings(nil, []profilebindingmodel.Binding{{}}); err == nil {
		t.Fatal("invalid profile binding graph accepted")
	}

	profile := definitionmodel.ObjectSchema{
		Key: "member_profile",
		UX:  map[string]any{"kind": "identity_profile_extension"},
		Fields: []definitionmodel.FieldSchema{
			{Key: "identity_user", Type: "relation", Unique: true, Required: true, Config: map[string]any{"object_key": "identity_user"}},
			{Key: "status", Type: "text"},
			{Key: "email", Type: "email"},
		},
	}
	base := profilebindingmodel.Binding{
		ContractVersion:  profilebindingmodel.ContractVersion,
		MinReaderVersion: profilebindingmodel.MinimumReaderVersion,
		ObjectKey:        "member_profile", IdentityRelationField: "identity_user",
		Cardinality: "one_to_one", DefaultVisibility: "when_readable",
		BusinessIdentity: profilebindingmodel.BusinessIdentityBinding{Key: "member"},
		BindingLifecycle: profilebindingmodel.Lifecycle{
			AllowUnbound: true,
			ClaimProofs:  []profilebindingmodel.ClaimProof{{Type: "email", FieldKey: "email"}},
		},
	}
	state := newValidationState(manifestmodel.ManifestSchema{
		Objects:                   []definitionmodel.ObjectSchema{{Key: "identity_user"}, profile},
		IdentityProfileExtensions: []profilebindingmodel.Binding{base, base},
	}, nil)
	state.validateIdentityProfileExtensions()
	joined := state.errs.Error()
	for _, expected := range []string{"must be nullable", "duplicate business identity binding"} {
		if !strings.Contains(joined, expected) {
			t.Fatalf("missing %q in diagnostics: %s", expected, joined)
		}
	}

	extension := base
	extension.BindingLifecycle = profilebindingmodel.Lifecycle{
		ClaimProofs: []profilebindingmodel.ClaimProof{
			{Type: "", FieldKey: "email"},
			{Type: "", FieldKey: "email"},
		},
	}
	state.validateIdentityProfileBindingLifecycle("lifecycle.blank-proof", extension)

	extension.BindingLifecycle = profilebindingmodel.Lifecycle{AllowUnbound: true}
	state.validateIdentityProfileBindingLifecycle("lifecycle.missing-proof", extension)

	extension.BindingLifecycle = profilebindingmodel.Lifecycle{
		ClaimProofs: []profilebindingmodel.ClaimProof{{Type: "email", FieldKey: "email"}},
	}
	state.validateIdentityProfileBindingLifecycle("lifecycle.proof-requires-unbound", extension)
}
