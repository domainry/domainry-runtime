package runtimehost

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	connector "github.com/domainry/domainry-connector-sdk"
	dataexchangesdk "github.com/domainry/domainry-data-exchange-sdk"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	identitymodule "github.com/domainry/domainry-identity/module"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	monitoringsdk "github.com/domainry/domainry-monitoring-sdk"
	notificationsdk "github.com/domainry/domainry-notification-sdk"
	reportsdk "github.com/domainry/domainry-report-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	"github.com/domainry/domainry-runtime/runtime/bootstrap"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workspaceprovision"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	runtimehttp "github.com/domainry/domainry-runtime/runtime/transport/http"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
)

func TestInitialTenantRequestDecodesManagedAcceptanceFixturesWithoutSerializableCredentials(t *testing.T) {
	secret := "ActorPassword1!"
	cfg := config.Config{
		InitialTenantRequestID: "request", InitialTenantCode: "tenant", InitialTenantName: "Tenant",
		InitialManagementLoginID: "admin@example.test", InitialManagementName: "Admin", InitialManagementPassword: "AdminPassword1!",
		InitialAcceptanceFixtures: `{"organizations":[{"id":"east","code":"east","name":"East"}],"actors":[{"id":"director","login_id":"director@example.test","name":"Director","role_key":"sales_director","initial_password":"` + secret + `"},{"id":"sales-east","login_id":"sales@example.test","name":"Sales","role_key":"sales_rep","organization_id":"east","manager_user_id":"director","initial_password":"` + secret + `"}]}`,
	}
	_, _, organizations, actors, err := initialTenantRequest(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(organizations) != 1 || len(actors) != 2 || actors[1].ManagerUserID != "director" || actors[1].OrganizationID != "east" || actors[1].InitialPassword != secret {
		t.Fatalf("fixtures organizations=%+v actors=%+v", organizations, actors)
	}
	encoded, err := json.Marshal(struct {
		Config config.Config                        `json:"config"`
		Actor  identitysdk.WorkspaceAcceptanceActor `json:"actor"`
	}{Config: cfg, Actor: actors[1]})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), "initial_acceptance") {
		t.Fatalf("acceptance credential leaked through serializable config: %s", encoded)
	}
}

