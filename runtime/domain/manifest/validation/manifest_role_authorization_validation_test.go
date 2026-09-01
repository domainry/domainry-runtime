package validation

import (
	"strings"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestManifestActionGrantValidatesRoleDataPoliciesWithoutASecondRoleAllowlist(t *testing.T) {
	manifest := manifestmodel.ManifestSchema{
		Objects: []definitionmodel.ObjectSchema{{Key: "booking"}},
		Roles:   []manifestmodel.RoleSchema{{Key: "member", Name: "Member", Permissions: []string{"booking.book"}, RecordScope: "custom", DataPermissions: []manifestmodel.RoleDataPermission{{ObjectKey: "booking", Scope: "custom", Read: true, Write: true}}}},
		Actions: []definitionmodel.ActionSchema{{Key: "booking.book", ObjectKey: "booking", EffectSet: &definitionmodel.ActionEffectSet{Read: []definitionmodel.ActionObjectEffect{{ObjectKey: "booking"}}, Write: []definitionmodel.ActionObjectEffect{{ObjectKey: "booking"}}}}},
	}
	state := newValidationState(manifest, nil)
	state.validateObjects()
	state.validateRoles()
	state.validateActions()
	if len(state.errs) != 0 {
		t.Fatalf("valid role authorization rejected: %v", state.errs)
	}

	manifest.Roles[0].DataPermissions[0].Write = false
	state = newValidationState(manifest, nil)
	state.validateObjects()
	state.validateRoles()
	state.validateActions()
	if !strings.Contains(state.errs.Error(), `lacks write data permission`) {
		t.Fatalf("RoleSchema Action grant without required data permission was not rejected: %v", state.errs)
	}
}

func TestManifestRolePoliciesResolveAgainstCurrentRuntimeObjectSchema(t *testing.T) {
	valid := func() manifestmodel.ManifestSchema {
		return manifestmodel.ManifestSchema{
			Objects: []definitionmodel.ObjectSchema{
				{Key: "booking", Fields: []definitionmodel.FieldSchema{
					{Key: "member_id", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "member"}},
					{Key: "amount", Type: "number"},
					{Key: "retired", Type: "text", DisabledAt: "2026-09-02T00:00:00Z"},
				}},
				{Key: "member", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}},
			},
			Roles: []manifestmodel.RoleSchema{{
				Key: "member", Name: "Member", Permissions: []string{"booking.read"}, RecordScope: "custom",
				DataPermissions: []manifestmodel.RoleDataPermission{{
					ObjectKey: "booking", Scope: "custom", Read: true,
					Predicate: &manifestmodel.RolePolicyExpression{
						Operator: "eq", Path: []manifestmodel.RolePolicyRelationSegment{{Direction: "forward", RelationFieldKey: "member_id", TargetObjectKey: "member"}},
						FieldKey: "name", ValueSource: "actor_claim", ClaimKey: "member_name",
					},
				}},
				FieldPermissions:     []manifestmodel.RoleFieldPermission{{ObjectKey: "booking", FieldKey: "amount", Read: true, Export: true}},
				ReferencePermissions: []manifestmodel.RoleReferencePermission{{SourceObjectKey: "booking", RelationFieldKey: "member_id", TargetObjectKey: "member", DisplayFields: []string{"id", "name"}}},
				ExportRules:          []manifestmodel.RoleExportRule{{ObjectKey: "booking", Mode: "allow_list", Fields: []string{"id", "amount"}}},
			}},
		}
	}
	validateRoles := func(manifest manifestmodel.ManifestSchema) error {
		state := newValidationState(manifest, nil)
		state.validateObjects()
		state.validateRoles()
		if len(state.errs) == 0 {
			return nil
		}
		return state.errs
	}
	if err := validateRoles(valid()); err != nil {
		t.Fatalf("valid Runtime role policies rejected: %v", err)
	}

	tests := []struct {
		name string
		edit func(*manifestmodel.ManifestSchema)
		want string
	}{
		{name: "predicate field", edit: func(value *manifestmodel.ManifestSchema) {
			value.Roles[0].DataPermissions[0].Predicate.FieldKey = "missing"
		}, want: `unknown or disabled field "missing" on object "member"`},
		{name: "predicate relation", edit: func(value *manifestmodel.ManifestSchema) {
			value.Roles[0].DataPermissions[0].Predicate.Path[0].RelationFieldKey = "amount"
		}, want: "invalid forward relation booking.amount -> member"},
		{name: "predicate operator", edit: func(value *manifestmodel.ManifestSchema) {
			value.Roles[0].DataPermissions[0].Predicate.Operator = "contains"
		}, want: `is not executable by Runtime: "contains"`},
		{name: "field", edit: func(value *manifestmodel.ManifestSchema) { value.Roles[0].FieldPermissions[0].FieldKey = "retired" }, want: `unknown or disabled field "retired" on object "booking"`},
		{name: "reference relation", edit: func(value *manifestmodel.ManifestSchema) {
			value.Roles[0].ReferencePermissions[0].RelationFieldKey = "amount"
		}, want: "is not a relation"},
		{name: "reference display", edit: func(value *manifestmodel.ManifestSchema) {
			value.Roles[0].ReferencePermissions[0].DisplayFields = []string{"missing"}
		}, want: `unknown or disabled field "missing" on object "member"`},
		{name: "export", edit: func(value *manifestmodel.ManifestSchema) { value.Roles[0].ExportRules[0].Fields = []string{"retired"} }, want: `unknown or disabled field "retired" on object "booking"`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			manifest := valid()
			test.edit(&manifest)
			if err := validateRoles(manifest); err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v want substring %q", err, test.want)
			}
		})
	}
}
