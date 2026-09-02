package runtime

import (
	"context"
	"encoding/json"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestRuntimeProjectRoleCatalogPreservesExternalAssignmentSafetyFacts(t *testing.T) {
	roles := []manifestmodel.RoleSchema{
		{
			Key: "member_onboarding", Name: "Member onboarding", Permissions: []string{"course.read"}, RecordScope: "all_records", Audience: "any", AssignmentMode: "manual", RiskLevel: "normal",
			DataPermissions:  []manifestmodel.RoleDataPermission{{ObjectKey: "course", Scope: "all_records", Read: true}},
			FieldPermissions: []manifestmodel.RoleFieldPermission{{ObjectKey: "course", FieldKey: "name", Read: true}},
		},
		{Key: "operator", Name: "Operator", Permissions: []string{"runtime.appschema.validate_application_definition"}, Audience: "any", AssignmentMode: "manual", RiskLevel: "privileged"},
		{Key: "member", Name: "Member", Audience: "business", RequiredBindingKey: "member", AssignmentMode: "system_managed", RiskLevel: "normal"},
	}

	catalog := runtimeProjectRoleCatalog(roles, " workspace-primary ", " runtime ")
	if catalog.Application.WorkspaceID != "workspace-primary" || catalog.Application.ApplicationKey != "runtime" || len(catalog.Roles) != 3 {
		t.Fatalf("catalog = %#v", catalog)
	}
	for index, want := range roles {
		got := catalog.Roles[index]
		if got.Key != want.Key || got.AssignmentMode != want.AssignmentMode || got.RiskLevel != want.RiskLevel || got.Audience != want.Audience || got.RequiredBindingKey != want.RequiredBindingKey {
			t.Fatalf("role[%d] safety facts = %#v, want %#v", index, got, want)
		}
		if got.SchemaHash == "" {
			t.Fatalf("role[%d] has no schema hash", index)
		}
	}
	if catalog.Roles[1].Permissions[0] != "runtime.appschema.validate_application_definition" {
		t.Fatalf("privileged permission was weakened: %#v", catalog.Roles[1])
	}
	if len(catalog.Roles[0].Permissions) != 1 || len(catalog.Roles[1].Permissions) != 1 {
		t.Fatalf("role publication invented permissions: %#v", catalog.Roles)
	}
	var dataPermissions []manifestmodel.RoleDataPermission
	if err := json.Unmarshal(catalog.Roles[0].DataPermissions, &dataPermissions); err != nil || len(dataPermissions) != 1 || dataPermissions[0].ObjectKey != "course" || !dataPermissions[0].Read {
		t.Fatalf("data permissions were not preserved: %#v, err=%v", dataPermissions, err)
	}
	var fieldPermissions []manifestmodel.RoleFieldPermission
	if err := json.Unmarshal(catalog.Roles[0].FieldPermissions, &fieldPermissions); err != nil || len(fieldPermissions) != 1 || fieldPermissions[0].FieldKey != "name" || !fieldPermissions[0].Read {
		t.Fatalf("field permissions were not preserved: %#v, err=%v", fieldPermissions, err)
	}
}

func TestRuntimeRolePermissionsKeepsOnlyExactDeclaredKeys(t *testing.T) {
	permissions := runtimeRolePermissions([]string{" customer.read ", "customer.read", "runtime.appschema.validate_application_definition"})
	if len(permissions) != 2 || permissions[0] != "customer.read" || permissions[1] != "runtime.appschema.validate_application_definition" {
		t.Fatalf("permissions=%#v", permissions)
	}
}

func TestPublishRuntimeProjectRolesUsesOptionalBindingCapability(t *testing.T) {
	binding := &runtimeProjectRolePublisherBinding{runtimeIdentityBindingStub: runtimeIdentityBindingStub{}}
	err := publishRuntimeProjectRoles(t.Context(), binding, []manifestmodel.RoleSchema{{Key: "member_onboarding", Name: "Member onboarding", Audience: "any", AssignmentMode: "manual", RiskLevel: "normal"}}, "workspace-primary", "runtime")
	if err != nil {
		t.Fatal(err)
	}
	if len(binding.catalog.Roles) != 1 || binding.catalog.Roles[0].Key != "member_onboarding" {
		t.Fatalf("published catalog = %#v", binding.catalog)
	}
	if err := publishRuntimeProjectRoles(t.Context(), &binding.runtimeIdentityBindingStub, nil, "workspace-primary", "runtime"); err != nil {
		t.Fatalf("binding without optional publisher failed: %v", err)
	}
}

type runtimeProjectRolePublisherBinding struct {
	runtimeIdentityBindingStub
	catalog identitysdk.ProjectRoleCatalog
}

func (binding *runtimeProjectRolePublisherBinding) PublishProjectRoles(_ context.Context, catalog identitysdk.ProjectRoleCatalog) (identitysdk.ProjectRoleCatalogReceipt, error) {
	binding.catalog = catalog
	return identitysdk.ProjectRoleCatalogReceipt{Published: len(catalog.Roles)}, nil
}
