package runtime

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	actioncontract "github.com/domainry/domainry-foundation/action"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestRuntimeProjectRolesAddsExplicitBootstrapAdministratorFromCompleteRegistry(t *testing.T) {
	original := []manifestmodel.RoleSchema{{Key: "operator", Name: "Operator"}}
	roles := runtimeProjectRolesWithBootstrapAdministrator(original, []actioncontract.PermissionDefinition{
		{Key: "orders.read"},
		{Key: "scheduler.definitions.list"},
	})
	if len(original) != 1 || len(roles) != 2 {
		t.Fatalf("roles=%+v original=%+v", roles, original)
	}
	admin := roles[1]
	if admin.Key != "admin" || admin.RiskLevel != "privileged" || !slices.Equal(admin.GrantableRoleKeys, []string{"*"}) {
		t.Fatalf("admin=%+v", admin)
	}
	keys := make([]string, 0, len(admin.Permissions))
	for _, permission := range admin.Permissions {
		if permission.DataScope != identitysdk.DataScopeAll {
			t.Fatalf("permission=%+v", permission)
		}
		keys = append(keys, permission.PermissionKey)
	}
	if !slices.Equal(keys, []string{"orders.read", "scheduler.definitions.list"}) {
		t.Fatalf("permissions=%v", keys)
	}
}

func TestRuntimeProjectRolesHonorsExplicitApplicationAdmin(t *testing.T) {
	explicit := manifestmodel.RoleSchema{Key: " admin ", Name: "Restricted admin", Permissions: []manifestmodel.RolePermission{{PermissionKey: "orders.read", DataScope: identitysdk.DataScopeOwner}}}
	roles := runtimeProjectRolesWithBootstrapAdministrator([]manifestmodel.RoleSchema{explicit}, []actioncontract.PermissionDefinition{{Key: "orders.read"}})
	if len(roles) != 1 || roles[0].Name != explicit.Name || !slices.Equal(roles[0].Permissions, explicit.Permissions) {
		t.Fatalf("explicit admin was changed: %+v", roles)
	}
}

func TestRuntimeProjectRoleCatalogPreservesExternalAssignmentSafetyFacts(t *testing.T) {
	roles := []manifestmodel.RoleSchema{
		{
			Key: "member_onboarding", Name: "Member onboarding", Permissions: []manifestmodel.RolePermission{{PermissionKey: "course.read", DataScope: identitysdk.DataScopeAll}}, Audience: "any", AssignmentMode: "manual", RiskLevel: "normal",
			FieldPermissions: []manifestmodel.RoleFieldPermission{{ObjectKey: "course", FieldKey: "name", Read: true}},
		},
		{Key: "operator", Name: "Operator", Permissions: []manifestmodel.RolePermission{{PermissionKey: "runtime.appschema.validate_application_definition", DataScope: identitysdk.DataScopeAll}}, Audience: "any", AssignmentMode: "manual", RiskLevel: "privileged"},
		{Key: "member", Name: "Member", Audience: "business", RequiredBindingKey: "member", AssignmentMode: "system_managed", RiskLevel: "normal"},
	}

	objects := []definitionmodel.ObjectSchema{{Key: "course", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}}
	catalog := runtimeProjectRoleCatalog(objects, roles, " workspace-primary ", " runtime ")
	if catalog.Application.WorkspaceID != "workspace-primary" || catalog.Application.ApplicationKey != "runtime" || len(catalog.Roles) != 3 {
		t.Fatalf("catalog = %#v", catalog)
	}
	var publishedObjects []definitionmodel.ObjectSchema
	if err := json.Unmarshal(catalog.Objects, &publishedObjects); err != nil || len(publishedObjects) != 1 || publishedObjects[0].Key != "course" || len(publishedObjects[0].Fields) != 1 || publishedObjects[0].Fields[0].Key != "name" {
		t.Fatalf("application objects were not preserved: %#v, err=%v", publishedObjects, err)
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
	if catalog.Roles[1].Permissions[0].PermissionKey != "runtime.appschema.validate_application_definition" || catalog.Roles[1].Permissions[0].DataScope != identitysdk.DataScopeAll {
		t.Fatalf("privileged permission was weakened: %#v", catalog.Roles[1])
	}
	if len(catalog.Roles[0].Permissions) != 1 || len(catalog.Roles[1].Permissions) != 1 {
		t.Fatalf("role publication invented permissions: %#v", catalog.Roles)
	}
	var fieldPermissions []manifestmodel.RoleFieldPermission
	if err := json.Unmarshal(catalog.Roles[0].FieldPermissions, &fieldPermissions); err != nil || len(fieldPermissions) != 1 || fieldPermissions[0].FieldKey != "name" || !fieldPermissions[0].Read {
		t.Fatalf("field permissions were not preserved: %#v, err=%v", fieldPermissions, err)
	}
}

func TestRuntimeRolePermissionsKeepsOnlyExactDeclaredKeys(t *testing.T) {
	permissions := runtimeRolePermissions([]manifestmodel.RolePermission{{PermissionKey: " customer.read ", DataScope: identitysdk.DataScopeOwner}, {PermissionKey: "customer.read", DataScope: identitysdk.DataScopeOrg}, {PermissionKey: "runtime.appschema.validate_application_definition", DataScope: identitysdk.DataScopeAll}})
	if len(permissions) != 2 || permissions[0].PermissionKey != "customer.read" || permissions[0].DataScope != identitysdk.DataScopeOwner || permissions[1].PermissionKey != "runtime.appschema.validate_application_definition" {
		t.Fatalf("permissions=%#v", permissions)
	}
}

func TestPublishRuntimeProjectRolesUsesOptionalBindingCapability(t *testing.T) {
	binding := &runtimeProjectRolePublisherBinding{runtimeIdentityBindingStub: runtimeIdentityBindingStub{}}
	err := publishRuntimeProjectRoles(t.Context(), binding, []definitionmodel.ObjectSchema{{Key: "member", Fields: []definitionmodel.FieldSchema{{Key: "name"}}}}, []manifestmodel.RoleSchema{{Key: "member_onboarding", Name: "Member onboarding", Audience: "any", AssignmentMode: "manual", RiskLevel: "normal"}}, "workspace-primary", "runtime")
	if err != nil {
		t.Fatal(err)
	}
	if len(binding.catalog.Roles) != 1 || binding.catalog.Roles[0].Key != "member_onboarding" {
		t.Fatalf("published catalog = %#v", binding.catalog)
	}
	if len(binding.catalog.Objects) == 0 {
		t.Fatal("published catalog has no application objects")
	}
	if err := publishRuntimeProjectRoles(t.Context(), &binding.runtimeIdentityBindingStub, nil, nil, "workspace-primary", "runtime"); err != nil {
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
