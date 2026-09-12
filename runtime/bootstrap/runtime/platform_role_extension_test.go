package runtime

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func installationGrantFixture() manifestmodel.RoleSchema {
	return manifestmodel.RoleSchema{
		Key: "tenant_admin", Name: "Installation administrator", PlatformRoleExtension: true,
		Audience: "user", AssignmentMode: "system_managed", RiskLevel: "privileged",
		Permissions: []manifestmodel.RolePermission{{PermissionKey: "invoice.usage", DataScope: identitysdk.DataScopeAll}},
	}
}

func TestInstallationGrantExtensionClosesBusinessCapabilitiesAndStaysInstallationOnly(t *testing.T) {
	roles := append(runtimeWorkspaceRolesForTest(), installationGrantFixture())
	descriptor := runtimeRoleCapabilityDescriptor("invoice.usage", func(value *runtimeext.HandlerDescriptor) {
		value.WorkspaceIdentityUsage = &runtimeext.WorkspaceIdentityUsageCapability{MaxPageSize: 25}
	})
	installed, err := RuntimeInstallationWorkspaceProjectRoleCatalog(nil, roles, "installation", "app", descriptor)
	if err != nil {
		t.Fatal(err)
	}
	admin, found := projectRoleByKey(installed.Roles, "tenant_admin")
	if !found || admin.AssignmentMode != "system_managed" || admin.ProvisionToWorkspaces {
		t.Fatalf("unprotected installation role: %+v", admin)
	}
	for _, key := range []string{"invoice.usage", identitysdk.WorkspaceIdentityUsageAggregatePermission, "runtime.workspaceprovision.list_workspaces"} {
		grant, exists := projectRolePermissionByKey(admin.Permissions, key)
		if !exists || grant.DataScope != identitysdk.DataScopeAll {
			t.Fatalf("missing exact grant %s: %+v", key, admin)
		}
	}
	ordinary, err := RuntimeWorkspaceProjectRoleCatalog(nil, roles, "customer", "app", descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := projectRoleByKey(ordinary.Roles, "tenant_admin"); exists {
		t.Fatal("installation grants escaped to ordinary Workspace")
	}
	bootstrap, err := RuntimeWorkspaceBootstrapRoleCatalog(nil, roles, "crm_acceptance_admin", "app", descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := projectRoleByKey(bootstrap.Roles, "tenant_admin"); exists {
		t.Fatal("installation grants escaped to new Workspace bootstrap")
	}
	initial, err := RuntimeInstallationWorkspaceBootstrapRoleCatalog(nil, roles, "crm_acceptance_admin", "app", descriptor)
	if err != nil {
		t.Fatal(err)
	}
	if _, exists := projectRoleByKey(initial.Roles, "tenant_admin"); !exists {
		t.Fatal("initial installation lost protected role")
	}
	// Removing the project extension must remove its business and derived grants,
	// while preserving the platform role and its stable identity.
	removed, err := RuntimeInstallationWorkspaceProjectRoleCatalog(nil, runtimeWorkspaceRolesForTest(), "installation", "app", descriptor)
	if err != nil {
		t.Fatal(err)
	}
	admin, found = projectRoleByKey(removed.Roles, "tenant_admin")
	if !found {
		t.Fatal("removing extension removed platform identity")
	}
	if _, exists := projectRolePermissionByKey(admin.Permissions, "invoice.usage"); exists {
		t.Fatal("removed grant persisted")
	}
	if _, exists := projectRolePermissionByKey(admin.Permissions, identitysdk.WorkspaceIdentityUsageAggregatePermission); exists {
		t.Fatal("removed downstream grant persisted")
	}
}

func TestInstallationGrantExtensionCannotRedefineIdentity(t *testing.T) {
	for _, change := range []struct {
		name string
		edit func(*manifestmodel.RoleSchema)
	}{
		{"assignment", func(r *manifestmodel.RoleSchema) { r.AssignmentMode = "manual" }},
		{"provision", func(r *manifestmodel.RoleSchema) { r.ProvisionToWorkspaces = true }},
		{"grantable", func(r *manifestmodel.RoleSchema) { r.GrantableRoleKeys = []string{"tenant_admin"} }},
		{"unknown", func(r *manifestmodel.RoleSchema) { r.Key = "unknown_admin" }},
		{"unmarked", func(r *manifestmodel.RoleSchema) { r.PlatformRoleExtension = false }},
	} {
		t.Run(change.name, func(t *testing.T) {
			role := installationGrantFixture()
			change.edit(&role)
			if _, err := RuntimeInstallationWorkspaceProjectRoleCatalog(nil, []manifestmodel.RoleSchema{role}, "installation", "app"); err == nil {
				t.Fatal("accepted identity override")
			}
		})
	}
}
