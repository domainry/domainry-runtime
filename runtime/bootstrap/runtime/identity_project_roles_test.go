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
	organizationunit "github.com/domainry/domainry-identity-sdk/organizationunit"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	workspaceprovisionapplication "github.com/domainry/domainry-runtime/runtime/application/workspaceprovision"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestRuntimeInstallationAdministratorRoleIsPlatformOwnedAndProtected(t *testing.T) {
	roles := runtimeWorkspaceRolesForTest()
	bootstrapCatalog, err := RuntimeInstallationWorkspaceBootstrapRoleCatalog(nil, roles, "crm_acceptance_admin", "runtime")
	if err != nil {
		t.Fatal(err)
	}
	bootstrapRole, found := projectRoleByKey(bootstrapCatalog.Roles, workspaceprovisionapplication.WorkspaceAdministratorRoleKey)
	if !found || bootstrapRole.AssignmentMode != "manual" || !bootstrapRole.ProvisionToWorkspaces {
		t.Fatalf("bootstrap installation role=%+v found=%t", bootstrapRole, found)
	}

	projectCatalog, err := RuntimeInstallationWorkspaceProjectRoleCatalog(nil, roles, "workspace-primary", "runtime")
	if err != nil {
		t.Fatal(err)
	}
	projectRole, found := projectRoleByKey(projectCatalog.Roles, workspaceprovisionapplication.WorkspaceAdministratorRoleKey)
	if !found || projectRole.AssignmentMode != "system_managed" || projectRole.ProvisionToWorkspaces || projectRole.RiskLevel != "privileged" || len(projectRole.GrantableRoleKeys) != 0 {
		t.Fatalf("published installation role=%+v found=%t", projectRole, found)
	}
	wantPermissions := map[string]bool{
		workspaceprovisionapplication.ProvisionActionKey:                              true,
		workspaceprovisionapplication.ListWorkspacesActionKey:                         true,
		workspaceprovisionapplication.SuspendWorkspaceActionKey:                       true,
		workspaceprovisionapplication.ReactivateWorkspaceActionKey:                    true,
		workspaceprovisionapplication.UpdateWorkspaceCommercialConfigurationActionKey: true,
	}
	for _, permission := range projectRole.Permissions {
		if !wantPermissions[permission.PermissionKey] || permission.DataScope != identitysdk.DataScopeAll || !permission.AuditDenial {
			t.Fatalf("installation permission=%+v", permission)
		}
		delete(wantPermissions, permission.PermissionKey)
	}
	if len(wantPermissions) != 0 {
		t.Fatalf("installation role missing permissions=%v", wantPermissions)
	}

	projectDeclared := append([]manifestmodel.RoleSchema(nil), roles...)
	projectDeclared = append(projectDeclared, manifestmodel.RoleSchema{Key: workspaceprovisionapplication.WorkspaceAdministratorRoleKey, Name: "Project tenant admin"})
	if _, err := RuntimeInstallationWorkspaceProjectRoleCatalog(nil, projectDeclared, "workspace-primary", "runtime"); err == nil || !strings.Contains(err.Error(), "platform-owned") {
		t.Fatalf("project-declared installation role error=%v", err)
	}
}

