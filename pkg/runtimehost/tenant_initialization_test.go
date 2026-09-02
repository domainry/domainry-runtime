package runtimehost

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/domainry/domainry-runtime/runtime/bootstrap"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workspaceprovision"
)

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
	if manager.Binding() != nil || len(manager.Surfaces()) != 0 {
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
	cfg.InitialTenantAdminPassword = ""
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
	if err == nil || !strings.Contains(err.Error(), "INITIAL_TENANT_ADMIN_PASSWORD") {
		t.Fatalf("activation error=%v", err)
	}
	for _, table := range []string{"_tenant_installation", "_tenant_registry", "_workspaces", "_workspace_configuration", "_workspace_provisioning_receipts"} {
		var count int
		if err := database.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+database.TableIdentifier(table)).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s count=%d err=%v", table, count, err)
		}
	}
}