func TestProjectTenantManagerInitializesBaselineAcceptanceFixturesWithProjectRoleCatalog(t *testing.T) {
	const actorSecret = "ActorPassword1!"
	cfg := serverTestConfig()
	cfg.DatabaseDriver = "sqlite"
	cfg.DBPath = filepath.Join(t.TempDir(), "baseline-acceptance.db")
	cfg.InitialAcceptanceFixtures = `{"organizations":[{"id":"department-1","code":"department-1","name":"Department 1"},{"id":"department-2","code":"department-2","name":"Department 2"}],"actors":[{"id":"director","login_id":"director@example.test","name":"Director","role_key":"sales_director","initial_password":"` + actorSecret + `"},{"id":"rep-1","login_id":"rep-1@example.test","name":"Rep 1","role_key":"sales_rep","organization_id":"department-1","manager_user_id":"director","initial_password":"` + actorSecret + `"},{"id":"rep-2","login_id":"rep-2@example.test","name":"Rep 2","role_key":"sales_rep","organization_id":"department-2","manager_user_id":"director","initial_password":"` + actorSecret + `"}]}`
	manifest := manifestmodel.ManifestSchema{Roles: []manifestmodel.RoleSchema{
		{Key: "sales_director", Name: "Sales Director", Audience: "user", ProvisionToWorkspaces: true, Permissions: []manifestmodel.RolePermission{{PermissionKey: "lead.read", DataScope: identitysdk.DataScopeAll}}},
		{Key: "sales_rep", Name: "Sales Representative", Audience: "user", ProvisionToWorkspaces: true, Permissions: []manifestmodel.RolePermission{{PermissionKey: "lead.read", DataScope: identitysdk.DataScopeOrg}}},
	}}
	database, err := bootstrap.PrepareProjectDatabase(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.CloseContext(t.Context()) })
	manager, err := newProjectTenantManager(
		t.Context(), cfg, identitymodule.NewFactory(identitymodule.Options{IdentityVersion: "test", DatabaseDriver: "sqlite", DatabasePath: cfg.DBPath}),
		database, projectIdentityDatabaseHandle(database, cfg.DBPath, nil),
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close(t.Context()) })
	if err := manager.Activate(t.Context(), manifest); err != nil {
		t.Fatal(err)
	}
	workspaceID := manager.Config().IdentityWorkspaceID
	for table, want := range map[string]int{
		"_tenant_installation": 1, "_tenant_registry": 1, "_workspaces": 1,
		"_identity_organization_units": 2,
	} {
		var count int
		query := `SELECT COUNT(*) FROM "` + table + `"`
		arguments := []any{}
		if strings.HasPrefix(table, "_identity_") {
			query += ` WHERE "workspace_id" = ?`
			arguments = append(arguments, workspaceID)
		}
		if err := database.DB().QueryRowContext(t.Context(), query, arguments...).Scan(&count); err != nil || count != want {
			t.Fatalf("%s count=%d want=%d err=%v", table, count, want, err)
		}
	}
	var projectRoleCount int
	if err := database.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM "_identity_roles" WHERE "workspace_id" = ? AND "role_key" IN (?, ?)`, workspaceID, "sales_director", "sales_rep").Scan(&projectRoleCount); err != nil || projectRoleCount != 2 {
		t.Fatalf("project role count=%d want=2 err=%v", projectRoleCount, err)
	}
	for _, table := range []string{"_identity_users", "_identity_credentials"} {
		var acceptancePrincipalCount int
		userColumn := "id"
		if table == "_identity_credentials" {
			userColumn = "user_id"
		}
		if err := database.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM "`+table+`" WHERE "workspace_id" = ? AND "`+userColumn+`" IN (?, ?, ?, ?)`, workspaceID, "admin", "director", "rep-1", "rep-2").Scan(&acceptancePrincipalCount); err != nil || acceptancePrincipalCount != 4 {
			t.Fatalf("%s acceptance principal count=%d want=4 err=%v", table, acceptancePrincipalCount, err)
		}
	}
	var acceptanceAssignmentCount int
	if err := database.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM "_identity_user_role_assignments" WHERE "workspace_id" = ? AND "user_id" IN (?, ?, ?) AND "source" = ?`, workspaceID, "director", "rep-1", "rep-2", "runtime_acceptance_fixture").Scan(&acceptanceAssignmentCount); err != nil || acceptanceAssignmentCount != 3 {
		t.Fatalf("acceptance assignment count=%d want=3 err=%v", acceptanceAssignmentCount, err)
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	configJSON, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(manifestJSON), actorSecret) || strings.Contains(string(configJSON), actorSecret) || strings.Contains(string(configJSON), "INITIAL_ACCEPTANCE_FIXTURES") {
		t.Fatalf("acceptance credential entered persistent/public configuration: manifest=%s config=%s", manifestJSON, configJSON)
	}
	references, err := manager.BusinessSeedReferenceCandidates()
	if err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, reference := range references {
		if reference.WorkspaceID != workspaceID || reference.RecordID == "" || reference.SourceKind == "" {
			t.Fatalf("unscoped baseline reference candidate=%+v", reference)
		}
		found[reference.TargetObjectKey+"/"+reference.RecordID] = true
	}
	for _, key := range []string{
		"identity_user/admin", "identity_user/director", "identity_user/rep-1", "identity_user/rep-2",
		"identity_organization_unit/department-1", "identity_organization_unit/department-2",
	} {
		if !found[key] {
			t.Fatalf("missing committed fixture reference %s in %+v", key, references)
		}
	}
}

func TestRuntimeHostPassesCommittedAcceptanceReferenceCandidatesIntoStartup(t *testing.T) {
	cfg := serverTestConfig()
	cfg.InitialAcceptanceFixtures = `{"organizations":[{"id":"department-2","code":"department-2","name":"Department 2"},{"id":"department-1","code":"department-1","name":"Department 1"}],"actors":[{"id":"director","login_id":"director@example.test","name":"Director","role_key":"sales_director","initial_password":"ActorPassword1!"},{"id":"rep-1","login_id":"rep-1@example.test","name":"Rep 1","role_key":"sales_rep","organization_id":"department-1","manager_user_id":"director","initial_password":"ActorPassword1!"}]}`
	runtime := &serverRuntimeFake{}
	dependencies := serverTestDependencies(t, cfg, runtime)
	createRuntime := dependencies.newRuntime
	var captured []bootstrap.BusinessSeedReferenceCandidate
	dependencies.newRuntime = func(ctx context.Context, runtimeConfig config.Config, handlers *runtimeext.BusinessHandlerRegistry, connectors *connector.Registry, release runtimehttp.RuntimeReleaseIdentity, evidence bootstrap.RuntimeReleaseArtifactEvidence, identity identitysdk.Binding, notification notificationsdk.Factory, monitoring monitoringsdk.Factory, scheduler schedulersdk.Factory, dataExchange dataexchangesdk.Factory, agent agentsdk.Factory, integration integrationsdk.Factory, report reportsdk.Factory, database *bootstrap.ProjectDatabase, references []bootstrap.BusinessSeedReferenceCandidate) runtimeProcess {
		captured = append([]bootstrap.BusinessSeedReferenceCandidate(nil), references...)
		return createRuntime(ctx, runtimeConfig, handlers, connectors, release, evidence, identity, notification, monitoring, scheduler, dataExchange, agent, integration, report, database, references)
	}
	if err := runWithDependencies(validOptions(), dependencies); err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	workspaceID := ""
	for _, reference := range captured {
		if workspaceID == "" {
			workspaceID = reference.WorkspaceID
		}
		if reference.WorkspaceID == "" || reference.WorkspaceID != workspaceID {
			t.Fatalf("mixed or empty candidate workspace: %+v", captured)
		}
		found[reference.TargetObjectKey+"/"+reference.RecordID] = true
	}
	for _, key := range []string{"identity_user/admin", "identity_user/director", "identity_user/rep-1", "identity_organization_unit/department-1", "identity_organization_unit/department-2"} {
		if !found[key] {
			t.Fatalf("host omitted candidate %s from %+v", key, captured)
		}
	}
}

func TestResolveInstallationConfigAlignsNotificationWithIdentityPrincipalScope(t *testing.T) {
	installation := workspaceprovision.Installation{
		TenantRegistryID: "tenant-registry-primary",
		WorkspaceID:      "workspace-primary",
	}
	resolved, err := resolveInstallationConfig(config.Config{}, installation)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.IdentityWorkspaceID != installation.WorkspaceID || resolved.NotificationWorkspaceID != installation.WorkspaceID || resolved.NotificationTenantID != installation.WorkspaceID {
		t.Fatalf("resolved application scope=%+v", resolved)
	}

	_, err = resolveInstallationConfig(config.Config{NotificationTenantID: installation.TenantRegistryID}, installation)
	if err == nil || !strings.Contains(err.Error(), "NOTIFICATION_TENANT_ID") {
		t.Fatalf("expected stale tenant-registry notification scope to be rejected, got %v", err)
	}
}

func TestProjectTenantManagerKeepsTenantBindingsClosedUntilAtomicInitialization(t *testing.T) {
	cfg := serverTestConfig()
	cfg.DatabaseDriver = "sqlite"
	cfg.DBPath = filepath.Join(t.TempDir(), "tenant-manager.db")
	database, err := bootstrap.PrepareProjectDatabase(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.CloseContext(t.Context()) })

	manager, err := newProjectTenantManager(t.Context(), cfg, identityFactoryStub{}, database, projectIdentityDatabaseHandle(database, cfg.DBPath, nil))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close(t.Context()) })
	if manager.Binding() != nil || len(manager.Adapters()) != 0 {
		t.Fatal("ordinary tenant Identity surfaces opened before initialization")
	}
	if _, found, err := workspaceprovision.LoadInstallation(t.Context(), database); err != nil || found {
		t.Fatalf("fresh installation found=%v err=%v", found, err)
	}

	if err := manager.Activate(t.Context(), manifestmodel.ManifestSchema{}); err != nil {
		t.Fatal(err)
	}
	if manager.Binding() == nil || manager.Config().IdentityWorkspaceID == "" || manager.Config().IdentityWorkspaceID == "default" {
		t.Fatalf("initialized manager config=%+v binding=%#v", manager.Config(), manager.Binding())
	}
	installation, found, err := workspaceprovision.LoadInstallation(t.Context(), database)
	if err != nil || !found || installation.WorkspaceID != manager.Config().IdentityWorkspaceID {
		t.Fatalf("installation=%+v found=%v err=%v", installation, found, err)
	}
	if err := manager.Activate(t.Context(), manifestmodel.ManifestSchema{}); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"_tenant_installation", "_tenant_registry", "_workspaces"} {
		var count int
		if err := database.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+database.TableIdentifier(table)).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s count=%d err=%v", table, count, err)
		}
	}
}

func TestProjectTenantManagerMissingPasswordLeavesNoTenantRows(t *testing.T) {
	cfg := serverTestConfig()
	cfg.InitialManagementPassword = ""
	cfg.DatabaseDriver = "sqlite"
	cfg.DBPath = filepath.Join(t.TempDir(), "tenant-manager-missing-password.db")
	database, err := bootstrap.PrepareProjectDatabase(t.Context(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.CloseContext(t.Context()) })
	manager, err := newProjectTenantManager(t.Context(), cfg, identityFactoryStub{}, database, projectIdentityDatabaseHandle(database, cfg.DBPath, nil))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Close(t.Context()) })

	err = manager.Activate(t.Context(), manifestmodel.ManifestSchema{})
	if err == nil || !strings.Contains(err.Error(), "INITIAL_MANAGEMENT_PASSWORD") {
		t.Fatalf("activation error=%v", err)
	}
	for _, table := range []string{"_tenant_installation", "_tenant_registry", "_workspaces", "_workspace_configuration", "_workspace_provisioning_receipts"} {
		var count int
		if err := database.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+database.TableIdentifier(table)).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s count=%d err=%v", table, count, err)
		}
	}
}
