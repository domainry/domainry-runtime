package runtimehost

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	identityprincipal "github.com/domainry/domainry-identity-sdk/authorization/principal"
	identitymodule "github.com/domainry/domainry-identity/module"
	workspaceprovisionapplication "github.com/domainry/domainry-runtime/runtime/application/workspaceprovision"
	"github.com/domainry/domainry-runtime/runtime/bootstrap"
	runtimebootstrap "github.com/domainry/domainry-runtime/runtime/bootstrap/runtime"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workspaceprovisionmodel "github.com/domainry/domainry-runtime/runtime/domain/workspaceprovision/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	workspaceprovisionpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workspaceprovision"
)

type installationAdministratorDeliveryProbe struct {
	credential InstallationAdministratorCredential
}

func (probe *installationAdministratorDeliveryProbe) DeliverInstallationAdministratorCredential(_ context.Context, credential InstallationAdministratorCredential) (InstallationAdministratorCredentialDeliveryAcknowledgment, error) {
	probe.credential = credential
	return InstallationAdministratorCredentialDeliveryAcknowledgment{Accepted: true}, nil
}

func TestExplicitInstallationAdministratorAuthenticatesAndReachesCommercialCatalogAndBillingAggregate(t *testing.T) {
	cfg := serverTestConfig()
	cfg.DatabaseDriver = "sqlite"
	cfg.DBPath = filepath.Join(t.TempDir(), "installation-administrator-acceptance.db")
	cfg.AuditExportTokenKey = "installation-usage-cursor-secret"
	cfg.InstallationAdministratorBootstrapEnabled = true
	cfg.InstallationAdministratorRequestID = "first-installation-administrator"
	cfg.InstallationAdministratorLoginID = "installation@example.test"
	cfg.InstallationAdministratorName = "Installation Administrator"
	cfg.InstallationAdministratorCredentialFile = filepath.Join(t.TempDir(), "unused-by-probe.json")
	store, err := bootstrap.PrepareProjectDatabase(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.CloseContext(context.Background()) })
	handle := projectIdentityDatabaseHandle(store, cfg.DBPath, nil, projectIdentityUsageOptions{ApplicationKey: cfg.IdentityAudience, CursorSecret: cfg.AuditExportTokenKey})
	factory := identitymodule.NewFactory(identitymodule.Options{IdentityVersion: "test", DatabaseDriver: "sqlite", DatabasePath: cfg.DBPath})
	installationDelivery := &installationAdministratorDeliveryProbe{}
	manager, err := newProjectWorkspaceManager(t.Context(), cfg, factory, store, handle, acceptingCredentialDelivery{}, installationDelivery)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close(context.Background()) })
	if err := manager.Activate(t.Context(), manifestmodel.ManifestSchema{Roles: workspaceRolesForTest(), InitialWorkspaceAdministratorRole: "headquarters_admin"}, nil); err != nil {
		t.Fatal(err)
	}
	if installationDelivery.credential.LoginID != cfg.InstallationAdministratorLoginID || installationDelivery.credential.InitialPassword == "" {
		t.Fatalf("installation credential=%+v", installationDelivery.credential)
	}
	application := identitysdk.ApplicationRef{WorkspaceID: identitysdk.WorkspaceID(manager.cfg.IdentityWorkspaceID), ApplicationKey: identitysdk.ApplicationKey(cfg.IdentityAudience)}
	if _, err := manager.Binding().Applications().Register(t.Context(), identitysdk.ApplicationRegistration{
		Application: application, RedirectURLs: []string{"http://localhost:3100/auth/callback"},
	}); err != nil {
		t.Fatal(err)
	}
	roles := workspaceRolesForTest()
	permissionRequest, err := identitysdk.NewPermissionReconcileRequest(application, "application:domainry-runtime", "", []identitysdk.PermissionDefinition{{
		PermissionKey: workspaceprovisionapplication.ListWorkspacesActionKey, ResourceKey: "runtime.workspaceprovision", OperationKey: "list_workspaces",
		Label: "List Workspaces", Category: "Workspace administration", SourceKind: "runtime_action",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Binding().Permissions().Reconcile(t.Context(), permissionRequest); err != nil {
		t.Fatal(err)
	}
	roleCatalog, err := runtimebootstrap.RuntimeInstallationWorkspaceProjectRoleCatalog(nil, roles, manager.cfg.IdentityWorkspaceID, cfg.IdentityAudience)
	if err != nil {
		t.Fatal(err)
	}
	publisher, ok := manager.Binding().(identitysdk.ProjectRoleCatalogPublisher)
	if !ok {
		t.Fatalf("project role publisher=%T", manager.Binding())
	}
	if _, err := publisher.PublishProjectRoles(t.Context(), roleCatalog); err != nil {
		t.Fatal(err)
	}
	session, err := manager.Binding().Authentication().LoginWithPassword(t.Context(), identitysdk.PasswordLoginRequest{
		WorkspaceID: application.WorkspaceID, ApplicationKey: application.ApplicationKey,
		Login: installationDelivery.credential.LoginID, Password: installationDelivery.credential.InitialPassword,
	})
	if err != nil || session.AccessToken == "" || session.DefaultRole != "tenant_admin" ||
		!slices.ContainsFunc(session.Roles, func(role identitysdk.Role) bool { return role.Key == "tenant_admin" }) ||
		!slices.Contains(session.Permissions, workspaceprovisionapplication.ListWorkspacesActionKey) {
		t.Fatalf("session=%+v error=%v", session, err)
	}
	resolver, err := identityprincipal.NewResolver(manager.Binding(), identityprincipal.Options{})
	if err != nil {
		t.Fatal(err)
	}
	authenticated, err := resolver.Authenticate(t.Context(), session.AccessToken)
	if err != nil || authenticated.RoleKey != "tenant_admin" || authenticated.AuthorizationRevision == "" ||
		!authenticated.HasPermission(workspaceprovisionapplication.ListWorkspacesActionKey) {
		t.Fatalf("authenticated principal=%+v error=%v", authenticated, err)
	}
	usageBinding, ok := manager.Binding().(identitysdk.EmbeddedWorkspaceIdentityUsageBinding)
	if !ok || usageBinding.WorkspaceIdentityUsageUnitOfWorkBinder() == nil {
		t.Fatalf("usage binding=%T", manager.Binding())
	}
	usageBinder := usageBinding.WorkspaceIdentityUsageUnitOfWorkBinder()
	authorization, err := usageBinder.AuthorizeWorkspaceIdentityUsage(t.Context(), identitysdk.WorkspaceIdentityUsageAuthorizationRequest{AccessToken: session.AccessToken})
	if err != nil {
		t.Fatal(err)
	}
	tx, err := store.DB().BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	usage, err := usageBinder.BindWorkspaceIdentityUsageUnitOfWork(identitysdk.EmbeddedTransaction{Executor: tx})
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	txCtx := database.WithActionExecutionTransaction(t.Context(), tx)
	item, err := usage.ResolveWorkspaceIdentityUsage(txCtx, identitysdk.WorkspaceIdentityUsageResolveRequest{
		ContractVersion: identitysdk.CurrentWorkspaceIdentityUsageContractVersion,
		ContractHash:    identitysdk.CurrentWorkspaceIdentityUsageContractHash,
		Authorization:   authorization, WorkspaceCode: cfg.InitialWorkspaceCode,
	})
	if err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	var expectedActiveHumanRoles int64
	if err := tx.QueryRowContext(txCtx, `SELECT COUNT(DISTINCT user.id) FROM _identity_users user JOIN _identity_user_role_assignments assignment ON assignment.workspace_id=user.workspace_id AND assignment.user_id=user.id WHERE user.workspace_id=? AND user.account_type='human' AND user.status='active' AND assignment.status='active'`, manager.cfg.IdentityWorkspaceID).Scan(&expectedActiveHumanRoles); err != nil {
		_ = tx.Rollback()
		t.Fatal(err)
	}
	if expectedActiveHumanRoles < 2 || item.Accounts.ActiveHumanAccountsWithActiveRole != expectedActiveHumanRoles {
		_ = tx.Rollback()
		t.Fatalf("billing aggregate=%+v expected_active_human_roles=%d", item.Accounts, expectedActiveHumanRoles)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}

	previousInstallationWorkspaceID := principalmodel.InstallationWorkspaceID
	t.Cleanup(func() { principalmodel.InstallationWorkspaceID = previousInstallationWorkspaceID })
	if err := principalmodel.ConfigureInstallationWorkspaceID(manager.cfg.IdentityWorkspaceID); err != nil {
		t.Fatal(err)
	}
	principal := principalmodel.NewPrincipalFromIdentity(authenticated, "catalog-acceptance")
	installationID, err := store.InstallationIdentity(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	cursor, err := workspaceprovisionapplication.NewWorkspaceAdministrationCursorCodec([]byte("installation-administrator-catalog-key"), installationID, nil)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := workspaceprovisionapplication.NewWorkspaceAdministrationApplicationService(workspaceprovisionpersistence.NewWorkspaceAdministrationStore(store), cursor).
		List(t.Context(), principal, workspaceprovisionmodel.CatalogQuery{PageSize: 10})
	if err != nil || len(catalog.Items) != 1 || catalog.Items[0].CanonicalCode != cfg.InitialWorkspaceCode || catalog.Items[0].CommercialConfiguration.Plan != "standard" {
		t.Fatalf("catalog=%+v error=%v", catalog, err)
	}
	if err := manager.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	replayDelivery := &installationAdministratorDeliveryProbe{}
	restarted, err := newProjectWorkspaceManager(t.Context(), cfg, factory, store,
		projectIdentityDatabaseHandle(store, cfg.DBPath, nil, projectIdentityUsageOptions{ApplicationKey: cfg.IdentityAudience, CursorSecret: cfg.AuditExportTokenKey}),
		nil, replayDelivery)
	if err != nil {
		t.Fatalf("restart with acknowledged receipt: %v", err)
	}
	if replayDelivery.credential.InitialPassword != "" {
		t.Fatal("acknowledged installation administrator credential was delivered twice")
	}
	if err := restarted.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}
