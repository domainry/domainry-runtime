package runtime

import (
	"context"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestM1Baseline26InternalWorkflowRoleCrossesWorkspaceBootstrapBoundary(t *testing.T) {
	raw, err := os.ReadFile("testdata/m1-role-catalog-baseline-26.json")
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := manifestmodel.DecodeManifest(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(manifest.Workflows) != 2 || manifest.Workflows[0].RunAs != "conversion_workflow_service" || manifest.Workflows[1].RunAs != "followup_reminder_service" {
		t.Fatalf("sealed workflow service subject=%#v", manifest.Workflows)
	}
	bootstrap, err := RuntimeWorkspaceBootstrapRoleCatalog(manifest.Objects, manifest.Roles, manifest.InitialWorkspaceAdministratorRole, "runtime")
	if err != nil {
		t.Fatalf("sealed M1 manifest did not cross initial Workspace role gate: %v", err)
	}
	published, err := RuntimeWorkspaceProjectRoleCatalog(manifest.Objects, manifest.Roles, "workspace-primary", "runtime")
	if err != nil {
		t.Fatalf("sealed M1 manifest did not compile for ordinary Identity publication: %v", err)
	}
	bootstrapKeys := projectRoleKeys(bootstrap.Roles)
	if !slices.Equal(bootstrapKeys, []string{"crm_acceptance_admin", "sales_director", "sales_rep"}) {
		t.Fatalf("sealed M1 bootstrap role keys=%v", bootstrapKeys)
	}
	if bootstrap.InitialWorkspaceAdministratorRoleKey != "crm_acceptance_admin" {
		t.Fatalf("sealed M1 initial Workspace administrator role=%q", bootstrap.InitialWorkspaceAdministratorRoleKey)
	}
	publishedKeys := projectRoleKeys(published.Roles)
	if !slices.Equal(publishedKeys, []string{"conversion_workflow_service", "crm_acceptance_admin", "followup_reminder_service", "sales_director", "sales_rep"}) {
		t.Fatalf("sealed M1 role catalogs bootstrap=%#v published=%#v", bootstrap.Roles, published.Roles)
	}
	for _, workflow := range manifest.Workflows {
		service, found := projectRoleByKey(published.Roles, workflow.RunAs)
		if !found || service.Audience != "service" || service.AssignmentMode != "system_managed" || service.ProvisionToWorkspaces || service.SchemaHash == "" {
			t.Fatalf("sealed M1 Workflow service role %q was not published safely: %#v", workflow.RunAs, service)
		}
		if _, provisioned := projectRoleByKey(bootstrap.Roles, workflow.RunAs); provisioned {
			t.Fatalf("sealed M1 Workflow service role %q entered the human bootstrap catalog", workflow.RunAs)
		}
	}
}

func TestRuntimeWorkspaceRoleCatalogSeparatesBootstrapRolesFromCompletePublication(t *testing.T) {
	roles := append(runtimeWorkspaceRolesForTest(), runtimeInternalServiceRoleForTest())
	objects := []definitionmodel.ObjectSchema{{Key: "course", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}}
	bootstrapCatalog, err := RuntimeWorkspaceBootstrapRoleCatalog(objects, roles, "crm_acceptance_admin", "runtime")
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
	if bootstrapCatalog.InitialWorkspaceAdministratorRoleKey != "crm_acceptance_admin" || boundCatalog.InitialWorkspaceAdministratorRoleKey != "" {
		t.Fatalf("catalog administrator policy bootstrap=%q bound=%q", bootstrapCatalog.InitialWorkspaceAdministratorRoleKey, boundCatalog.InitialWorkspaceAdministratorRoleKey)
	}
	if !reflect.DeepEqual(bootstrapCatalog.Objects, boundCatalog.Objects) {
		t.Fatalf("bootstrap and bound objects differ:\nbootstrap=%#v\nbound=%#v", bootstrapCatalog, boundCatalog)
	}
	for _, role := range bootstrapCatalog.Roles {
		if role.Key == "admin" {
			t.Fatal("legacy admin role was synthesized")
		}
	}
	if keys := projectRoleKeys(bootstrapCatalog.Roles); !slices.Equal(keys, []string{"crm_acceptance_admin", "sales_director", "sales_rep"}) {
		t.Fatalf("bootstrap role keys=%v", keys)
	}
	if len(boundCatalog.Roles) != len(roles) || !slices.Equal(projectRoleKeys(boundCatalog.Roles), []string{"conversion_workflow_service", "crm_acceptance_admin", "sales_director", "sales_rep"}) {
		t.Fatalf("complete published roles=%#v", boundCatalog.Roles)
	}
	boundByKey := make(map[string]identitysdk.ProjectRoleDefinition, len(boundCatalog.Roles))
	for _, role := range boundCatalog.Roles {
		boundByKey[role.Key] = role
	}
	for _, role := range bootstrapCatalog.Roles {
		if !reflect.DeepEqual(role, boundByKey[role.Key]) {
			t.Fatalf("shared role %q changed identity across lifecycle: bootstrap=%#v bound=%#v", role.Key, role, boundByKey[role.Key])
		}
	}
	service, found := projectRoleByKey(boundCatalog.Roles, "conversion_workflow_service")
	if !found {
		t.Fatal("complete catalog omitted conversion_workflow_service")
	}
	if service.Audience != "service" || service.AssignmentMode != "system_managed" || service.ProvisionToWorkspaces || service.SchemaHash == "" || len(service.Permissions) != 1 {
		t.Fatalf("internal service role was weakened: %#v", service)
	}
	administrator := boundByKey["crm_acceptance_admin"]
	if len(administrator.Permissions) != 1 || administrator.Permissions[0].PermissionKey != "course.read" || administrator.Permissions[0].DataScope != identitysdk.DataScopeOrgChild {
		t.Fatalf("administrator permissions=%#v", administrator.Permissions)
	}
	var fieldPermissions []manifestmodel.RoleFieldPermission
	if err := json.Unmarshal(administrator.FieldPermissions, &fieldPermissions); err != nil || !reflect.DeepEqual(fieldPermissions, roles[0].FieldPermissions) {
		t.Fatalf("administrator field permissions=%#v err=%v", fieldPermissions, err)
	}
}

func TestRuntimeWorkspaceRoleCatalogRejectsInvalidHumanAndInternalRoles(t *testing.T) {
	valid := runtimeWorkspaceRolesForTest()
	noProvisionedRole := append([]manifestmodel.RoleSchema(nil), valid...)
	for index := range noProvisionedRole {
		noProvisionedRole[index].ProvisionToWorkspaces = false
	}
	emptyKey := append([]manifestmodel.RoleSchema(nil), valid...)
	emptyKey[0].Key = " "
	emptyName := append([]manifestmodel.RoleSchema(nil), valid...)
	emptyName[0].Name = " "
	invalidAudience := append([]manifestmodel.RoleSchema(nil), valid...)
	invalidAudience[0].Audience = "robot"
	invalidAssignment := append([]manifestmodel.RoleSchema(nil), valid...)
	invalidAssignment[0].AssignmentMode = "automatic"
	systemManagedProvisioned := append([]manifestmodel.RoleSchema(nil), valid...)
	systemManagedProvisioned[0].AssignmentMode = "system_managed"
	serviceProvisioned := append(append([]manifestmodel.RoleSchema(nil), valid...), runtimeInternalServiceRoleForTest())
	serviceProvisioned[len(serviceProvisioned)-1].ProvisionToWorkspaces = true
	serviceManual := append(append([]manifestmodel.RoleSchema(nil), valid...), runtimeInternalServiceRoleForTest())
	serviceManual[len(serviceManual)-1].AssignmentMode = "manual"
	for name, roles := range map[string][]manifestmodel.RoleSchema{
		"no provisioned human role":   noProvisionedRole,
		"duplicate":                   append(append([]manifestmodel.RoleSchema(nil), valid...), valid[0]),
		"empty key":                   emptyKey,
		"empty name":                  emptyName,
		"invalid audience":            invalidAudience,
		"invalid assignment":          invalidAssignment,
		"system managed provisioned":  systemManagedProvisioned,
		"service provisioned":         serviceProvisioned,
		"service manually assignable": serviceManual,
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := RuntimeWorkspaceBootstrapRoleCatalog(nil, roles, "crm_acceptance_admin", "runtime"); err == nil {
				t.Fatalf("invalid roles accepted: %#v", roles)
			}
		})
	}
}

