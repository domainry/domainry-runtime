package runtimehost

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	identityprincipal "github.com/domainry/domainry-identity-sdk/authorization/principal"
	identityhttpapi "github.com/domainry/domainry-identity-sdk/httpapi"
	identitymodulehost "github.com/domainry/domainry-identity-sdk/modulehost"
	"github.com/domainry/domainry-orm/query"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	"github.com/domainry/domainry-runtime/runtime/bootstrap"
	runtimebootstrap "github.com/domainry/domainry-runtime/runtime/bootstrap/runtime"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workspaceprovisionmodel "github.com/domainry/domainry-runtime/runtime/domain/workspaceprovision/model"
	appschemapersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/appschema"
	"github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workspaceprovision"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

type projectWorkspaceManager struct {
	mu                              sync.Mutex
	cfg                             config.Config
	factory                         identitysdk.Factory
	database                        *bootstrap.ProjectDatabase
	handle                          identitysdk.DatabaseHandle
	bootstrap                       identitysdk.BootstrapBinding
	binding                         identitysdk.Binding
	adapters                        []identityhttpapi.Adapter
	businessSeedReferenceCandidates []bootstrap.BusinessSeedReferenceCandidate
	bootstrapRoleCatalog            identitysdk.ProjectRoleCatalog
	projectNavigationCatalog        identitysdk.ProjectNavigationCatalog
	credentialDelivery              InitialWorkspaceCredentialDelivery
	installationAdminDelivery       InstallationAdministratorCredentialDelivery
}

func newProjectWorkspaceManager(ctx context.Context, cfg config.Config, factory identitysdk.Factory, database *bootstrap.ProjectDatabase, handle identitysdk.DatabaseHandle, delivery InitialWorkspaceCredentialDelivery, installationAdminDelivery ...InstallationAdministratorCredentialDelivery) (*projectWorkspaceManager, error) {
	emptyNavigation, err := loadProjectNavigationCatalog("", nil)
	if err != nil {
		return nil, err
	}
	manager := &projectWorkspaceManager{cfg: cfg, factory: factory, database: database, handle: handle, credentialDelivery: delivery, projectNavigationCatalog: emptyNavigation}
	if len(installationAdminDelivery) > 0 {
		manager.installationAdminDelivery = installationAdminDelivery[0]
	}
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
	if manager.credentialDelivery == nil && strings.TrimSpace(cfg.InitialWorkspaceCredentialFile) != "" {
		manager.credentialDelivery, err = NewInitialWorkspaceCredentialFileDelivery(cfg.InitialWorkspaceCredentialFile)
		if err != nil {
			return nil, err
		}
	}
	bootstrapFactory, ok := factory.(identitysdk.BootstrapDatabaseFactory)
	if !ok {
		return nil, fmt.Errorf("initial Workspace requires an embedded Identity BootstrapDatabaseFactory")
	}
	bootstrapBinding, err := bootstrapFactory.OpenBootstrapWithDatabase(ctx, identitysdk.ApplicationKey(cfg.IdentityAudience), handle)
	if err != nil {
		return nil, fmt.Errorf("open initial Workspace Identity bootstrap: %w", err)
	}
	if bootstrapBinding == nil {
		return nil, fmt.Errorf("Identity bootstrap factory returned no binding")
	}
	manager.bootstrap = bootstrapBinding
	return manager, nil
}

func (manager *projectWorkspaceManager) SetProjectNavigationCatalog(catalog identitysdk.ProjectNavigationCatalog) error {
	normalized, err := identitysdk.NormalizeProjectNavigationCatalog(catalog)
	if err != nil {
		return err
	}
	manager.mu.Lock()
	manager.projectNavigationCatalog = normalized
	manager.mu.Unlock()
	return nil
}

