package validation

import (
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"

	"testing"
)

func TestReviewManifestUpdateTracksProfileExtensionAddFieldAndTabEvolution(t *testing.T) {
	previous := profileReviewManifest()
	next := cloneManifestForReviewTest(t, previous)
	next.Objects[0].Fields = append(next.Objects[0].Fields, definitionmodel.FieldSchema{Key: "license_no", Name: "License", Type: "text"})
	next.IdentityProfileExtensions[0].ProfileTabs = append(next.IdentityProfileExtensions[0].ProfileTabs, "credentials")
	next.IdentityProfileExtensions[0].ProfileTabLabels["credentials"] = "Credentials"
	next.IdentityProfileExtensions[0].ProfileTabFields["credentials"] = []string{"license_no"}

	result := ReviewManifestUpdate(previous, next, ReviewOptions{})
	if result.HasBlockers() {
		t.Fatalf("additive Profile evolution must not require destructive approval: %#v", result.Blockers)
	}
	for _, kind := range []string{"add_field", "add_identity_profile_tab", "change_identity_profile_tab_contract"} {
		if !reviewChangesContainKind(result.Changes, kind) {
			t.Fatalf("additive Profile evolution omitted %s: %#v", kind, result.Changes)
		}
	}

	withoutExtension := cloneManifestForReviewTest(t, previous)
	withoutExtension.IdentityProfileExtensions = nil
	added := ReviewManifestUpdate(withoutExtension, previous, ReviewOptions{})
	if added.HasBlockers() || !reviewChangesContainKind(added.Changes, "add_identity_profile_extension") {
		t.Fatalf("adding a valid Profile Extension must be an auditable additive change: %#v", added)
	}
}

func TestReviewManifestUpdateProtectsProfileBindingTabsAndDeletion(t *testing.T) {
	previous := profileReviewManifest()
	tests := []struct {
		name string
		next func(manifestmodel.ManifestSchema) manifestmodel.ManifestSchema
		kind string
	}{
		{
			name: "remove extension",
			next: func(next manifestmodel.ManifestSchema) manifestmodel.ManifestSchema {
				next.IdentityProfileExtensions = nil
				return next
			},
			kind: "remove_identity_profile_extension",
		},
		{
			name: "remove tab",
			next: func(next manifestmodel.ManifestSchema) manifestmodel.ManifestSchema {
				next.IdentityProfileExtensions[0].ProfileTabs = nil
				return next
			},
			kind: "remove_identity_profile_tab",
		},
		{
			name: "change relation binding",
			next: func(next manifestmodel.ManifestSchema) manifestmodel.ManifestSchema {
				next.IdentityProfileExtensions[0].IdentityRelationField = "manager"
				return next
			},
			kind: "change_identity_profile_binding",
		},
		{
			name: "remove required permission",
			next: func(next manifestmodel.ManifestSchema) manifestmodel.ManifestSchema {
				next.IdentityProfileExtensions[0].RequiredPermissions = nil
				return next
			},
			kind: "widen_identity_profile_visibility",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			next := test.next(cloneManifestForReviewTest(t, previous))
			result := ReviewManifestUpdate(previous, next, ReviewOptions{})
			if !result.HasBlockers() || !reviewChangesContainKind(result.Blockers, test.kind) {
				t.Fatalf("Profile destructive boundary omitted %s: %#v", test.kind, result)
			}
			approved := ReviewManifestUpdate(previous, next, ReviewOptions{ApproveDestructive: true})
			if approved.HasBlockers() || !reviewChangesContainKind(approved.Changes, test.kind) {
				t.Fatalf("approved Profile change lost audit evidence for %s: %#v", test.kind, approved)
			}
		})
	}
}

func profileReviewManifest() manifestmodel.ManifestSchema {
	return manifestmodel.ManifestSchema{
		TemplateID: "profile-review", Version: "1",
		Objects: []definitionmodel.ObjectSchema{{Key: "employee_profile", Fields: []definitionmodel.FieldSchema{
			{Key: "identity_user", Type: "relation", Required: true, Unique: true, Config: map[string]any{"object_key": "identity_user"}},
			{Key: "legal_entity", Type: "text"},
		}}},
		IdentityProfileExtensions: []profilebindingmodel.Binding{{
			ContractVersion: profilebindingmodel.ContractVersion, MinReaderVersion: profilebindingmodel.MinimumReaderVersion,
			ObjectKey: "employee_profile", IdentityRelationField: "identity_user", Cardinality: "one_to_one",
			SummaryFields: []string{"legal_entity"}, ProfileTabs: []string{"employment"},
			ProfileTabLabels: map[string]string{"employment": "Employment"}, ProfileTabFields: map[string][]string{"employment": {"legal_entity"}},
			ProfileTabRelatedObjects: map[string][]string{}, ProfileTabComponents: map[string][]string{},
			DefaultVisibility: "when_readable", RequiredPermissions: []string{"employee_profile.read"},
		}},
	}
}

func reviewChangesContainKind(changes []ReviewChange, kind string) bool {
	for _, change := range changes {
		if change.Kind == kind {
			return true
		}
	}
	return false
}
