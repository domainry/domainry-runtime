package runtime

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestRuntimeWorkspaceRoleCatalogIsExactFourAndIdenticalAcrossLifecycle(t *testing.T) {
	roles := runtimeWorkspaceRolesForTest()
	objects := []definitionmodel.ObjectSchema{{Key: "course", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}}
	bootstrapCatalog, err := RuntimeWorkspaceBootstrapRoleCatalog(objects, roles, "runtime")
	if err != nil {
		t.Fatal(err)
	}
	boundCatalog, err := RuntimeWorkspaceProjectRoleCatalog(objects, roles, "workspace-primary", "runtime")
	if err != nil {
		t.Fatal(err)
	}
	if bootstrapCatalog.Application.WorkspaceID != "" || boundCatalog.Application.WorkspaceID != "workspace-primary" {
		t.Fatalf("catalog scopes bootstrap=%#v bound=%#v", bootstrapCatalog.Application, boundCatalog.Application)
	}
	if !reflect.DeepEqual(bootstrapCatalog.Objects, boundCatalog.Objects) || !reflect.DeepEqual(bootstrapCatalog.Roles, boundCatalog.Roles) {
		t.Fatalf("bootstrap and bound policy differ:\nbootstrap=%#v\nbound=%#v", bootstrapCatalog, boundCatalog)
	}
	keys := make([]string, 0, len(boundCatalog.Roles))
	for _, role := range boundCatalog.Roles {
		keys = append(keys, role.Key)
		if role.Key == "admin" {
			t.Fatal("legacy admin role was synthesized")
		}
	}
	if !slices.Equal(keys, runtimeWorkspaceRoleKeys[:]) {
		t.Fatalf("published role keys=%v want=%v", keys, runtimeWorkspaceRoleKeys)
	}
	headquarters := boundCatalog.Roles[1]
	if len(headquarters.Permissions) != 1 || headquarters.Permissions[0].PermissionKey != "course.read" || headquarters.Permissions[0].DataScope != identitysdk.DataScopeOrgChild {
		t.Fatalf("headquarters permissions=%#v", headquarters.Permissions)
	}
	var fieldPermissions []manifestmodel.RoleFieldPermission
	if err := json.Unmarshal(headquarters.FieldPermissions, &fieldPermissions); err != nil || !reflect.DeepEqual(fieldPermissions, roles[0].FieldPermissions) {
		t.Fatalf("headquarters field permissions=%#v err=%v", fieldPermissions, err)
	}
}

func TestRuntimeWorkspaceRoleCatalogRejectsMissingDuplicateAndAdditionalRoles(t *testing.T) {
	valid := runtimeWorkspaceRolesForTest()
	for name, roles := range map[string][]manifestmodel.RoleSchema{
		"missing":    append([]manifestmodel.RoleSchema(nil), valid[:3]...),
		"duplicate":  append(append([]manifestmodel.RoleSchema(nil), valid...), valid[0]),
		"additional": append(append([]manifestmodel.RoleSchema(nil), valid...), manifestmodel.RoleSchema{Key: "admin", Name: "Admin"}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := RuntimeWorkspaceBootstrapRoleCatalog(nil, roles, "runtime"); err == nil {
				t.Fatalf("invalid roles accepted: %#v", roles)
			}
		})
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
	catalog := RuntimeProjectRoleCatalog(objects, roles, " workspace-primary ", " runtime ")
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
	err := publishRuntimeProjectRoles(t.Context(), binding, []definitionmodel.ObjectSchema{{Key: "member", Fields: []definitionmodel.FieldSchema{{Key: "name"}}}}, runtimeWorkspaceRolesForTest(), "workspace-primary", "runtime")
	if err != nil {
		t.Fatal(err)
	}
	if len(binding.catalog.Roles) != len(runtimeWorkspaceRoleKeys) || binding.catalog.Roles[0].Key != identitysdk.WorkspaceBootstrapRoleTenantAdmin {
		t.Fatalf("published catalog = %#v", binding.catalog)
	}
	if len(binding.catalog.Objects) == 0 {
		t.Fatal("published catalog has no application objects")
	}
	if err := publishRuntimeProjectRoles(t.Context(), &binding.runtimeIdentityBindingStub, nil, nil, "workspace-primary", "runtime"); err != nil {
		t.Fatalf("binding without optional publisher failed: %v", err)
	}
}

func runtimeWorkspaceRolesForTest() []manifestmodel.RoleSchema {
	return []manifestmodel.RoleSchema{
		{
			Key: identitysdk.WorkspaceBootstrapRoleHeadquartersAdmin, Name: "Headquarters administrator",
			Permissions:          []manifestmodel.RolePermission{{PermissionKey: "course.read", DataScope: identitysdk.DataScopeOrgChild, AuditDenial: true}},
			FieldPermissions:     []manifestmodel.RoleFieldPermission{{ObjectKey: "course", FieldKey: "name", Read: true, Write: true}},
			ReferencePermissions: []manifestmodel.RoleReferencePermission{{SourceObjectKey: "course", RelationFieldKey: "owner", TargetObjectKey: "identity_user", DisplayFields: []string{"name"}}},
			ExportRules:          []manifestmodel.RoleExportRule{{ObjectKey: "course", Mode: "selected_fields", Fields: []string{"name"}}},
			Audience:             "user", AssignmentMode: "manual", RiskLevel: "privileged", GrantableRoleKeys: []string{identitysdk.WorkspaceBootstrapRoleStoreManager},
		},
		{Key: identitysdk.WorkspaceBootstrapRoleStaff, Name: "Staff", Permissions: []manifestmodel.RolePermission{{PermissionKey: "course.read", DataScope: identitysdk.DataScopeOwner}}, Audience: "user", AssignmentMode: "manual"},
		{Key: identitysdk.WorkspaceBootstrapRoleTenantAdmin, Name: "Platform administrator", Permissions: []manifestmodel.RolePermission{{PermissionKey: "runtime.admin", DataScope: identitysdk.DataScopeAll}}, Audience: "user", AssignmentMode: "manual", RiskLevel: "privileged"},
		{Key: identitysdk.WorkspaceBootstrapRoleStoreManager, Name: "Store manager", Permissions: []manifestmodel.RolePermission{{PermissionKey: "course.read", DataScope: identitysdk.DataScopeOrg}}, Audience: "user", AssignmentMode: "manual"},
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