func (manager *projectWorkspaceManager) Activate(ctx context.Context, manifest manifestmodel.ManifestSchema, participant runtimeext.WorkspaceBootstrapParticipant, handlerDescriptors ...runtimeext.HandlerDescriptor) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	// Bootstrap receives only the validated Workspace-login subset. Ordinary
	// publication later receives the complete catalog, including validated
	// internal service roles.
	roleCatalog, err := runtimebootstrap.RuntimeWorkspaceBootstrapRoleCatalog(manifest.Objects, manifest.Roles, manifest.InitialWorkspaceAdministratorRole, manager.cfg.IdentityAudience, handlerDescriptors...)
	if err != nil {
		return fmt.Errorf("compile roles for initial Workspace: %w", err)
	}
	rolePolicy, err := workspaceprovision.NewWorkspaceBootstrapRolePolicyEvidence(roleCatalog, manager.projectNavigationCatalog)
	if err != nil {
		return fmt.Errorf("compile role-policy evidence for initial Workspace: %w", err)
	}
	manager.bootstrapRoleCatalog = roleCatalog
	manifest.InitialWorkspaceAdministratorRole = roleCatalog.InitialWorkspaceAdministratorRoleKey
	if manager.binding != nil {
		return manager.bindWorkspaceBootstrapCatalogs(ctx, manager.binding)
	}
	if manager.bootstrap == nil {
		return fmt.Errorf("initial Workspace bootstrap is unavailable")
	}
	if manager.credentialDelivery == nil {
		return fmt.Errorf("initial Workspace credential delivery is required before initialization")
	}
	if err := manager.bindWorkspaceBootstrapCatalogs(ctx, manager.bootstrap); err != nil {
		return err
	}
	if err := manager.database.EnsureRuntimeSchema(ctx); err != nil {
		return fmt.Errorf("prepare application schema before initial Workspace: %w", err)
	}
	applicationSchema := appschemapersistence.NewApplicationSchemaStore(manager.database)
	applicationSchemaScope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "prepare application object storage before initial Workspace")
	if err := applicationSchema.SyncManifest(ctx, applicationSchemaScope, manifest); err != nil {
		return fmt.Errorf("materialize application object storage before initial Workspace: %w", err)
	}
	request, err := initialWorkspaceRequest(manager.cfg)
	if err != nil {
		return err
	}
	initialization := workspaceprovision.NewWorkspaceInitializationStoreWithParticipant(manager.database, manager.bootstrap, manifest, participant, rolePolicy)
	result, err := initialization.Initialize(ctx, request)
	if err != nil {
		return fmt.Errorf("initialize first Workspace atomically: %w", err)
	}
	var deliveryErr error
	if result.CredentialDelivery != workspaceprovisionmodel.CredentialDelivered || strings.TrimSpace(result.InitialPassword) == "" {
		deliveryErr = &InitialWorkspaceCredentialDeliveryError{CanonicalCode: result.CanonicalCode}
	} else {
		acknowledgment, credentialErr := manager.credentialDelivery.DeliverInitialWorkspaceCredential(context.WithoutCancel(ctx), InitialWorkspaceCredential{
			CanonicalCode: result.CanonicalCode, LoginID: result.AdminLoginID,
			InitialPassword: result.InitialPassword, MustChangePassword: result.MustChangePassword,
		})
		if credentialErr != nil || !acknowledgment.Accepted {
			deliveryErr = &InitialWorkspaceCredentialDeliveryError{CanonicalCode: result.CanonicalCode, Cause: credentialErr}
		}
	}
	result.InitialPassword = ""
	if err := manager.bootstrap.Close(context.WithoutCancel(ctx)); err != nil {
		return fmt.Errorf("close Identity bootstrap after initial Workspace: %w", err)
	}
	manager.bootstrap = nil
	manager.businessSeedReferenceCandidates = []bootstrap.BusinessSeedReferenceCandidate{{
		WorkspaceID: result.WorkspaceID, TargetObjectKey: "identity_user", RecordID: result.InitialAdminUserID,
		SourceKind: "runtime_initial_administrator",
	}}
	installation, found, err := workspaceprovision.LoadInstallation(ctx, manager.database)
	if err != nil {
		return fmt.Errorf("verify committed initial Workspace: %w", err)
	}
	if !found || installation.WorkspaceID != result.WorkspaceID {
		return fmt.Errorf("verify committed initial Workspace: authority does not match transaction result")
	}
	if err := manager.bindInitializedIdentity(ctx, installation); err != nil {
		return err
	}
	return deliveryErr
}

