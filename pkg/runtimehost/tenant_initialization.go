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
	runtimebootstrap "github.com/domainry/domainry-runtime/runtime/bootstrap/runtime"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	workspaceprovisionmodel "github.com/domainry/domainry-runtime/runtime/domain/workspaceprovision/model"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workspaceprovision"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

type projectTenantManager struct {
	mu                              sync.Mutex
	cfg                             config.Config
	factory                         identitysdk.Factory
	database                        *bootstrap.ProjectDatabase
	handle                          identitysdk.DatabaseHandle
	bootstrap                       identitysdk.BootstrapBinding
	binding                         identitysdk.Binding
	adapters                        []identityhttpapi.Adapter
	businessSeedReferenceCandidates []bootstrap.BusinessSeedReferenceCandidate
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
	request, password, organizations, actors, err := initialTenantRequest(manager.cfg)
	if err != nil {
		return err
	}
	if err := manager.bootstrap.BindBootstrapProjectRoleCatalog(ctx, runtimebootstrap.RuntimeProjectRoleCatalog(manifest.Objects, manifest.Roles, "", manager.cfg.IdentityAudience)); err != nil {
		return fmt.Errorf("bind project roles for initial tenant: %w", err)
	}
	result, err := workspaceprovision.NewTenantInitializationStore(manager.database, manager.bootstrap, manifest).InitializeWithAcceptanceFixtures(ctx, request, password, organizations, actors)
	if err != nil {
		return fmt.Errorf("initialize first tenant atomically: %w", err)
	}
	if err := manager.bootstrap.Close(context.WithoutCancel(ctx)); err != nil {
		return fmt.Errorf("close Identity bootstrap after initial tenant: %w", err)
	}
	manager.bootstrap = nil
	manager.businessSeedReferenceCandidates = acceptanceBusinessSeedReferenceCandidates(result.WorkspaceID, organizations, actors)
	installation, found, err := workspaceprovision.LoadInstallation(ctx, manager.database)
	if err != nil {
		return fmt.Errorf("verify committed initial tenant: %w", err)
	}
	if !found || installation.WorkspaceID != result.WorkspaceID || installation.TenantRegistryID != result.TenantRegistryID {
		return fmt.Errorf("verify committed initial tenant: committed marker does not match transaction result")
	}
	return manager.bindInitializedIdentity(ctx, installation)
}

func (manager *projectTenantManager) BusinessSeedReferenceCandidates() ([]bootstrap.BusinessSeedReferenceCandidate, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if len(manager.businessSeedReferenceCandidates) != 0 {
		return append([]bootstrap.BusinessSeedReferenceCandidate(nil), manager.businessSeedReferenceCandidates...), nil
	}
	organizations, actors, err := decodeInitialAcceptanceFixtures(manager.cfg.InitialAcceptanceFixtures)
	if err != nil {
		return nil, err
	}
	manager.businessSeedReferenceCandidates = acceptanceBusinessSeedReferenceCandidates(manager.cfg.IdentityWorkspaceID, organizations, actors)
	return append([]bootstrap.BusinessSeedReferenceCandidate(nil), manager.businessSeedReferenceCandidates...), nil
}

func acceptanceBusinessSeedReferenceCandidates(workspaceID string, organizations []identitysdk.WorkspaceAcceptanceOrganization, actors []identitysdk.WorkspaceAcceptanceActor) []bootstrap.BusinessSeedReferenceCandidate {
	workspaceID = strings.TrimSpace(workspaceID)
	candidates := []bootstrap.BusinessSeedReferenceCandidate{{
		WorkspaceID: workspaceID, TargetObjectKey: "identity_user", RecordID: "admin", SourceKind: "runtime_initial_administrator",
	}}
	for _, organization := range organizations {
		candidates = append(candidates, bootstrap.BusinessSeedReferenceCandidate{
			WorkspaceID: workspaceID, TargetObjectKey: "identity_organization_unit",
			RecordID: strings.TrimSpace(organization.ID), SourceKind: "runtime_acceptance_fixture",
		})
	}
	for _, actor := range actors {
		candidates = append(candidates, bootstrap.BusinessSeedReferenceCandidate{
			WorkspaceID: workspaceID, TargetObjectKey: "identity_user",
			RecordID: strings.TrimSpace(actor.ID), SourceKind: "runtime_acceptance_fixture",
		})
		if organizationID := strings.TrimSpace(actor.OrganizationID); organizationID != "" {
			candidates = append(candidates, bootstrap.BusinessSeedReferenceCandidate{
				WorkspaceID: workspaceID, TargetObjectKey: "identity_organization_unit",
				RecordID: organizationID, SourceKind: "runtime_acceptance_actor_graph",
			})
		}
	}
	return candidates
}

