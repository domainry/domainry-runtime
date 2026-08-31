package runtimehost

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	identityhttpapi "github.com/domainry/domainry-identity-sdk/httpapi"
	"github.com/domainry/domainry-runtime/runtime/bootstrap"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	workspaceprovisionmodel "github.com/domainry/domainry-runtime/runtime/domain/workspaceprovision/model"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workspaceprovision"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

type projectTenantManager struct {
	mu        sync.Mutex
	cfg       config.Config
	factory   identitysdk.Factory
	database  *bootstrap.ProjectDatabase
	handle    identitysdk.DatabaseHandle
	bootstrap identitysdk.BootstrapBinding
	binding   identitysdk.Binding
	surfaces  []identityhttpapi.Surface
}

func newProjectTenantManager(ctx context.Context, cfg config.Config, factory identitysdk.Factory, database *bootstrap.ProjectDatabase, handle identitysdk.DatabaseHandle) (*projectTenantManager, error) {
	manager := &projectTenantManager{cfg: cfg, factory: factory, database: database, handle: handle}
	installation, found, err := workspaceprovision.LoadInstallation(ctx, database)
	if err != nil {
		return nil, err
	}
	if found {
		if err := manager.bindInitializedIdentity(ctx, installation); err != nil {
			return nil, err
		}
		return manager, nil
	}
	bootstrapFactory, ok := factory.(identitysdk.BootstrapDatabaseFactory)
	if !ok {
		return nil, fmt.Errorf("initial tenant requires an embedded Identity BootstrapDatabaseFactory")
	}
	bootstrapBinding, err := bootstrapFactory.OpenBootstrapWithDatabase(ctx, identitysdk.ApplicationKey(cfg.IdentityAudience), handle)
	if err != nil {
		return nil, fmt.Errorf("open initial tenant Identity bootstrap: %w", err)
	}
	if bootstrapBinding == nil {
		return nil, fmt.Errorf("Identity bootstrap factory returned no binding")
	}
	manager.bootstrap = bootstrapBinding
	return manager, nil
}

func (manager *projectTenantManager) Activate(ctx context.Context, manifest manifestmodel.ManifestSchema) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.binding != nil {
		return nil
	}
	if manager.bootstrap == nil {
		return fmt.Errorf("initial tenant bootstrap is unavailable")
	}
	if err := manager.database.EnsureRuntimeSchema(ctx); err != nil {
		return fmt.Errorf("prepare application schema before initial tenant: %w", err)
	}
	request, password, err := initialTenantRequest(manager.cfg)
	if err != nil {
		return err
	}
	result, err := workspaceprovision.NewTenantInitializationStore(manager.database, manager.bootstrap, manifest).Initialize(ctx, request, password)
	if err != nil {
		return fmt.Errorf("initialize first tenant atomically: %w", err)
	}
	if err := manager.bootstrap.Close(context.WithoutCancel(ctx)); err != nil {
		return fmt.Errorf("close Identity bootstrap after initial tenant: %w", err)
	}
	manager.bootstrap = nil
	installation, found, err := workspaceprovision.LoadInstallation(ctx, manager.database)
	if err != nil {
		return fmt.Errorf("verify committed initial tenant: %w", err)
	}
	if !found || installation.WorkspaceID != result.WorkspaceID || installation.TenantRegistryID != result.TenantRegistryID {
		return fmt.Errorf("verify committed initial tenant: committed marker does not match transaction result")
	}
	return manager.bindInitializedIdentity(ctx, installation)
}