func TestRuntimeWorkspaceRoleCatalogClosesFrozenHandlerCapabilitiesWithExactActionScope(t *testing.T) {
	descriptors := []runtimeext.HandlerDescriptor{
		runtimeRoleCapabilityDescriptor("department.provision", func(value *runtimeext.HandlerDescriptor) {
			value.TargetOrganization = &runtimeext.ActionTargetOrganizationCapability{Source: runtimeext.TargetOrganizationSourceProvisionedStore}
		}),
		runtimeRoleCapabilityDescriptor("organization_unit.create", func(value *runtimeext.HandlerDescriptor) {
			value.TargetOrganization = &runtimeext.ActionTargetOrganizationCapability{Source: runtimeext.TargetOrganizationSourceDeliveredOrganizationUnit}
			value.OrganizationUnitDelivery = &runtimeext.OrganizationUnitDeliveryCapability{
				Operations:   []runtimeext.OrganizationUnitDeliveryOperation{runtimeext.OrganizationUnitDeliveryCreate},
				NodeTypes:    []runtimeext.OrganizationUnitNodeType{runtimeext.OrganizationUnitNodeTypeDepartment},
				ParentSource: runtimeext.OrganizationUnitParentSourceWorkspaceCompany,
			}
		}),
		runtimeRoleCapabilityDescriptor("organization_unit.resolve", func(value *runtimeext.HandlerDescriptor) {
			value.TargetOrganization = &runtimeext.ActionTargetOrganizationCapability{Source: runtimeext.TargetOrganizationSourceExplicit, Input: runtimeext.TargetOrganizationInputInvocation}
			value.OrganizationUnitDelivery = &runtimeext.OrganizationUnitDeliveryCapability{
				Operations: []runtimeext.OrganizationUnitDeliveryOperation{runtimeext.OrganizationUnitDeliveryResolve},
				NodeTypes:  []runtimeext.OrganizationUnitNodeType{runtimeext.OrganizationUnitNodeTypeDepartment},
			}
		}),
		runtimeRoleCapabilityDescriptor("department.catalog", func(value *runtimeext.HandlerDescriptor) {
			value.StoreOrganizationCatalog = &runtimeext.StoreOrganizationCatalogCapability{MaxPageSize: 25}
		}),
		runtimeRoleCapabilityDescriptor("department.select", func(value *runtimeext.HandlerDescriptor) {
			value.TargetOrganization = &runtimeext.ActionTargetOrganizationCapability{Source: runtimeext.TargetOrganizationSourceExplicit, Input: runtimeext.TargetOrganizationInputInvocation}
		}),
		runtimeRoleCapabilityDescriptor("department.select_or_sole", func(value *runtimeext.HandlerDescriptor) {
			value.TargetOrganization = &runtimeext.ActionTargetOrganizationCapability{Source: runtimeext.TargetOrganizationSourceExplicitOrSoleAuthorizedStore, Input: runtimeext.TargetOrganizationInputInvocation}
		}),
		runtimeRoleCapabilityDescriptor("department.maintain", func(value *runtimeext.HandlerDescriptor) {
			value.TargetOrganization = &runtimeext.ActionTargetOrganizationCapability{Source: runtimeext.TargetOrganizationSourceRecordOwner}
			value.StoreOrganizationMutation = &runtimeext.ActionStoreOrganizationMutationCapability{Operations: []runtimeext.StoreOrganizationMutationOperation{runtimeext.StoreOrganizationMutationRename, runtimeext.StoreOrganizationMutationDisable}}
		}),
		runtimeRoleCapabilityDescriptor("staff.resolve", func(value *runtimeext.HandlerDescriptor) {
			value.TargetOrganization = &runtimeext.ActionTargetOrganizationCapability{Source: runtimeext.TargetOrganizationSourceRecordOwner}
			value.IdentityHandlerDelivery = &runtimeext.IdentityHandlerDeliveryCapability{
				Operations:      []runtimeext.IdentityHandlerOperation{runtimeext.IdentityHandlerCreate, runtimeext.IdentityHandlerUpdate, runtimeext.IdentityHandlerDisable, runtimeext.IdentityHandlerResolve},
				ProfileBindings: []runtimeext.IdentityProfileBindingCapability{{BindingKey: "employee", ObjectKey: "employee_profile"}},
			}
		}),
		runtimeRoleCapabilityDescriptor("installation.measure", func(value *runtimeext.HandlerDescriptor) {
			value.WorkspaceIdentityUsage = &runtimeext.WorkspaceIdentityUsageCapability{MaxPageSize: 20}
		}),
		runtimeRoleCapabilityDescriptor("department.no_capability", nil),
	}
	businessActions := []string{
		"department.provision", "organization_unit.create", "organization_unit.resolve", "department.catalog", "department.select", "department.select_or_sole", "department.maintain",
		"staff.resolve", "installation.measure", "department.no_capability",
	}
	permissions := make([]manifestmodel.RolePermission, 0, len(businessActions))
	for _, actionKey := range businessActions {
		permissions = append(permissions, manifestmodel.RolePermission{PermissionKey: actionKey, DataScope: identitysdk.DataScopeOrgChild, AuditDenial: true})
	}
	roles := []manifestmodel.RoleSchema{
		{Key: "operator", Name: "Operator", Audience: "any", AssignmentMode: "manual", ProvisionToWorkspaces: true, Permissions: permissions},
		{Key: "observer", Name: "Observer", Audience: "user", AssignmentMode: "manual", ProvisionToWorkspaces: true, Permissions: []manifestmodel.RolePermission{{PermissionKey: "unrelated.read", DataScope: identitysdk.DataScopeOwner}}},
	}
	catalog, err := RuntimeWorkspaceProjectRoleCatalog(nil, roles, "workspace-primary", "runtime", descriptors...)
	if err != nil {
		t.Fatal(err)
	}
	operator, found := projectRoleByKey(catalog.Roles, "operator")
	if !found {
		t.Fatal("operator role was not published")
	}
	wantCapabilities := []string{
		identitysdk.HandlerDeliveryCreatePermission,
		identitysdk.HandlerDeliveryDisablePermission,
		identitysdk.HandlerDeliveryResolvePermission,
		identitysdk.HandlerDeliveryUpdatePermission,
		identitysdk.StoreOrganizationDeliveryCreatePermission,
		identitysdk.StoreOrganizationDeliveryDisablePermission,
		identitysdk.StoreOrganizationDeliveryListPermission,
		identitysdk.StoreOrganizationDeliveryRenamePermission,
		identitysdk.StoreOrganizationDeliveryResolvePermission,
		identitysdk.WorkspaceIdentityUsageAggregatePermission,
		organizationunit.DeliveryCreatePermission,
		organizationunit.DeliveryResolvePermission,
	}
	for _, permissionKey := range wantCapabilities {
		permission, found := projectRolePermissionByKey(operator.Permissions, permissionKey)
		if !found || permission.DataScope != identitysdk.DataScopeOrgChild || !permission.AuditDenial {
			t.Fatalf("derived capability %q=%+v found=%t permissions=%#v", permissionKey, permission, found, operator.Permissions)
		}
	}
	if len(operator.Permissions) != len(businessActions)+len(wantCapabilities) {
		t.Fatalf("operator permissions=%#v", operator.Permissions)
	}
	observer, found := projectRoleByKey(catalog.Roles, "observer")
	if !found || len(observer.Permissions) != 1 || observer.Permissions[0].PermissionKey != "unrelated.read" {
		t.Fatalf("role without a descriptor-owned business Action received capability permissions: %#v", observer)
	}
}