func TestRuntimeWorkspaceRoleCatalogAcceptsEveryHumanLoginAudienceAndRequestOnlyAssignment(t *testing.T) {
	roles := runtimeWorkspaceRolesForTest()
	roles[2].AssignmentMode = "request_only"
	catalog, err := RuntimeWorkspaceBootstrapRoleCatalog(nil, roles, "crm_acceptance_admin", "runtime")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(projectRoleKeys(catalog.Roles), []string{"crm_acceptance_admin", "sales_director", "sales_rep"}) {
		t.Fatalf("bootstrap roles=%#v", catalog.Roles)
	}
}

func TestRuntimeWorkspaceBootstrapRoleCatalogRequiresExplicitEligibleAdministrator(t *testing.T) {
	roles := runtimeWorkspaceRolesForTest()
	for name, administrator := range map[string]string{
		"missing":          "",
		"unknown":          "missing",
		"business profile": "sales_rep",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := RuntimeWorkspaceBootstrapRoleCatalog(nil, roles, administrator, "runtime"); err == nil || !strings.Contains(err.Error(), "initial_workspace_administrator_role") {
				t.Fatalf("administrator=%q error=%v", administrator, err)
			}
		})
	}
	requestOnly := append([]manifestmodel.RoleSchema(nil), roles...)
	requestOnly[0].AssignmentMode = "request_only"
	if _, err := RuntimeWorkspaceBootstrapRoleCatalog(nil, requestOnly, "crm_acceptance_admin", "runtime"); err == nil || !strings.Contains(err.Error(), "initial_workspace_administrator_role") {
		t.Fatalf("request-only administrator error=%v", err)
	}
	requiresBinding := append([]manifestmodel.RoleSchema(nil), roles...)
	requiresBinding[0].RequiredBindingKey = "sales_profile"
	if _, err := RuntimeWorkspaceBootstrapRoleCatalog(nil, requiresBinding, "crm_acceptance_admin", "runtime"); err == nil || !strings.Contains(err.Error(), "initial_workspace_administrator_role") {
		t.Fatalf("binding-dependent administrator error=%v", err)
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
	roles := append(runtimeWorkspaceRolesForTest(), runtimeInternalServiceRoleForTest())
	err := publishRuntimeProjectRoles(t.Context(), binding, []definitionmodel.ObjectSchema{{Key: "member", Fields: []definitionmodel.FieldSchema{{Key: "name"}}}}, roles, "workspace-primary", "runtime")
	if err != nil {
		t.Fatal(err)
	}
	if len(binding.catalog.Roles) != len(roles) {
		t.Fatalf("published catalog = %#v", binding.catalog)
	}
	if _, found := projectRoleByKey(binding.catalog.Roles, "conversion_workflow_service"); !found {
		t.Fatalf("published catalog omitted internal service role: %#v", binding.catalog)
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
			Key: "crm_acceptance_admin", Name: "CRM acceptance administrator",
			Permissions:          []manifestmodel.RolePermission{{PermissionKey: "course.read", DataScope: identitysdk.DataScopeOrgChild, AuditDenial: true}},
			FieldPermissions:     []manifestmodel.RoleFieldPermission{{ObjectKey: "course", FieldKey: "name", Read: true, Write: true}},
			ReferencePermissions: []manifestmodel.RoleReferencePermission{{SourceObjectKey: "course", RelationFieldKey: "owner", TargetObjectKey: "identity_user", DisplayFields: []string{"name"}}},
			ExportRules:          []manifestmodel.RoleExportRule{{ObjectKey: "course", Mode: "selected_fields", Fields: []string{"name"}}},
			Audience:             "any", AssignmentMode: "manual", RiskLevel: "privileged", GrantableRoleKeys: []string{"sales_director"}, ProvisionToWorkspaces: true,
		},
		{Key: "sales_rep", Name: "Sales representative", Permissions: []manifestmodel.RolePermission{{PermissionKey: "course.read", DataScope: identitysdk.DataScopeOwner}}, Audience: "business_profile", RequiredBindingKey: "sales_profile", AssignmentMode: "manual", ProvisionToWorkspaces: true},
		{Key: "sales_director", Name: "Sales director", Permissions: []manifestmodel.RolePermission{{PermissionKey: "course.read", DataScope: identitysdk.DataScopeOrg}}, Audience: "user", AssignmentMode: "manual", ProvisionToWorkspaces: true},
	}
}

