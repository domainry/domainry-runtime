package runtimehost

import (
	"context"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	identitymodule "github.com/domainry/domainry-identity/module"
	organizationunit "github.com/domainry/domainry-identity/organizationunit"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	"github.com/domainry/domainry-runtime/runtime/bootstrap"
	runtimebootstrap "github.com/domainry/domainry-runtime/runtime/bootstrap/runtime"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workspaceprovision"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

type hostWorkspaceBootstrapParticipant struct{}

type initializedWorkspaceBootstrapBindingProbe struct {
	identityBindingStub
	roleCatalog       identitysdk.ProjectRoleCatalog
	navigationCatalog identitysdk.ProjectNavigationCatalog
}

func (probe *initializedWorkspaceBootstrapBindingProbe) BindBootstrapProjectRoleCatalog(_ context.Context, catalog identitysdk.ProjectRoleCatalog) error {
	probe.roleCatalog = catalog
	return nil
}

func (probe *initializedWorkspaceBootstrapBindingProbe) BindBootstrapProjectNavigationCatalog(_ context.Context, catalog identitysdk.ProjectNavigationCatalog) error {
	probe.navigationCatalog = catalog
	return nil
}

func (*initializedWorkspaceBootstrapBindingProbe) BootstrapWorkspaceIdentity(context.Context, identitysdk.WorkspaceIdentityBootstrapRequest, identitysdk.EmbeddedTransaction) (identitysdk.WorkspaceIdentityBootstrapReceipt, error) {
	return identitysdk.WorkspaceIdentityBootstrapReceipt{}, nil
}

func (*initializedWorkspaceBootstrapBindingProbe) CompleteWorkspaceIdentityBootstrap(context.Context, identitysdk.WorkspaceIdentityBootstrapCompletion) error {
	return nil
}

func (*initializedWorkspaceBootstrapBindingProbe) ClaimWorkspaceIdentityBootstrapCredential(context.Context, identitysdk.WorkspaceIdentityBootstrapCredentialClaim) (identitysdk.WorkspaceIdentityBootstrapOneTimeCredential, error) {
	return identitysdk.WorkspaceIdentityBootstrapOneTimeCredential{}, nil
}

func (hostWorkspaceBootstrapParticipant) Descriptor() runtimeext.WorkspaceBootstrapDescriptor {
	descriptor := runtimeext.WorkspaceBootstrapDescriptor{
		Key:                 "store_configuration",
		InputType:           "runtimehost.StoreConfigurationInput",
		ParticipantRevision: "revision-1",
		InputFields: []runtimeext.WorkspaceBootstrapInputField{
			{Key: "currency", Type: runtimeext.WorkspaceBootstrapInputString, Required: true},
		},
		Records: []runtimeext.WorkspaceBootstrapRecordCapability{
			{Key: "initial_store_configuration", ObjectKey: "store_configuration", Fields: []string{"currency"}},
		},
	}
	descriptor.InputContractSHA256 = descriptor.ComputedInputContractSHA256()
	return descriptor
}

func (hostWorkspaceBootstrapParticipant) BuildWorkspaceBootstrap(_ context.Context, _ runtimeext.WorkspaceBootstrapContext, input map[string]any) ([]runtimeext.WorkspaceBootstrapRecord, error) {
	return []runtimeext.WorkspaceBootstrapRecord{{
		CapabilityKey: "initial_store_configuration",
		Data:          map[string]any{"currency": input["currency"]},
	}}, nil
}

func TestInitialWorkspaceRequestUsesTypedCommercialConfiguration(t *testing.T) {
	cfg := serverTestConfig()
	request, err := initialWorkspaceRequest(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if request.WorkspaceCode != "primary" || request.CommercialConfiguration.MaxStores != 2 {
		t.Fatalf("request=%+v", request)
	}
	cfg.InitialWorkspaceApplicationBootstrapJSON = `{"currency":"JPY","tax_rate":10}`
	request, err = initialWorkspaceRequest(cfg)
	if err != nil || request.ApplicationBootstrap["currency"] != "JPY" {
		t.Fatalf("application bootstrap=%#v err=%v", request.ApplicationBootstrap, err)
	}
	cfg.InitialWorkspaceApplicationBootstrapJSON = `{"currency":"JPY"} []`
	if _, err := initialWorkspaceRequest(cfg); err == nil || !strings.Contains(err.Error(), "exactly one") {
		t.Fatalf("trailing application bootstrap error=%v", err)
	}
	cfg.InitialWorkspaceCommercialConfigurationJSON = `{"plan":"standard","max_stores":2,"unknown":true}`
	if _, err := initialWorkspaceRequest(cfg); err == nil || !strings.Contains(err.Error(), "unknown field") {
		t.Fatalf("unknown-field error=%v", err)
	}
}

func TestWorkspaceManagerMaterializesApplicationSchemaBeforeAtomicBootstrap(t *testing.T) {
	cfg := serverTestConfig()
	cfg.DatabaseDriver = "sqlite"
	cfg.DBPath = filepath.Join(t.TempDir(), "workspace-application-bootstrap.db")
	cfg.InitialWorkspaceApplicationBootstrapJSON = `{"currency":"JPY"}`
	database, err := bootstrap.PrepareProjectDatabase(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.CloseContext(t.Context()) })
	manager, err := newProjectWorkspaceManager(
		t.Context(), cfg, identitymodule.NewFactory(identitymodule.Options{IdentityVersion: "test", DatabaseDriver: "sqlite", DatabasePath: cfg.DBPath}),
		database, projectIdentityDatabaseHandle(database, cfg.DBPath, nil), acceptingCredentialDelivery{},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close(t.Context()) })
	manifest := manifestmodel.ManifestSchema{
		Objects: []definitionmodel.ObjectSchema{{
			Key: "store_configuration",
			Fields: []definitionmodel.FieldSchema{
				{Key: "currency", Type: "text", Required: true},
				{Key: "label", Type: "text", Required: true, DefaultValue: "Default Store"},
			},
		}},
		Roles:                             workspaceRolesForTest(),
		InitialWorkspaceAdministratorRole: "headquarters_admin",
	}
	if err := manager.Activate(t.Context(), manifest, hostWorkspaceBootstrapParticipant{}); err != nil {
		t.Fatal(err)
	}
	var workspaceID, ownerOrganizationID, currency, label string
	if err := database.DB().QueryRowContext(t.Context(), `SELECT "workspace_id","owner_org_id","currency","label" FROM "store_configuration"`).Scan(&workspaceID, &ownerOrganizationID, &currency, &label); err != nil {
		t.Fatal(err)
	}
	if workspaceID == "" || ownerOrganizationID == "" || currency != "JPY" || label != "Default Store" {
		t.Fatalf("application bootstrap workspace=%q owner=%q currency=%q label=%q", workspaceID, ownerOrganizationID, currency, label)
	}
}