func (manager *projectWorkspaceManager) bindWorkspaceBootstrapCatalogs(ctx context.Context, target any) error {
	if _, supportsWorkspaceBootstrap := target.(identitysdk.WorkspaceIdentityBootstrap); !supportsWorkspaceBootstrap {
		return nil
	}
	roleBinder, ok := target.(identitysdk.BootstrapProjectRoleCatalogBinder)
	if !ok {
		return fmt.Errorf("embedded Identity binding does not accept the trusted Workspace role catalog")
	}
	if err := roleBinder.BindBootstrapProjectRoleCatalog(ctx, manager.bootstrapRoleCatalog); err != nil {
		return fmt.Errorf("bind roles for Workspace bootstrap: %w", err)
	}
	navigationBinder, ok := target.(identitysdk.BootstrapProjectNavigationCatalogBinder)
	if !ok {
		return fmt.Errorf("embedded Identity binding does not accept the trusted project navigation template")
	}
	if err := navigationBinder.BindBootstrapProjectNavigationCatalog(ctx, manager.projectNavigationCatalog); err != nil {
		return fmt.Errorf("bind project navigation for Workspace bootstrap: %w", err)
	}
	return nil
}

func (manager *projectWorkspaceManager) BusinessSeedReferenceCandidates() ([]bootstrap.BusinessSeedReferenceCandidate, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return append([]bootstrap.BusinessSeedReferenceCandidate(nil), manager.businessSeedReferenceCandidates...), nil
}

func initialWorkspaceRequest(cfg config.Config) (workspaceprovisionmodel.Request, error) {
	configuration := workspaceprovisionmodel.CommercialConfiguration{}
	decoder := json.NewDecoder(bytes.NewBufferString(strings.TrimSpace(cfg.InitialWorkspaceCommercialConfigurationJSON)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&configuration); err != nil {
		return workspaceprovisionmodel.Request{}, fmt.Errorf("INITIAL_WORKSPACE_COMMERCIAL_CONFIGURATION_JSON must match the typed commercial contract: %w", err)
	}
	if err := requireJSONEOF(decoder); err != nil {
		return workspaceprovisionmodel.Request{}, fmt.Errorf("INITIAL_WORKSPACE_COMMERCIAL_CONFIGURATION_JSON must contain exactly one JSON object: %w", err)
	}
	applicationBootstrap := map[string]any{}
	applicationBootstrapJSON := strings.TrimSpace(cfg.InitialWorkspaceApplicationBootstrapJSON)
	if applicationBootstrapJSON == "" {
		applicationBootstrapJSON = "{}"
	}
	bootstrapDecoder := json.NewDecoder(bytes.NewBufferString(applicationBootstrapJSON))
	bootstrapDecoder.UseNumber()
	if err := bootstrapDecoder.Decode(&applicationBootstrap); err != nil {
		return workspaceprovisionmodel.Request{}, fmt.Errorf("INITIAL_WORKSPACE_APPLICATION_BOOTSTRAP_JSON must be one JSON object: %w", err)
	}
	if err := requireJSONEOF(bootstrapDecoder); err != nil {
		return workspaceprovisionmodel.Request{}, fmt.Errorf("INITIAL_WORKSPACE_APPLICATION_BOOTSTRAP_JSON must contain exactly one JSON object: %w", err)
	}
	return workspaceprovisionmodel.Request{
		RequestID: strings.TrimSpace(cfg.InitialWorkspaceRequestID), WorkspaceCode: strings.TrimSpace(cfg.InitialWorkspaceCode),
		WorkspaceName: strings.TrimSpace(cfg.InitialWorkspaceName), FirstStoreCode: strings.TrimSpace(cfg.InitialWorkspaceFirstStoreCode),
		FirstStoreName: strings.TrimSpace(cfg.InitialWorkspaceFirstStoreName), AdminLoginID: strings.TrimSpace(cfg.InitialWorkspaceAdminLoginID),
		AdminName: strings.TrimSpace(cfg.InitialWorkspaceAdminName), CommercialConfiguration: configuration, ApplicationBootstrap: applicationBootstrap,
	}, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return fmt.Errorf("unexpected trailing JSON value")
	} else if err != io.EOF {
		return err
	}
	return nil
}

