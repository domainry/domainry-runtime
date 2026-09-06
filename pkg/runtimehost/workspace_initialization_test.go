package runtimehost

import (
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	identitymodule "github.com/domainry/domainry-identity/module"
	"github.com/domainry/domainry-runtime/runtime/bootstrap"
	runtimebootstrap "github.com/domainry/domainry-runtime/runtime/bootstrap/runtime"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workspaceprovision"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

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

func TestWorkspaceManagerV3PublishesExactFixedRolesAndNoLegacyFixtureGraph(t *testing.T) {
	cfg := serverTestConfig()
	cfg.DatabaseDriver = "sqlite"
	cfg.DBPath = filepath.Join(t.TempDir(), "workspace-bootstrap.db")
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
	manifest := manifestmodel.ManifestSchema{Roles: workspaceRolesForTest()}
	if err := manager.Activate(t.Context(), manifest, nil); err != nil {
		t.Fatal(err)
	}
	workspaceID := manager.Config().IdentityWorkspaceID
	boundCatalog, err := runtimebootstrap.RuntimeWorkspaceProjectRoleCatalog(manifest.Objects, manifest.Roles, workspaceID, cfg.IdentityAudience)
	if err != nil {
		t.Fatal(err)
	}
	publisher, ok := manager.Binding().(identitysdk.ProjectRoleCatalogPublisher)
	if !ok {
		t.Fatal("initialized Identity binding cannot publish the exact-four role catalog")
	}
	receipt, err := publisher.PublishProjectRoles(t.Context(), boundCatalog)
	if err != nil || receipt.Published != len(workspaceRolesForTest()) {
		t.Fatalf("post-bind role publication receipt=%+v err=%v", receipt, err)
	}
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
	sort.Strings(roleKeys)
	wantBootstrapRoles := []string{
		identitysdk.WorkspaceBootstrapRoleHeadquartersAdmin,
		identitysdk.WorkspaceBootstrapRoleStaff,
		identitysdk.WorkspaceBootstrapRoleStoreManager,
		identitysdk.WorkspaceBootstrapRoleTenantAdmin,
	}
	sort.Strings(wantBootstrapRoles)
	var actualBootstrapRoles []string
	for _, roleKey := range roleKeys {
		for _, expected := range wantBootstrapRoles {
			if roleKey == expected {
				actualBootstrapRoles = append(actualBootstrapRoles, roleKey)
			}
		}
	}
	if !reflect.DeepEqual(actualBootstrapRoles, wantBootstrapRoles) {
		t.Fatalf("bootstrap role keys=%v want=%v all=%v", actualBootstrapRoles, wantBootstrapRoles, roleKeys)
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
	var platformAssignments int
	if err := database.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM "_identity_user_role_assignments" a JOIN "_identity_roles" r ON r."workspace_id" = a."workspace_id" AND r."id" = a."role_id" WHERE a."workspace_id" = ? AND a."user_id" = ? AND r."role_key" = ?`, workspaceID, initialAdministratorID, identitysdk.WorkspaceBootstrapRoleTenantAdmin).Scan(&platformAssignments); err != nil || platformAssignments != 0 {
		t.Fatalf("platform administrator assignments=%d err=%v", platformAssignments, err)
	}
	var headquartersAssignments, otherAssignments int
	if err := database.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM "_identity_user_role_assignments" a JOIN "_identity_roles" r ON r."workspace_id" = a."workspace_id" AND r."id" = a."role_id" WHERE a."workspace_id" = ? AND a."user_id" = ? AND r."role_key" = ?`, workspaceID, initialAdministratorID, identitysdk.WorkspaceBootstrapRoleHeadquartersAdmin).Scan(&headquartersAssignments); err != nil || headquartersAssignments != 1 {
		t.Fatalf("headquarters administrator assignments=%d initial_admin=%q err=%v", headquartersAssignments, initialAdministratorID, err)
	}
	if err := database.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM "_identity_user_role_assignments" a JOIN "_identity_roles" r ON r."workspace_id" = a."workspace_id" AND r."id" = a."role_id" WHERE a."workspace_id" = ? AND a."user_id" = ? AND r."role_key" <> ?`, workspaceID, initialAdministratorID, identitysdk.WorkspaceBootstrapRoleHeadquartersAdmin).Scan(&otherAssignments); err != nil || otherAssignments != 0 {
		t.Fatalf("initial administrator received another role: count=%d err=%v", otherAssignments, err)
	}
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
	if err := manager.Activate(t.Context(), manifestmodel.ManifestSchema{Roles: workspaceRolesForTest()}, nil); err != nil {
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
	restarted, err := newProjectWorkspaceManager(t.Context(), cfg, identityFactoryStub{}, reopened, projectIdentityDatabaseHandle(reopened, cfg.DBPath, nil), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close(t.Context()) })
	if restarted.Binding() == nil || restarted.Config().IdentityWorkspaceID != installation.WorkspaceID {
		t.Fatalf("restart config=%+v", restarted.Config())
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