func TestRuntimeOrganizationUnitTargetDoesNotDeriveStoreResolvePermission(t *testing.T) {
	organizationUnit := runtimeRoleCapabilityDescriptor("organization_unit.resolve", func(value *runtimeext.HandlerDescriptor) {
		value.TargetOrganization = &runtimeext.ActionTargetOrganizationCapability{Source: runtimeext.TargetOrganizationSourceExplicit, Input: runtimeext.TargetOrganizationInputInvocation}
		value.OrganizationUnitDelivery = &runtimeext.OrganizationUnitDeliveryCapability{
			Operations: []runtimeext.OrganizationUnitDeliveryOperation{runtimeext.OrganizationUnitDeliveryResolve},
			NodeTypes:  []runtimeext.OrganizationUnitNodeType{runtimeext.OrganizationUnitNodeTypeDepartment},
		}
	})
	ordinaryTarget := runtimeRoleCapabilityDescriptor("department.select", func(value *runtimeext.HandlerDescriptor) {
		value.TargetOrganization = &runtimeext.ActionTargetOrganizationCapability{Source: runtimeext.TargetOrganizationSourceExplicit, Input: runtimeext.TargetOrganizationInputInvocation}
	})

	permissions, err := runtimeDownstreamCapabilityPermissions([]runtimeext.HandlerDescriptor{organizationUnit, ordinaryTarget})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(permissions[organizationUnit.ActionKey], []string{organizationunit.DeliveryResolvePermission}) {
		t.Fatalf("Organization Unit permissions=%v", permissions[organizationUnit.ActionKey])
	}
	if !slices.Equal(permissions[ordinaryTarget.ActionKey], []string{identitysdk.StoreOrganizationDeliveryResolvePermission}) {
		t.Fatalf("ordinary target permissions=%v", permissions[ordinaryTarget.ActionKey])
	}
}

