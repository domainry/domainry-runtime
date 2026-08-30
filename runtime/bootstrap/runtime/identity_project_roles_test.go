package runtime

import (
	"context"
	"encoding/json"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestRuntimeProjectRoleCatalogPreservesExternalAssignmentSafetyFacts(t *testing.T) {
	roles := []manifestmodel.RoleSchema{
		{
			Key: "member_onboarding", Name: "Member onboarding", Permissions: []string{"course.read"}, RecordScope: "all_records", Audience: "any", AssignmentMode: "manual", RiskLevel: "normal",
			DataPermissions:  []manifestmodel.RoleDataPermission{{ObjectKey: "course", Scope: "all_records", Read: true}},
			FieldPermissions: []manifestmodel.RoleFieldPermission{{ObjectKey: "course", FieldKey: "name", Read: true}},
		},
		{Key: "operator", Name: "Operator", Permissions: []string{"workspace.admin"}, Audience: "any", AssignmentMode: "manual", RiskLevel: "privileged"},
		{Key: "member", Name: "Member", Audience: "business", RequiredBindingKey: "member", AssignmentMode: "system_managed", RiskLevel: "normal"},
	}

	catalog := runtimeProjectRoleCatalog(roles, " default ", " runtime ")
	if catalog.Application.WorkspaceID != "default" || catalog.Application.ApplicationKey != "runtime" || len(catalog.Roles) != 3 {
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
	if catalog.Roles[1].Permissions[0] != "workspace.admin" {
		t.Fatalf("privileged permission was weakened: %#v", catalog.Roles[1])
	}
	for _, test := range []struct {
		role       int
		permission string
	}{
		{role: 0, permission: "notification_inbox.read"},
		{role: 0, permission: "notification_preference.update"},
		{role: 1, permission: "notification_template.*"},
		{role: 1, permission: "notification_team_mailbox.read"},
	} {
		if !containsRuntimeRolePermission(catalog.Roles[test.role].Permissions, test.permission) {
			t.Fatalf("role[%d] missing derived Notification permission %q: %#v", test.role, test.permission, catalog.Roles[test.role].Permissions)
		}
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

func TestRuntimeRolePermissionsMapsLegacyNotificationAdministration(t *testing.T) {
	permissions := runtimeRolePermissions([]string{
		"notification.template.read", "notification.template.manage", "notification.template.test",
		"notification.template.publish", "notification.template.approve", "notification.policy.read", "notification.policy.manage",
	})
	for _, expected := range []string{
		"notification_template.read", "notification_template.draft", "notification_template.disable", "notification_template.preview",
		"notification_publication.read", "notification_publication.request", "notification_publication.cancel", "notification_publication.approve", "notification_publication.reject",
		"notification_delivery_policy.read", "notification_delivery_policy.update", "notification_governance.read",
	} {
		if !containsRuntimeRolePermission(permissions, expected) {
			t.Fatalf("missing mapped permission %q: %#v", expected, permissions)
		}
	}
}

func TestRuntimeIdentityCatalogIncludesEmbeddedNotificationAuthorization(t *testing.T) {
	catalog := runtimeIdentityCatalog(appschemamodel.ApplicationSchemaSnapshot{}, "workspace", "runtime", nil)
	resources := map[string]identitysdk.ResourceDefinition{}
	for _, resource := range catalog.Resources {
		resources[string(resource.Key)] = resource
	}
	for _, key := range []string{"notification_inbox", "notification_template", "notification_publication", "notification_delivery_policy", "notification_preference", "notification_team_mailbox", "notification_delegation", "notification_governance"} {
		resource, ok := resources[key]
		if !ok || len(resource.SupportedFacts) != 3 {
			t.Fatalf("missing Notification resource %q: %#v", key, resource)
		}
	}
	actions := map[string]bool{}
	for _, action := range catalog.Actions {
		actions[string(action.Resource)+"."+string(action.Action)] = true
	}
	for _, permission := range []string{"notification_inbox.act", "notification_template.draft", "notification_publication.approve", "notification_delivery_policy.update", "notification_delegation.delete"} {
		if !actions[permission] {
			t.Fatalf("missing Notification action %q", permission)
		}
	}
}

func containsRuntimeRolePermission(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}

func TestPublishRuntimeProjectRolesUsesOptionalBindingCapability(t *testing.T) {
	binding := &runtimeProjectRolePublisherBinding{runtimeIdentityBindingStub: runtimeIdentityBindingStub{}}
	err := publishRuntimeProjectRoles(t.Context(), binding, []manifestmodel.RoleSchema{{Key: "member_onboarding", Name: "Member onboarding", Audience: "any", AssignmentMode: "manual", RiskLevel: "normal"}}, "default", "runtime")
	if err != nil {
		t.Fatal(err)
	}
	if len(binding.catalog.Roles) != 1 || binding.catalog.Roles[0].Key != "member_onboarding" {
		t.Fatalf("published catalog = %#v", binding.catalog)
	}
	if err := publishRuntimeProjectRoles(t.Context(), &binding.runtimeIdentityBindingStub, nil, "default", "runtime"); err != nil {
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