func initialTenantRequest(cfg config.Config) (workspaceprovisionmodel.Request, string, error) {
	configuration := map[string]any{}
	rawConfiguration := strings.TrimSpace(cfg.InitialTenantStoreConfiguration)
	if rawConfiguration == "" {
		rawConfiguration = "{}"
	}
	if err := json.Unmarshal([]byte(rawConfiguration), &configuration); err != nil {
		return workspaceprovisionmodel.Request{}, "", fmt.Errorf("INITIAL_TENANT_STORE_CONFIGURATION must be a JSON object: %w", err)
	}
	if configuration == nil {
		return workspaceprovisionmodel.Request{}, "", fmt.Errorf("INITIAL_TENANT_STORE_CONFIGURATION must be a JSON object")
	}
	password := strings.TrimSpace(cfg.InitialTenantAdminPassword)
	if password == "" {
		return workspaceprovisionmodel.Request{}, "", fmt.Errorf("INITIAL_TENANT_ADMIN_PASSWORD or INITIAL_TENANT_ADMIN_PASSWORD_FILE is required before tenant initialization")
	}
	return workspaceprovisionmodel.Request{
		RequestID: strings.TrimSpace(cfg.InitialTenantRequestID), TenantCode: strings.TrimSpace(cfg.InitialTenantCode),
		TenantName: strings.TrimSpace(cfg.InitialTenantName), AdminLoginID: strings.TrimSpace(cfg.InitialTenantAdminLoginID),
		AdminName: strings.TrimSpace(cfg.InitialTenantAdminName), StoreConfiguration: configuration,
	}, password, nil
}

func (manager *projectTenantManager) bindInitializedIdentity(ctx context.Context, installation workspaceprovision.Installation) error {
	cfg, err := resolveInstallationConfig(manager.cfg, installation)
	if err != nil {
		return err
	}
	binding, surfaces, err := openProjectIdentity(ctx, cfg, manager.factory, manager.handle)
	if err != nil {
		return err
	}
	manager.cfg, manager.binding, manager.surfaces = cfg, binding, surfaces
	return nil
}

func resolveInstallationConfig(cfg config.Config, installation workspaceprovision.Installation) (config.Config, error) {
	expected := map[string]string{
		"IDENTITY_WORKSPACE_ID":     installation.WorkspaceID,
		"NOTIFICATION_TENANT_ID":    installation.TenantRegistryID,
		"NOTIFICATION_WORKSPACE_ID": installation.WorkspaceID,
		"PARTY_TENANT_ID":           installation.TenantRegistryID,
		"PARTY_WORKSPACE_ID":        installation.WorkspaceID,
	}
	configured := map[string]string{
		"IDENTITY_WORKSPACE_ID":     cfg.IdentityWorkspaceID,
		"NOTIFICATION_TENANT_ID":    cfg.NotificationTenantID,
		"NOTIFICATION_WORKSPACE_ID": cfg.NotificationWorkspaceID,
		"PARTY_TENANT_ID":           cfg.PartyTenantID,
		"PARTY_WORKSPACE_ID":        cfg.PartyWorkspaceID,
	}
	for name, actual := range configured {
		if actual = strings.TrimSpace(actual); actual != "" && actual != expected[name] {
			return config.Config{}, fmt.Errorf("%s=%q conflicts with initialized tenant %q", name, actual, expected[name])
		}
	}
	cfg.IdentityWorkspaceID = installation.WorkspaceID
	cfg.NotificationTenantID, cfg.NotificationWorkspaceID = installation.TenantRegistryID, installation.WorkspaceID
	cfg.PartyTenantID, cfg.PartyWorkspaceID = installation.TenantRegistryID, installation.WorkspaceID
	return cfg, nil
}

func (manager *projectTenantManager) Config() config.Config {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.cfg
}

func (manager *projectTenantManager) Binding() identitysdk.Binding {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.binding
}

func (manager *projectTenantManager) Surfaces() []identityhttpapi.Surface {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return append([]identityhttpapi.Surface(nil), manager.surfaces...)
}

func (manager *projectTenantManager) Close(ctx context.Context) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.binding != nil {
		return manager.binding.Close(ctx)
	}
	if manager.bootstrap != nil {
		return manager.bootstrap.Close(ctx)
	}
	return nil
}
