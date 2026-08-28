package validation

import (
	"strings"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestManifestRoleDataPermissionAndHandlerRoleReferences(t *testing.T) {
	manifest := manifestmodel.ManifestSchema{
		Objects: []definitionmodel.ObjectSchema{{Key: "booking"}},
		Roles:   []manifestmodel.RoleSchema{{Key: "member", Name: "Member", Permissions: []string{"booking.book"}, RecordScope: "custom", DataPermissions: []manifestmodel.RoleDataPermission{{ObjectKey: "booking", Scope: "custom", Read: true, Write: true}}}},
		Actions: []definitionmodel.ActionSchema{{Key: "booking.book", ObjectKey: "booking", RequiresPermission: "booking.book", Authorization: &definitionmodel.ActionAuthorization{AllowedRoles: []string{"member"}}, EffectSet: &definitionmodel.ActionEffectSet{Read: []definitionmodel.ActionObjectEffect{{ObjectKey: "booking"}}, Write: []definitionmodel.ActionObjectEffect{{ObjectKey: "booking"}}}}},
	}
	state := newValidationState(manifest, nil)
	state.validateObjects()
	state.validateRoles()
	state.validateActions()
	if len(state.errs) != 0 {
		t.Fatalf("valid role authorization rejected: %v", state.errs)
	}

	manifest.Actions[0].Authorization.AllowedRoles = []string{"coach"}
	state = newValidationState(manifest, nil)
	state.validateObjects()
	state.validateRoles()
	state.validateActions()
	if !strings.Contains(state.errs.Error(), `unknown role "coach"`) {
		t.Fatalf("unknown allowed role was not rejected: %v", state.errs)
	}

	manifest.Actions[0].Authorization.AllowedRoles = []string{"member"}
	manifest.Roles[0].DataPermissions[0].Write = false
	state = newValidationState(manifest, nil)
	state.validateObjects()
	state.validateRoles()
	state.validateActions()
	if !strings.Contains(state.errs.Error(), `lacks write data permission`) {
		t.Fatalf("Handler write without role data permission was not rejected: %v", state.errs)
	}
}