func runtimeInternalServiceRoleForTest() manifestmodel.RoleSchema {
	return manifestmodel.RoleSchema{
		Key: "conversion_workflow_service", Name: "Conversion Workflow Service", Audience: "service", AssignmentMode: "system_managed",
		Permissions: []manifestmodel.RolePermission{{PermissionKey: "conversion_request.approve", DataScope: identitysdk.DataScopeAll}},
	}
}

type runtimeProjectRolePublisherBinding struct {
	runtimeIdentityBindingStub
	catalog identitysdk.ProjectRoleCatalog
}

func projectRoleKeys(roles []identitysdk.ProjectRoleDefinition) []string {
	keys := make([]string, 0, len(roles))
	for _, role := range roles {
		keys = append(keys, role.Key)
	}
	return keys
}

func projectRoleByKey(roles []identitysdk.ProjectRoleDefinition, key string) (identitysdk.ProjectRoleDefinition, bool) {
	for _, role := range roles {
		if role.Key == key {
			return role, true
		}
	}
	return identitysdk.ProjectRoleDefinition{}, false
}

func (binding *runtimeProjectRolePublisherBinding) PublishProjectRoles(_ context.Context, catalog identitysdk.ProjectRoleCatalog) (identitysdk.ProjectRoleCatalogReceipt, error) {
	binding.catalog = catalog
	return identitysdk.ProjectRoleCatalogReceipt{Published: len(catalog.Roles)}, nil
}