func initialTenantRequest(cfg config.Config) (workspaceprovisionmodel.Request, string, []identitysdk.WorkspaceAcceptanceOrganization, []identitysdk.WorkspaceAcceptanceActor, error) {
	configuration := map[string]any{}
	rawConfiguration := strings.TrimSpace(cfg.InitialTenantStoreConfiguration)
	if rawConfiguration == "" {
		rawConfiguration = "{}"
	}
	if err := json.Unmarshal([]byte(rawConfiguration), &configuration); err != nil {
		return workspaceprovisionmodel.Request{}, "", nil, nil, fmt.Errorf("INITIAL_TENANT_STORE_CONFIGURATION must be a JSON object: %w", err)
	}
	if configuration == nil {
		return workspaceprovisionmodel.Request{}, "", nil, nil, fmt.Errorf("INITIAL_TENANT_STORE_CONFIGURATION must be a JSON object")
	}
	password := strings.TrimSpace(cfg.InitialManagementPassword)
	if password == "" {
		return workspaceprovisionmodel.Request{}, "", nil, nil, fmt.Errorf("INITIAL_MANAGEMENT_PASSWORD or INITIAL_MANAGEMENT_PASSWORD_FILE is required before tenant initialization")
	}
	organizations, actors, err := decodeInitialAcceptanceFixtures(cfg.InitialAcceptanceFixtures)
	if err != nil {
		return workspaceprovisionmodel.Request{}, "", nil, nil, err
	}
	return workspaceprovisionmodel.Request{
		RequestID: strings.TrimSpace(cfg.InitialTenantRequestID), TenantCode: strings.TrimSpace(cfg.InitialTenantCode),
		TenantName: strings.TrimSpace(cfg.InitialTenantName), AdminLoginID: strings.TrimSpace(cfg.InitialManagementLoginID),
		AdminName: strings.TrimSpace(cfg.InitialManagementName), StoreConfiguration: configuration,
	}, password, organizations, actors, nil
}

func decodeInitialAcceptanceFixtures(raw string) ([]identitysdk.WorkspaceAcceptanceOrganization, []identitysdk.WorkspaceAcceptanceActor, error) {
	fixtures := struct {
		Organizations []identitysdk.WorkspaceAcceptanceOrganization `json:"organizations"`
		Actors        []struct {
			ID              string `json:"id"`
			LoginID         string `json:"login_id"`
			Name            string `json:"name"`
			RoleKey         string `json:"role_key"`
			OrganizationID  string `json:"organization_id"`
			ManagerUserID   string `json:"manager_user_id"`
			InitialPassword string `json:"initial_password"`
		} `json:"actors"`
	}{}
	if raw = strings.TrimSpace(raw); raw != "" {
		if err := json.Unmarshal([]byte(raw), &fixtures); err != nil {
			return nil, nil, fmt.Errorf("INITIAL_ACCEPTANCE_FIXTURES must be a valid managed fixture envelope: %w", err)
		}
	}
	actors := make([]identitysdk.WorkspaceAcceptanceActor, 0, len(fixtures.Actors))
	for _, actor := range fixtures.Actors {
		actors = append(actors, identitysdk.WorkspaceAcceptanceActor{
			ID: actor.ID, LoginID: actor.LoginID, Name: actor.Name, RoleKey: actor.RoleKey,
			OrganizationID: actor.OrganizationID, ManagerUserID: actor.ManagerUserID, InitialPassword: actor.InitialPassword,
		})
	}
	return fixtures.Organizations, actors, nil
}

func (manager *projectTenantManager) bindInitializedIdentity(ctx context.Context, installation workspaceprovision.Installation) error {
	cfg, err := resolveInstallationConfig(manager.cfg, installation)
	if err != nil {
		return err
	}
	binding, adapters, err := openProjectIdentity(ctx, cfg, manager.factory, manager.handle)
	if err != nil {
		return err
	}
	manager.cfg, manager.binding, manager.adapters = cfg, binding, adapters
	return nil
}

func resolveInstallationConfig(cfg config.Config, installation workspaceprovision.Installation) (config.Config, error) {
	expected := map[string]string{
		"IDENTITY_WORKSPACE_ID": installation.WorkspaceID,
		// Embedded Identity currently issues browser access bundles with both
		// tenant_id and workspace_id set to the initialized workspace ID. The
		// embedded Notification binding validates both fields against its
		// application scope, so it must use the same principal scope. The
		// tenant registry ID remains the Runtime provisioning identity; it is
		// not the tenant claim exposed by embedded Identity sessions.
		"NOTIFICATION_TENANT_ID":    installation.WorkspaceID,
		"NOTIFICATION_WORKSPACE_ID": installation.WorkspaceID,
	}
	configured := map[string]string{
		"IDENTITY_WORKSPACE_ID":     cfg.IdentityWorkspaceID,
		"NOTIFICATION_TENANT_ID":    cfg.NotificationTenantID,
		"NOTIFICATION_WORKSPACE_ID": cfg.NotificationWorkspaceID,
	}
	for name, actual := range configured {
		if actual = strings.TrimSpace(actual); actual != "" && actual != expected[name] {
			return config.Config{}, fmt.Errorf("%s=%q conflicts with initialized tenant %q", name, actual, expected[name])
		}
	}
	cfg.IdentityWorkspaceID = installation.WorkspaceID
	cfg.NotificationTenantID, cfg.NotificationWorkspaceID = installation.WorkspaceID, installation.WorkspaceID
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

func (manager *projectTenantManager) Adapters() []identityhttpapi.Adapter {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return append([]identityhttpapi.Adapter(nil), manager.adapters...)
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