func TestRuntimeWorkspaceCapabilityClosureIsSharedStableAndFailClosed(t *testing.T) {
	descriptors := []runtimeext.HandlerDescriptor{
		runtimeRoleCapabilityDescriptor("department.provision", func(value *runtimeext.HandlerDescriptor) {
			value.TargetOrganization = &runtimeext.ActionTargetOrganizationCapability{Source: runtimeext.TargetOrganizationSourceProvisionedStore}
		}),
		runtimeRoleCapabilityDescriptor("department.catalog", func(value *runtimeext.HandlerDescriptor) {
			value.StoreOrganizationCatalog = &runtimeext.StoreOrganizationCatalogCapability{MaxPageSize: 25}
		}),
	}
	roles := []manifestmodel.RoleSchema{{
		Key: "workspace_admin", Name: "Workspace administrator", Audience: "any", AssignmentMode: "manual", ProvisionToWorkspaces: true,
		Permissions: []manifestmodel.RolePermission{
			{PermissionKey: "department.provision", DataScope: identitysdk.DataScopeAll},
			{PermissionKey: "department.catalog", DataScope: identitysdk.DataScopeAll},
		},
	}}
	bootstrap, err := RuntimeWorkspaceBootstrapRoleCatalog(nil, roles, "workspace_admin", "runtime", descriptors...)
	if err != nil {
		t.Fatal(err)
	}
	published, err := RuntimeWorkspaceProjectRoleCatalog(nil, roles, "workspace-primary", "runtime", descriptors...)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(bootstrap.Roles[0], published.Roles[0]) {
		t.Fatalf("bootstrap and ordinary publication capability closures differ:\nbootstrap=%#v\npublished=%#v", bootstrap.Roles[0], published.Roles[0])
	}
	reordered, err := RuntimeWorkspaceProjectRoleCatalog(nil, roles, "workspace-primary", "runtime", descriptors[1], descriptors[0])
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(published.Roles, reordered.Roles) || published.Roles[0].SchemaHash == "" {
		t.Fatalf("descriptor order changed role identity:\nfirst=%#v\nreordered=%#v", published.Roles, reordered.Roles)
	}
	conflicting := append([]manifestmodel.RoleSchema(nil), roles...)
	conflicting[0].Permissions = append([]manifestmodel.RolePermission(nil), roles[0].Permissions...)
	conflicting[0].Permissions[0].DataScope = identitysdk.DataScopeOwner
	conflicting[0].Permissions[1].DataScope = identitysdk.DataScopeOrg
	if _, err := RuntimeWorkspaceProjectRoleCatalog(nil, conflicting, "workspace-primary", "runtime", descriptors...); err == nil || !strings.Contains(err.Error(), "incompatible data scopes") {
		t.Fatalf("incompatible capability scopes error=%v", err)
	}
	comparable := append([]manifestmodel.RoleSchema(nil), roles...)
	comparable[0].Permissions = []manifestmodel.RolePermission{
		{PermissionKey: "department.provision", DataScope: identitysdk.DataScopeOrg},
		{PermissionKey: "department.catalog", DataScope: identitysdk.DataScopeOrgChild},
	}
	comparableCatalog, err := RuntimeWorkspaceProjectRoleCatalog(nil, comparable, "workspace-primary", "runtime", descriptors...)
	if err != nil {
		t.Fatal(err)
	}
	listGrant, found := projectRolePermissionByKey(comparableCatalog.Roles[0].Permissions, identitysdk.StoreOrganizationDeliveryListPermission)
	if !found || listGrant.DataScope != identitysdk.DataScopeOrgChild {
		t.Fatalf("comparable org scope join=%+v found=%t", listGrant, found)
	}
	comparable[0].Permissions[0], comparable[0].Permissions[1] = comparable[0].Permissions[1], comparable[0].Permissions[0]
	reversedComparable, err := RuntimeWorkspaceProjectRoleCatalog(nil, comparable, "workspace-primary", "runtime", descriptors...)
	if err != nil || !reflect.DeepEqual(comparableCatalog.Roles, reversedComparable.Roles) {
		t.Fatalf("scope join depends on Action grant order: first=%#v reversed=%#v err=%v", comparableCatalog.Roles, reversedComparable.Roles, err)
	}
	declaredInternal := append([]manifestmodel.RoleSchema(nil), roles...)
	declaredInternal[0].Permissions = append(append([]manifestmodel.RolePermission(nil), roles[0].Permissions...), manifestmodel.RolePermission{PermissionKey: identitysdk.StoreOrganizationDeliveryCreatePermission, DataScope: identitysdk.DataScopeAll})
	if _, err := RuntimeWorkspaceProjectRoleCatalog(nil, declaredInternal, "workspace-primary", "runtime", descriptors...); err == nil || !strings.Contains(err.Error(), "Runtime-managed downstream capability") {
		t.Fatalf("project-declared internal capability error=%v", err)
	}
}

func runtimeRoleCapabilityDescriptor(actionKey string, mutate func(*runtimeext.HandlerDescriptor)) runtimeext.HandlerDescriptor {
	descriptor := runtimeext.HandlerDescriptor{
		ActionKey: actionKey, InputType: "generated." + strings.ReplaceAll(actionKey, ".", "_") + "Input", OutputType: "generated." + strings.ReplaceAll(actionKey, ".", "_") + "Output",
		InputContractSHA256: strings.Repeat("a", 64), OutputContractSHA256: strings.Repeat("b", 64), HandlerRevision: "revision-1",
	}
	if mutate != nil {
		mutate(&descriptor)
	}
	return descriptor
}

func projectRolePermissionByKey(permissions []identitysdk.ProjectRolePermission, key string) (identitysdk.ProjectRolePermission, bool) {
	for _, permission := range permissions {
		if permission.PermissionKey == key {
			return permission, true
		}
	}
	return identitysdk.ProjectRolePermission{}, false
}

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
	err := publishRuntimeProjectRoles(t.Context(), binding, []definitionmodel.ObjectSchema{{Key: "member", Fields: []definitionmodel.FieldSchema{{Key: "name"}}}}, roles, "workspace-primary", "runtime", false)
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
	if err := publishRuntimeProjectRoles(t.Context(), &binding.runtimeIdentityBindingStub, nil, nil, "workspace-primary", "runtime", false); err != nil {
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