func (manager *projectWorkspaceManager) bindInitializedIdentity(ctx context.Context, installation workspaceprovision.Installation) error {
	cfg, err := resolveInstallationConfig(manager.cfg, installation)
	if err != nil {
		return err
	}
	binding, adapters, err := openProjectIdentity(ctx, cfg, manager.factory, manager.handle)
	if err != nil {
		return err
	}
	if err := manager.ensureInstallationAdministrator(ctx, binding, installation); err != nil {
		_ = binding.Close(context.WithoutCancel(ctx))
		return err
	}
	if len(manager.bootstrapRoleCatalog.Roles) != 0 {
		if err := manager.bindWorkspaceBootstrapCatalogs(ctx, binding); err != nil {
			_ = binding.Close(context.WithoutCancel(ctx))
			return err
		}
	}
	if authority, ok := manager.handle.WorkspaceIdentityUsageAuthority.(*runtimeWorkspaceIdentityUsageAuthority); ok {
		authenticator, resolverErr := identityprincipal.NewResolver(binding, identityprincipal.Options{})
		if resolverErr != nil {
			_ = binding.Close(context.WithoutCancel(ctx))
			return fmt.Errorf("initialize Workspace identity usage authority: %w", resolverErr)
		}
		if bindErr := authority.BindAuthenticator(authenticator); bindErr != nil {
			_ = binding.Close(context.WithoutCancel(ctx))
			return fmt.Errorf("bind Workspace identity usage authority: %w", bindErr)
		}
	}
	manager.cfg, manager.binding, manager.adapters = cfg, binding, adapters
	return nil
}

func (manager *projectWorkspaceManager) ensureInstallationAdministrator(ctx context.Context, binding identitysdk.Binding, installation workspaceprovision.Installation) (err error) {
	if !manager.cfg.InstallationAdministratorBootstrapEnabled {
		return nil
	}
	provider, ok := binding.(identitysdk.EmbeddedInstallationAdministratorBootstrapBinding)
	if !ok || provider.InstallationAdministratorBootstrap() == nil {
		return fmt.Errorf("explicit installation administrator bootstrap requires an embedded Identity capability")
	}
	capability := provider.InstallationAdministratorBootstrap()
	tx, err := manager.database.DB().BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin installation administrator bootstrap: %w", err)
	}
	var receipt identitymodulehost.InstallationAdministratorBootstrapReceipt
	committed, completionAttempted := false, false
	defer func() {
		if committed {
			return
		}
		_ = tx.Rollback()
		if receipt.ReceiptID == "" || completionAttempted {
			return
		}
		completionAttempted = true
		completionErr := capability.CompleteInstallationAdministratorBootstrapV1(context.WithoutCancel(ctx), identitymodulehost.InstallationAdministratorBootstrapCompletion{
			WorkspaceID: receipt.WorkspaceID, ReceiptID: receipt.ReceiptID, Outcome: identitysdk.WorkspaceIdentityBootstrapTransactionRolledBack,
		})
		if completionErr != nil && err == nil {
			err = completionErr
		}
	}()
	receipt, err = capability.BootstrapInstallationAdministratorV1(ctx, identitymodulehost.InstallationAdministratorBootstrapRequest{
		ContractVersion: identitymodulehost.CurrentInstallationAdministratorBootstrapContractVersion,
		ContractHash:    identitymodulehost.CurrentInstallationAdministratorBootstrapContractHash,
		InvocationID:    strings.TrimSpace(manager.cfg.InstallationAdministratorRequestID), WorkspaceID: strings.TrimSpace(installation.WorkspaceID),
		LoginID: strings.TrimSpace(manager.cfg.InstallationAdministratorLoginID), Name: strings.TrimSpace(manager.cfg.InstallationAdministratorName),
	}, identitysdk.EmbeddedTransaction{Executor: tx})
	if err != nil {
		return fmt.Errorf("bootstrap installation administrator: %w", err)
	}
	if receipt.ContractVersion != identitymodulehost.CurrentInstallationAdministratorBootstrapContractVersion ||
		receipt.ContractHash != identitymodulehost.CurrentInstallationAdministratorBootstrapContractHash ||
		receipt.WorkspaceID != strings.TrimSpace(installation.WorkspaceID) || receipt.RoleKey != identitymodulehost.InstallationAdministratorRoleKey ||
		strings.TrimSpace(receipt.ReceiptID) == "" || strings.TrimSpace(receipt.UserID) == "" || strings.TrimSpace(receipt.LoginID) == "" {
		return fmt.Errorf("Identity returned an invalid installation administrator receipt")
	}
	if err = tx.Commit(); err != nil {
		return fmt.Errorf("commit installation administrator bootstrap: %w", err)
	}
	committed = true
	if receipt.Replayed {
		if receipt.CredentialDelivered {
			return nil
		}
		return &InstallationAdministratorCredentialDeliveryError{}
	}
	completionAttempted = true
	if err = capability.CompleteInstallationAdministratorBootstrapV1(context.WithoutCancel(ctx), identitymodulehost.InstallationAdministratorBootstrapCompletion{
		WorkspaceID: receipt.WorkspaceID, ReceiptID: receipt.ReceiptID, Outcome: identitysdk.WorkspaceIdentityBootstrapTransactionCommitted,
	}); err != nil {
		return &InstallationAdministratorCredentialDeliveryError{Cause: err}
	}
	credential, err := capability.ClaimInstallationAdministratorCredentialV1(ctx, identitymodulehost.InstallationAdministratorCredentialClaim{WorkspaceID: receipt.WorkspaceID, ReceiptID: receipt.ReceiptID})
	if err != nil {
		return &InstallationAdministratorCredentialDeliveryError{Cause: err}
	}
	delivery := manager.installationAdminDelivery
	if delivery == nil {
		delivery, err = NewInstallationAdministratorCredentialFileDelivery(manager.cfg.InstallationAdministratorCredentialFile)
		if err != nil {
			return &InstallationAdministratorCredentialDeliveryError{Cause: err}
		}
	}
	canonicalCode, err := manager.installationWorkspaceCanonicalCode(ctx, installation.WorkspaceID)
	if err != nil {
		return &InstallationAdministratorCredentialDeliveryError{Cause: err}
	}
	acknowledgment, deliveryErr := delivery.DeliverInstallationAdministratorCredential(context.WithoutCancel(ctx), InstallationAdministratorCredential{
		CanonicalWorkspaceCode: canonicalCode, LoginID: credential.LoginID, InitialPassword: credential.InitialPassword, MustChangePassword: credential.MustChangePassword,
	})
	credential.InitialPassword = ""
	if deliveryErr != nil || !acknowledgment.Accepted {
		return &InstallationAdministratorCredentialDeliveryError{Cause: deliveryErr}
	}
	if err := capability.AcknowledgeInstallationAdministratorCredentialDeliveryV1(context.WithoutCancel(ctx), identitymodulehost.InstallationAdministratorCredentialDeliveryAcknowledgment{
		WorkspaceID: receipt.WorkspaceID, ReceiptID: receipt.ReceiptID,
	}); err != nil {
		return &InstallationAdministratorCredentialDeliveryError{Cause: err}
	}
	return nil
}