func TestWorkspaceManagerInitializesM1HumanRolesAndPublishesInternalRoles(t *testing.T) {
	cfg := serverTestConfig()
	cfg.DatabaseDriver = "sqlite"
	cfg.DBPath = filepath.Join(t.TempDir(), "workspace-bootstrap.db")
	database, err := bootstrap.PrepareProjectDatabase(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.CloseContext(t.Context()) })
	credentialDelivery := &initialCredentialDeliveryProbe{accepted: true}
	manager, err := newProjectWorkspaceManager(
		t.Context(), cfg, identitymodule.NewFactory(identitymodule.Options{IdentityVersion: "test", DatabaseDriver: "sqlite", DatabasePath: cfg.DBPath}),
		database, projectIdentityDatabaseHandle(database, cfg.DBPath, nil), credentialDelivery,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close(t.Context()) })
	if err := manager.SetProjectNavigationCatalog(identitysdk.ProjectNavigationCatalog{
		ContractVersion: identitysdk.ProjectNavigationContractVersion,
		Menus: []identitysdk.ProjectMenuDefinition{
			{Key: "business.crm", Label: map[string]string{"en": "CRM"}, Route: "/crm", SortOrder: 10},
			{Key: "business.crm.leads", Label: map[string]string{"en": "Leads"}, Route: "/crm/leads", ParentKey: "business.crm", SortOrder: 20},
		},
		RoleMenuSets: []identitysdk.ProjectRoleMenuSet{{RoleKey: "crm_acceptance_admin", MenuKeys: []string{"business.crm.leads"}}},
	}); err != nil {
		t.Fatal(err)
	}
	manifest := manifestmodel.ManifestSchema{
		Roles:                             m1WorkspaceRolesForTest(),
		InitialWorkspaceAdministratorRole: "crm_acceptance_admin",
	}
	provisionDescriptor := runtimeext.HandlerDescriptor{
		ActionKey: "department_anchor.provision", InputType: "runtimehost.DepartmentAnchorProvisionInput", OutputType: "runtimehost.DepartmentAnchorProvisionOutput",
		InputContractSHA256: strings.Repeat("a", 64), OutputContractSHA256: strings.Repeat("b", 64), HandlerRevision: "revision-1",
		TargetOrganization: &runtimeext.ActionTargetOrganizationCapability{Source: runtimeext.TargetOrganizationSourceProvisionedStore},
	}
	if err := manager.Activate(t.Context(), manifest, nil, provisionDescriptor); err != nil {
		t.Fatal(err)
	}
	workspaceID := manager.Config().IdentityWorkspaceID
	var navigationRootID, navigationChildID, navigationParentID string
	if err := database.DB().QueryRowContext(t.Context(), `SELECT "id" FROM "_identity_menus" WHERE "workspace_id" = ? AND "menu_key" = ?`, workspaceID, "business.crm").Scan(&navigationRootID); err != nil {
		t.Fatal(err)
	}
	if err := database.DB().QueryRowContext(t.Context(), `SELECT "id","parent_id" FROM "_identity_menus" WHERE "workspace_id" = ? AND "menu_key" = ?`, workspaceID, "business.crm.leads").Scan(&navigationChildID, &navigationParentID); err != nil {
		t.Fatal(err)
	}
	if navigationRootID == "" || navigationChildID == "" || navigationParentID != navigationRootID {
		t.Fatalf("project navigation root=%q child=%q parent=%q", navigationRootID, navigationChildID, navigationParentID)
	}
	var navigationAssignments int
	if err := database.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM "_identity_role_menu_assignments" a JOIN "_identity_roles" r ON r."workspace_id" = a."workspace_id" AND r."id" = a."role_id" JOIN "_identity_menus" m ON m."workspace_id" = a."workspace_id" AND m."id" = a."menu_id" WHERE a."workspace_id" = ? AND r."role_key" = ? AND m."menu_key" = ?`, workspaceID, "crm_acceptance_admin", "business.crm.leads").Scan(&navigationAssignments); err != nil || navigationAssignments != 1 {
		t.Fatalf("project navigation assignments=%d err=%v", navigationAssignments, err)
	}
	bootstrapRoleKeys := workspaceIdentityRoleKeys(t, database, workspaceID)
	wantBootstrapRoles := []string{
		"crm_acceptance_admin",
		"sales_director",
		"sales_rep",
	}
	sort.Strings(wantBootstrapRoles)
	for _, roleKey := range wantBootstrapRoles {
		if !slices.Contains(bootstrapRoleKeys, roleKey) {
			t.Fatalf("bootstrap role keys=%v missing provisioned human role=%q", bootstrapRoleKeys, roleKey)
		}
	}
	for _, roleKey := range []string{"conversion_workflow_service", "followup_reminder_service"} {
		if slices.Contains(bootstrapRoleKeys, roleKey) {
			t.Fatalf("internal service role %q was provisioned into initial Workspace login roles: %v", roleKey, bootstrapRoleKeys)
		}
	}
	boundCatalog, err := runtimebootstrap.RuntimeWorkspaceProjectRoleCatalog(manifest.Objects, manifest.Roles, workspaceID, cfg.IdentityAudience, provisionDescriptor)
	if err != nil {
		t.Fatal(err)
	}
	publisher, ok := manager.Binding().(identitysdk.ProjectRoleCatalogPublisher)
	if !ok {
		t.Fatal("initialized Identity binding cannot publish the complete project role catalog")
	}
	receipt, err := publisher.PublishProjectRoles(t.Context(), boundCatalog)
	if err != nil || receipt.Published != len(manifest.Roles) {
		t.Fatalf("post-bind role publication receipt=%+v err=%v", receipt, err)
	}
	roleKeys := workspaceIdentityRoleKeys(t, database, workspaceID)
	for _, roleKey := range append(wantBootstrapRoles, "conversion_workflow_service", "followup_reminder_service") {
		if !slices.Contains(roleKeys, roleKey) {
			t.Fatalf("published role keys=%v missing=%q", roleKeys, roleKey)
		}
	}
	for table, want := range map[string]int{"_identity_organization_units": 2} {
		var count int
		if err := database.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM "`+table+`" WHERE "workspace_id" = ?`, workspaceID).Scan(&count); err != nil || count != want {
			t.Fatalf("%s count=%d want=%d err=%v", table, count, want, err)
		}
	}
	for _, table := range []string{"_identity_users", "_identity_roles", "_identity_organization_units"} {
		var fixtureRows int
		if err := database.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM "`+table+`" WHERE "workspace_id" = ? AND "id" IN (?, ?, ?, ?)`, workspaceID, "director", "sales-east", "department-1", "department-2").Scan(&fixtureRows); err != nil || fixtureRows != 0 {
			t.Fatalf("%s legacy fixture rows=%d err=%v", table, fixtureRows, err)
		}
	}
	references, err := manager.BusinessSeedReferenceCandidates()
	if err != nil || len(references) != 1 || references[0].RecordID == "" {
		t.Fatalf("initial administrator reference=%+v err=%v", references, err)
	}
	initialAdministratorID := references[0].RecordID
	var administratorAssignments, otherAssignments int
	if err := database.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM "_identity_user_role_assignments" a JOIN "_identity_roles" r ON r."workspace_id" = a."workspace_id" AND r."id" = a."role_id" WHERE a."workspace_id" = ? AND a."user_id" = ? AND r."role_key" = ?`, workspaceID, initialAdministratorID, manifest.InitialWorkspaceAdministratorRole).Scan(&administratorAssignments); err != nil || administratorAssignments != 1 {
		t.Fatalf("initial administrator assignments=%d role=%q initial_admin=%q err=%v", administratorAssignments, manifest.InitialWorkspaceAdministratorRole, initialAdministratorID, err)
	}
	if err := database.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM "_identity_user_role_assignments" a JOIN "_identity_roles" r ON r."workspace_id" = a."workspace_id" AND r."id" = a."role_id" WHERE a."workspace_id" = ? AND a."user_id" = ? AND r."role_key" <> ?`, workspaceID, initialAdministratorID, manifest.InitialWorkspaceAdministratorRole).Scan(&otherAssignments); err != nil || otherAssignments != 0 {
		t.Fatalf("initial administrator received another role: count=%d err=%v", otherAssignments, err)
	}
	application := identitysdk.ApplicationRef{WorkspaceID: identitysdk.WorkspaceID(workspaceID), ApplicationKey: identitysdk.ApplicationKey(cfg.IdentityAudience)}
	if _, err := manager.Binding().Applications().Register(t.Context(), identitysdk.ApplicationRegistration{
		Application: application, RedirectURLs: []string{"http://localhost:3100/auth/callback"},
	}); err != nil {
		t.Fatal(err)
	}
	session, err := manager.Binding().Authentication().LoginWithPassword(t.Context(), identitysdk.PasswordLoginRequest{
		WorkspaceID: application.WorkspaceID, ApplicationKey: application.ApplicationKey,
		Login: credentialDelivery.credential.LoginID, Password: credentialDelivery.credential.InitialPassword,
	})
	if err != nil || session.AccessToken == "" ||
		!slices.Contains(session.Permissions, identitysdk.StoreOrganizationDeliveryListPermission) ||
		!slices.Contains(session.Permissions, identitysdk.StoreOrganizationDeliveryCreatePermission) {
		t.Fatalf("initial administrator capability permissions=%v token_present=%t error=%v", session.Permissions, session.AccessToken != "", err)
	}
}

func m1WorkspaceRolesForTest() []manifestmodel.RoleSchema {
	return []manifestmodel.RoleSchema{
		workspaceInternalServiceRoleForTest(),
		{Key: "crm_acceptance_admin", Name: "Crm Acceptance Admin", Audience: "any", AssignmentMode: "manual", ProvisionToWorkspaces: true, Permissions: []manifestmodel.RolePermission{{PermissionKey: "department_anchor.provision", DataScope: identitysdk.DataScopeAll}}},
		{Key: "followup_reminder_service", Name: "Followup Reminder Service", Audience: "service", AssignmentMode: "system_managed", Permissions: []manifestmodel.RolePermission{{PermissionKey: "lead.send_overdue_reminders", DataScope: identitysdk.DataScopeAll}}},
		{Key: "sales_director", Name: "Sales Director", Audience: "any", AssignmentMode: "manual", ProvisionToWorkspaces: true},
		{Key: "sales_rep", Name: "Sales Rep", Audience: "any", AssignmentMode: "manual", ProvisionToWorkspaces: true},
	}
}

func TestWorkspaceManagerPublishesOrganizationUnitDeliveryPermissionWithoutReplacingStoreDelivery(t *testing.T) {
	cfg := serverTestConfig()
	cfg.DatabaseDriver = "sqlite"
	cfg.DBPath = filepath.Join(t.TempDir(), "workspace-organization-capabilities.db")
	database, err := bootstrap.PrepareProjectDatabase(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.CloseContext(t.Context()) })
	credentialDelivery := &initialCredentialDeliveryProbe{accepted: true}
	manager, err := newProjectWorkspaceManager(
		t.Context(), cfg, identitymodule.NewFactory(identitymodule.Options{IdentityVersion: "test", DatabaseDriver: "sqlite", DatabasePath: cfg.DBPath}),
		database, projectIdentityDatabaseHandle(database, cfg.DBPath, nil), credentialDelivery,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close(t.Context()) })
	manifest := manifestmodel.ManifestSchema{
		Roles: []manifestmodel.RoleSchema{{
			Key: "organization_operator", Name: "Organization operator", Audience: "any", AssignmentMode: "manual", ProvisionToWorkspaces: true,
			Permissions: []manifestmodel.RolePermission{
				{PermissionKey: "store.provision", DataScope: identitysdk.DataScopeAll},
				{PermissionKey: "department.provision", DataScope: identitysdk.DataScopeAll},
			},
		}},
		InitialWorkspaceAdministratorRole: "organization_operator",
	}
	storeDescriptor := runtimeext.HandlerDescriptor{
		ActionKey: "store.provision", InputType: "runtimehost.StoreProvisionInput", OutputType: "runtimehost.StoreProvisionOutput",
		InputContractSHA256: strings.Repeat("a", 64), OutputContractSHA256: strings.Repeat("b", 64), HandlerRevision: "revision-1",
		TargetOrganization: &runtimeext.ActionTargetOrganizationCapability{Source: runtimeext.TargetOrganizationSourceProvisionedStore},
	}
	departmentDescriptor := runtimeext.HandlerDescriptor{
		ActionKey: "department.provision", InputType: "runtimehost.DepartmentProvisionInput", OutputType: "runtimehost.DepartmentProvisionOutput",
		InputContractSHA256: strings.Repeat("c", 64), OutputContractSHA256: strings.Repeat("d", 64), HandlerRevision: "revision-1",
		TargetOrganization: &runtimeext.ActionTargetOrganizationCapability{Source: runtimeext.TargetOrganizationSourceDeliveredOrganizationUnit},
		OrganizationUnitDelivery: &runtimeext.OrganizationUnitDeliveryCapability{
			Operations:   []runtimeext.OrganizationUnitDeliveryOperation{runtimeext.OrganizationUnitDeliveryCreate},
			NodeTypes:    []runtimeext.OrganizationUnitNodeType{runtimeext.OrganizationUnitNodeTypeDepartment},
			ParentSource: runtimeext.OrganizationUnitParentSourceWorkspaceCompany,
		},
	}
	if err := manager.Activate(t.Context(), manifest, nil, storeDescriptor, departmentDescriptor); err != nil {
		t.Fatal(err)
	}
	application := identitysdk.ApplicationRef{WorkspaceID: identitysdk.WorkspaceID(manager.Config().IdentityWorkspaceID), ApplicationKey: identitysdk.ApplicationKey(cfg.IdentityAudience)}
	if _, err := manager.Binding().Applications().Register(t.Context(), identitysdk.ApplicationRegistration{Application: application}); err != nil {
		t.Fatal(err)
	}
	session, err := manager.Binding().Authentication().LoginWithPassword(t.Context(), identitysdk.PasswordLoginRequest{
		WorkspaceID: application.WorkspaceID, ApplicationKey: application.ApplicationKey,
		Login: credentialDelivery.credential.LoginID, Password: credentialDelivery.credential.InitialPassword,
	})
	if err != nil || !slices.Contains(session.Permissions, identitysdk.StoreOrganizationDeliveryCreatePermission) || !slices.Contains(session.Permissions, organizationunit.DeliveryCreatePermission) {
		t.Fatalf("published capability permissions=%v error=%v", session.Permissions, err)
	}
}

func workspaceIdentityRoleKeys(t *testing.T, database *bootstrap.ProjectDatabase, workspaceID string) []string {
	t.Helper()
	rows, err := database.DB().QueryContext(t.Context(), `SELECT "role_key" FROM "_identity_roles" WHERE "workspace_id" = ?`, workspaceID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var roleKeys []string
	for rows.Next() {
		var roleKey string
		if err := rows.Scan(&roleKey); err != nil {
			t.Fatal(err)
		}
		roleKeys = append(roleKeys, roleKey)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	sort.Strings(roleKeys)
	return roleKeys
}

func TestWorkspaceManagerAuthoritySurvivesRestart(t *testing.T) {
	cfg := serverTestConfig()
	cfg.DatabaseDriver = "sqlite"
	cfg.DBPath = filepath.Join(t.TempDir(), "workspace-restart.db")
	database, err := bootstrap.PrepareProjectDatabase(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	manager, err := newProjectWorkspaceManager(t.Context(), cfg, identityFactoryStub{}, database, projectIdentityDatabaseHandle(database, cfg.DBPath, nil), acceptingCredentialDelivery{})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Activate(t.Context(), manifestmodel.ManifestSchema{Roles: workspaceRolesForTest(), InitialWorkspaceAdministratorRole: "headquarters_admin"}, nil); err != nil {
		t.Fatal(err)
	}
	installation, found, err := workspaceprovision.LoadInstallation(t.Context(), database)
	if err != nil || !found || installation.WorkspaceID == "" {
		t.Fatalf("installation=%+v found=%t err=%v", installation, found, err)
	}
	if err := manager.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := database.CloseContext(t.Context()); err != nil {
		t.Fatal(err)
	}

	reopened, err := bootstrap.PrepareProjectDatabase(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.CloseContext(t.Context()) })
	initializedBinding := &initializedWorkspaceBootstrapBindingProbe{}
	restarted, err := newProjectWorkspaceManager(t.Context(), cfg, identityFactoryStub{binding: initializedBinding}, reopened, projectIdentityDatabaseHandle(reopened, cfg.DBPath, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close(t.Context()) })
	if restarted.Binding() == nil || restarted.Config().IdentityWorkspaceID != installation.WorkspaceID {
		t.Fatalf("restart config=%+v", restarted.Config())
	}
	navigation := identitysdk.ProjectNavigationCatalog{
		ContractVersion: identitysdk.ProjectNavigationContractVersion,
		Menus:           []identitysdk.ProjectMenuDefinition{{Key: "business.orders", Label: map[string]string{"en": "Orders"}, Route: "/orders", SortOrder: 10}},
		RoleMenuSets:    []identitysdk.ProjectRoleMenuSet{{RoleKey: "headquarters_admin", MenuKeys: []string{"business.orders"}}},
	}
	if err := restarted.SetProjectNavigationCatalog(navigation); err != nil {
		t.Fatal(err)
	}
	if err := restarted.Activate(t.Context(), manifestmodel.ManifestSchema{Roles: workspaceRolesForTest(), InitialWorkspaceAdministratorRole: "headquarters_admin"}, nil); err != nil {
		t.Fatal(err)
	}
	if len(initializedBinding.navigationCatalog.Menus) != 1 || initializedBinding.navigationCatalog.Menus[0].Key != "business.orders" || len(initializedBinding.roleCatalog.Roles) == 0 {
		t.Fatalf("restart catalogs role=%+v navigation=%+v", initializedBinding.roleCatalog, initializedBinding.navigationCatalog)
	}
}

func TestWorkspaceBootstrapIsNotPubliclyConfigurableWithLegacyFixtureInputs(t *testing.T) {
	for _, definition := range config.Definitions() {
		name := strings.ToLower(definition.Name)
		if strings.Contains(name, "acceptance_fixture") || strings.Contains(name, "tenant_code") || strings.Contains(name, "tenant_name") {
			t.Fatalf("legacy bootstrap input remains reachable: %s", definition.Name)
		}
	}
}