func (manager *projectWorkspaceManager) installationWorkspaceCanonicalCode(ctx context.Context, workspaceID string) (string, error) {
	statement, arguments, err := query.NewSelectBuilder(manager.database.RuntimeRenderer(), "_workspaces").
		Columns("canonical_code").Where(query.Equal("id", strings.TrimSpace(workspaceID))).Limit(1).Build()
	if err != nil {
		return "", err
	}
	var canonicalCode string
	if err := manager.database.DB().QueryRowContext(ctx, statement, arguments...).Scan(&canonicalCode); err != nil {
		return "", err
	}
	canonicalCode = strings.TrimSpace(canonicalCode)
	if canonicalCode == "" {
		return "", fmt.Errorf("installation Workspace canonical code is unavailable")
	}
	return canonicalCode, nil
}

func resolveInstallationConfig(cfg config.Config, installation workspaceprovision.Installation) (config.Config, error) {
	expected := map[string]string{
		"IDENTITY_WORKSPACE_ID":     installation.WorkspaceID,
		"NOTIFICATION_WORKSPACE_ID": installation.WorkspaceID,
	}
	configured := map[string]string{
		"IDENTITY_WORKSPACE_ID":     cfg.IdentityWorkspaceID,
		"NOTIFICATION_WORKSPACE_ID": cfg.NotificationWorkspaceID,
	}
	for name, actual := range configured {
		if actual = strings.TrimSpace(actual); actual != "" && actual != expected[name] {
			return config.Config{}, fmt.Errorf("%s=%q conflicts with initialized Workspace %q", name, actual, expected[name])
		}
	}
	cfg.IdentityWorkspaceID = installation.WorkspaceID
	cfg.NotificationWorkspaceID = installation.WorkspaceID
	return applyLegacyNotificationWorkspaceScope(cfg, installation.WorkspaceID)
}

func (manager *projectWorkspaceManager) Config() config.Config {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.cfg
}

func (manager *projectWorkspaceManager) Binding() identitysdk.Binding {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return manager.binding
}

func (manager *projectWorkspaceManager) Adapters() []identityhttpapi.Adapter {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	return append([]identityhttpapi.Adapter(nil), manager.adapters...)
}

func (manager *projectWorkspaceManager) Close(ctx context.Context) error {
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
