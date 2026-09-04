package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"
	"os"
	"path/filepath"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentlifecycle "github.com/domainry/domainry-agent-sdk/lifecycle"

	auditsdk "github.com/domainry/domainry-audit-sdk"
	auditmoduleimpl "github.com/domainry/domainry-audit/module"
	connector "github.com/domainry/domainry-connector-sdk"
	dataexchangesdk "github.com/domainry/domainry-data-exchange-sdk"
	dataexchangemodulehost "github.com/domainry/domainry-data-exchange-sdk/modulehost"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	lifecyclemoduleimpl "github.com/domainry/domainry-lifecycle/module"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	reportsdk "github.com/domainry/domainry-report-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	auditrepository "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	recordtimerapplication "github.com/domainry/domainry-runtime/runtime/application/recordtimer"
	uploadapplication "github.com/domainry/domainry-runtime/runtime/application/upload"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	composition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	runtimeauditmodule "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/auditmodule"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	actionpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/action"
	appschemapersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/appschema"
	automationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/automation"
	automationnotification "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/automationnotification"
	deploymentpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/deployment"
	notificationpublicationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/notificationpublication"
	operationspersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/operations"
	publicationhandoffpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/publicationhandoff"
	ratelimitpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/ratelimit"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	reportpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/report"
	reportnotification "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/reportnotification"
	workflowpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workflow"
	lifecyclemodule "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/lifecyclemodule"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

// runtimeServiceAssembly is the result of wiring domain ports to adapters.
type runtimeServiceAssembly struct {
	services            *composition.RuntimeServices
	records             recordrepository.RecordRepository
	worker              workerplatform.Dependencies
	dataExchangeBinding dataexchangesdk.Binding
	lifecycleBinding    lifecyclesdk.Binding
}

type runtimeExtensionRegistries struct {
	businessHandlers             *runtimeext.BusinessHandlerRegistry
	connectorProviders           *connector.Registry
	notificationCompiler         func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
	taskNotificationCommitter    workflowapplication.WorkflowTaskNotificationCommitter
	notificationPublisher        notificationIntentPublisher
	integrationOwnerDelivery     integrationsdk.Delivery
	integrationOwnerCatalog      integrationsdk.Catalog
	integrationOwnerManagement   integrationsdk.Management
	integrationOwnerOperations   integrationsdk.Operations
	dataExchangeProviderKey      string
	dataExchangeImportProvider   dataexchangemodulehost.ImportProvider
	dataExchangeExportProvider   dataexchangemodulehost.ExportProvider
	integrationMode              integrationsdk.DeploymentMode
	notificationSubjectLifecycle lifecyclecontract.SubjectExecutionHandler
	notificationRetention        lifecyclecontract.OwnerLifecycleExecutor
	auditRepository              auditrepository.AuditRepository
	auditSubjectLifecycle        lifecyclecontract.SubjectExecutionHandler
	dataExchangeFactory          dataexchangesdk.Factory
	agentBinding                 agentsdk.Binding
	reportBinding                reportsdk.Binding
}

func assembleRuntimeServices(ctx context.Context, cfg config.Config, manifest manifestmodel.ManifestSchema, notifications composition.NotificationRenderer, store *persistence.RuntimeStore, identityProjection identitysdk.Projection, identityPrincipals identitysdk.PrincipalResolver, auditApplication *auditapplication.AuditApplicationService, workerDependencies workerplatform.Dependencies, extensionRegistries ...runtimeExtensionRegistries) (runtimeServiceAssembly, error) {
	businessHandlers := runtimeext.NewBusinessHandlerRegistry()
	connectorProviders := connector.NewRegistry()
	var notificationCompiler func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
	var taskNotificationCommitter workflowapplication.WorkflowTaskNotificationCommitter
	var notificationPublisher notificationIntentPublisher
	var integrationOwnerDelivery integrationsdk.Delivery
	var integrationOwnerCatalog integrationsdk.Catalog
	var integrationOwnerManagement integrationsdk.Management
	var integrationOwnerOperations integrationsdk.Operations
	var notificationSubjectLifecycle lifecyclecontract.SubjectExecutionHandler
	var notificationRetention lifecyclecontract.OwnerLifecycleExecutor
	var auditRepository auditrepository.AuditRepository
	var auditSubjectLifecycle lifecyclecontract.SubjectExecutionHandler
	var dataExchangeFactory dataexchangesdk.Factory
	var agentBinding agentsdk.Binding
	var reportBinding reportsdk.Binding
	var dataExchangeProviderKey string
	var dataExchangeImportProvider dataexchangemodulehost.ImportProvider
	var dataExchangeExportProvider dataexchangemodulehost.ExportProvider
	if len(extensionRegistries) > 0 && extensionRegistries[0].businessHandlers != nil {
		businessHandlers = extensionRegistries[0].businessHandlers
	} else {
		businessHandlers.Freeze()
	}
	if len(extensionRegistries) > 0 && extensionRegistries[0].connectorProviders != nil {
		connectorProviders = extensionRegistries[0].connectorProviders
	} else {
		connectorProviders.Freeze()
	}
	if len(extensionRegistries) > 0 {
		notificationCompiler = extensionRegistries[0].notificationCompiler
		taskNotificationCommitter = extensionRegistries[0].taskNotificationCommitter
		notificationPublisher = extensionRegistries[0].notificationPublisher
		integrationOwnerDelivery = extensionRegistries[0].integrationOwnerDelivery
		integrationOwnerCatalog = extensionRegistries[0].integrationOwnerCatalog
		integrationOwnerManagement = extensionRegistries[0].integrationOwnerManagement
		integrationOwnerOperations = extensionRegistries[0].integrationOwnerOperations
		notificationSubjectLifecycle = extensionRegistries[0].notificationSubjectLifecycle
		notificationRetention = extensionRegistries[0].notificationRetention
		if extensionRegistries[0].auditRepository != nil {
			auditRepository = extensionRegistries[0].auditRepository
		}
		if extensionRegistries[0].auditSubjectLifecycle != nil {
			auditSubjectLifecycle = extensionRegistries[0].auditSubjectLifecycle
		}
		dataExchangeFactory = extensionRegistries[0].dataExchangeFactory
		agentBinding = extensionRegistries[0].agentBinding
		reportBinding = extensionRegistries[0].reportBinding
		dataExchangeProviderKey = extensionRegistries[0].dataExchangeProviderKey
		dataExchangeImportProvider = extensionRegistries[0].dataExchangeImportProvider
		dataExchangeExportProvider = extensionRegistries[0].dataExchangeExportProvider
	}
	if auditRepository == nil || auditSubjectLifecycle == nil {
		binding, err := auditmoduleimpl.NewFactory(auditmoduleimpl.Options{}).OpenModule(ctx,
			auditsdk.ApplicationRef{InstallationID: valueOrDefault(manifest.TemplateID, "domainry-runtime")}, runtimeauditmodule.NewHost(store))
		if err != nil {
			return runtimeServiceAssembly{}, fmt.Errorf("open Audit module: %w", err)
		}
		if auditRepository == nil {
			auditRepository = runtimeauditmodule.NewAuditStore(binding)
		}
		if auditSubjectLifecycle == nil {
			auditSubjectLifecycle = runtimeauditmodule.NewSubjectLifecycle(binding)
		}
	}
	records := recordpersistence.NewRecordStore(store)
	if dataExchangeFactory == nil {
		return runtimeServiceAssembly{}, fmt.Errorf("Data Exchange factory is required")
	}
	dataExchangeProviders := recordapplication.NewDataExchangeProviders(nil)
	if strings.TrimSpace(dataExchangeProviderKey) != "" {
		dataExchangeProviders.RegisterImportProvider(dataExchangeProviderKey, dataExchangeImportProvider)
		dataExchangeProviders.RegisterExportProvider(dataExchangeProviderKey, dataExchangeExportProvider)
	}
	dataExchangeBinding, err := openDataExchangeBinding(ctx, dataExchangeFactory, dataexchangesdk.ApplicationRef{ApplicationID: valueOrDefault(manifest.TemplateID, "domainry-runtime"), RuntimeID: valueOrDefault(cfg.RuntimeVersion, "domainry-runtime")}, dataExchangeModuleHost{store: store, providers: dataExchangeProviders})
	if err != nil {
		return runtimeServiceAssembly{}, fmt.Errorf("open Data Exchange module: %w", err)
	}
	uploadDirectory := cfg.UploadDir
	if uploadDirectory == "" {
		uploadDirectory = "../data/uploads"
	}
	lifecycleBinding, err := lifecyclemoduleimpl.NewFactory().OpenModule(ctx,
		lifecyclesdk.ApplicationRef{RuntimeID: valueOrDefault(cfg.RuntimeVersion, "domainry-runtime")}, lifecyclemodule.NewHost(store))
	if err != nil {
		return runtimeServiceAssembly{}, fmt.Errorf("open Lifecycle module: %w", err)
	}
	if err := lifecycleBinding.Descriptor().Validate(); err != nil {
		_ = lifecycleBinding.Close(context.WithoutCancel(ctx))
		return runtimeServiceAssembly{}, fmt.Errorf("validate Lifecycle module: %w", err)
	}
	lifecycleArchives := lifecycleBinding.ArchiveStore()
	if lifecycleArchives == nil {
		_ = lifecycleBinding.Close(context.WithoutCancel(ctx))
		return runtimeServiceAssembly{}, fmt.Errorf("Lifecycle Binding is incomplete")
	}
	var agentLifecycleExecutor lifecyclecontract.OwnerLifecycleExecutor
	if agentBinding != nil {
		if !agentBinding.Descriptor().HasCapability(agentsdk.CapabilityLifecycleExecute) {
			_ = lifecycleBinding.Close(context.WithoutCancel(ctx))
			return runtimeServiceAssembly{}, fmt.Errorf("Agent Binding does not disclose lifecycle execution capability")
		}
		agentLifecycleBinding, ok := agentBinding.(agentlifecycle.Binding)
		if !ok {
			_ = lifecycleBinding.Close(context.WithoutCancel(ctx))
			return runtimeServiceAssembly{}, fmt.Errorf("Agent Binding returned no lifecycle extension")
		}
		agentLifecycleExecutor = agentLifecycleBinding.LifecycleExecutor(lifecycleArchives)
		if agentLifecycleExecutor == nil {
			_ = lifecycleBinding.Close(context.WithoutCancel(ctx))
			return runtimeServiceAssembly{}, fmt.Errorf("Agent Binding returned no lifecycle executor")
		}
	}
	lifecycleArtifacts, err := lifecycleBinding.SubjectArtifacts(uploadDirectory)
	if err != nil {
		_ = lifecycleBinding.Close(context.WithoutCancel(ctx))
		return runtimeServiceAssembly{}, fmt.Errorf("open Lifecycle subject artifacts: %w", err)
	}
	lifecycleFileArtifacts, err := lifecycleBinding.UploadArtifacts(lifecyclesdk.UploadArtifactOptions{
		Root:              uploadDirectory,
		Fields:            lifecyclemodule.NewUploadFieldCatalog(manifest.Objects),
		References:        recordpersistence.NewUploadArtifactReferences(store, manifest.Objects),
		ExpiredReferences: reportpersistence.NewUploadArtifactCleaner(store, manifest.Objects),
	})
	if err != nil {
		_ = lifecycleBinding.Close(context.WithoutCancel(ctx))
		return runtimeServiceAssembly{}, fmt.Errorf("open Lifecycle upload artifacts: %w", err)
	}
	workerDependencies = workerplatform.NormalizeDependencies(workerDependencies)
	lifecycleExecutors := []lifecyclecontract.OwnerLifecycleExecutor{
		ratelimitpersistence.LifecycleExecutor(store, lifecycleArchives),
		actionpersistence.LifecycleExecutor(store, lifecycleArchives),
		recordpersistence.LifecycleExecutor(store, lifecycleArchives, manifest.Objects...),
		operationspersistence.LifecycleExecutor(store, lifecycleArchives),
		notificationpublicationpersistence.LifecycleExecutor(store, lifecycleArchives),
		workflowpersistence.LifecycleExecutor(store, lifecycleArchives),
		automationpersistence.LifecycleExecutor(store, lifecycleArchives),
		runtimeauditmodule.LifecycleExecutor(store, lifecycleArchives),
		reportpersistence.LifecycleExecutor(store, lifecycleArchives, manifest.Objects...),
	}
	if agentLifecycleExecutor != nil {
		lifecycleExecutors = append(lifecycleExecutors, agentLifecycleExecutor)
	}
	lifecycleExecutorPorts := append([]lifecyclecontract.OwnerLifecycleExecutor(nil), lifecycleExecutors...)
	if notificationRetention != nil {
		lifecycleExecutorPorts = append(lifecycleExecutorPorts, notificationRetention)
	}
	fileScanKey := sha256.Sum256([]byte("domainry-file-scan-receipt-v1:" + cfg.IntegrationSecretKey))
	fileScans := uploadapplication.NewFileScanReceiptVerifier(lifecycleFileArtifacts, fileScanKey[:])
	recordSubjectLifecycle := recordapplication.NewRecordSubjectLifecycleApplicationService(records, manifest.Objects, lifecycleArtifacts, manifest.IdentityProfileExtensions)
	reportDatasetStore := reportpersistence.NewReportDatasetStore(store)
	var agentTaskRunner agentsdk.TaskRunner
	if agentBinding != nil {
		agentTaskRunner = agentBinding.TaskRunner()
	}
	projectRevision, metadataRevision := runtimeActionRevisions(manifest)
	subjectHandlers := []lifecyclecontract.SubjectExecutionHandler{recordSubjectLifecycle, auditSubjectLifecycle}
	if notificationSubjectLifecycle != nil {
		subjectHandlers = append(subjectHandlers, notificationSubjectLifecycle)
	}
	if err := lifecycleBinding.BindOwners(ctx, lifecyclesdk.OwnerExtensions{
		Executors: lifecycleExecutorPorts, SubjectResolver: recordSubjectLifecycle, SubjectHandlers: subjectHandlers,
		Artifacts: lifecycleArtifacts, UploadArtifacts: lifecycleFileArtifacts,
	}); err != nil {
		_ = lifecycleBinding.Close(context.WithoutCancel(ctx))
		return runtimeServiceAssembly{}, fmt.Errorf("bind Lifecycle owner extensions: %w", err)
	}
	if lifecycleBinding.Governance() == nil || lifecycleBinding.System() == nil {
		_ = lifecycleBinding.Close(context.WithoutCancel(ctx))
		return runtimeServiceAssembly{}, fmt.Errorf("Lifecycle Binding returned no business capabilities")
	}
	services := composition.NewRuntimeServices(ctx, composition.RuntimeServicesConfig{
		Manifest: manifest,
		Dependencies: composition.RuntimeServicesDependencies{
			ProductBrandName:                    cfg.EffectiveProductBrandName(),
			AgentPrincipals:                     identityPrincipals,
			AgentTaskRunner:                     agentTaskRunner,
			ActionRuntimeRevision:               cfg.RuntimeVersion,
			ActionProjectRevision:               projectRevision,
			ActionMetadataRevision:              metadataRevision,
			Records:                             records,
			ReportDatasetRows:                   reportDatasetStore,
			ReportObjectSQL:                     reportDatasetStore,
			ReportSnapshotSources:               reportDatasetStore,
			RecordExecutions:                    records,
			DataExchange:                        dataExchangeBinding,
			DataExchangeProviders:               dataExchangeProviders,
			Audit:                               auditRepository,
			AuditApplication:                    auditApplication,
			AuditExportTokenKey:                 []byte(cfg.AuditExportTokenKey),
			ApplicationSchema:                   appschemapersistence.NewApplicationSchemaStore(store),
			MetadataDefinitions:                 store.Metadata().Definitions(),
			MetadataLocalization:                store.Metadata().Localization(),
			IntegrationPublication:              publicationhandoffpersistence.NewPublicationStore(store),
			IntegrationPublicationWorker:        publicationhandoffpersistence.NewWorkerStore(store),
			WorkerWakeups:                       store.WorkerWakeups(),
			WorkflowWorker:                      workflowpersistence.NewWorkflowWorkerStore(store),
			WorkflowDefinitions:                 workflowpersistence.NewWorkflowDefinitionStore(store),
			WorkflowProcesses:                   workflowpersistence.NewWorkflowProcessStore(store),
			WorkflowDecisions:                   workflowpersistence.NewWorkflowDecisionStore(store),
			WorkflowNotificationCompiler:        notificationCompiler,
			WorkflowTaskNotificationCommitter:   taskNotificationCommitter,
			RecordNotificationCompiler:          notificationCompiler,
			ReportNotificationCompiler:          notificationCompiler,
			ReportSnapshotNotificationCommitter: reportnotification.NewReportSnapshotNotificationCommitter(store, reportBinding.Snapshots()),
			AutomationNotificationCompiler:      notificationCompiler,
			AutomationNotificationCommitter:     automationnotification.NewAutomationExecutionNotificationCommitter(store),
			NotificationIntentPublisher:         notificationIntentPublisherCallback(notificationPublisher),
			AutomationWorker:                    automationpersistence.NewAutomationWorkerStore(store),
			AutomationExecutions:                automationpersistence.NewAutomationExecutionStore(store),
			BusinessEvidence:                    nil,
			ActionExecutions:                    actionpersistence.NewActionBusinessExecutionStore(store),
			ActionAssurance:                     actionpersistence.NewActionAssuranceStore(store),
			BusinessHandlers:                    businessHandlers,
			VerifyFileClean: func(ctx context.Context, workspaceID string, request runtimeext.FileVerificationRequest) (runtimeext.FileVerificationEvidence, error) {
				evidence, err := fileScans.VerifyClean(ctx, workspaceID, request.FileID, request.ContentSHA256, request.ScanReceipt)
				if err != nil {
					return runtimeext.FileVerificationEvidence{}, err
				}
				return runtimeext.FileVerificationEvidence{FileID: evidence.FileID, ContentSHA256: evidence.SHA256, Size: evidence.Size, Status: evidence.Status, Provider: evidence.Provider, EvidenceRef: evidence.EvidenceRef}, nil
			},
			PrepareOutboxPayload: func(ctx context.Context, message publicationmodel.Message, payload map[string]any) (map[string]any, error) {
				if strings.TrimSpace(message.ConnectorKey) != "email" || strings.TrimSpace(message.Operation) != "send_file_email" {
					return payload, nil
				}
				fileID, contentHash, receipt := strings.TrimSpace(fmt.Sprint(payload["file_id"])), strings.TrimSpace(fmt.Sprint(payload["content_sha256"])), strings.TrimSpace(fmt.Sprint(payload["scan_receipt"]))
				evidence, err := fileScans.VerifyClean(ctx, message.WorkspaceID, fileID, contentHash, receipt)
				if err != nil {
					return nil, err
				}
				workspaceDigest := sha256.Sum256([]byte(message.WorkspaceID))
				path := filepath.Join(uploadDirectory, "workspace-"+hex.EncodeToString(workspaceDigest[:16]), evidence.Filename)
				content, err := os.ReadFile(path)
				if err != nil {
					return nil, fmt.Errorf("backend.integration.email.attachment_unavailable: %w", err)
				}
				if int64(len(content)) != evidence.Size {
					return nil, fmt.Errorf("backend.integration.email.attachment_identity_mismatch")
				}
				contentDigest := sha256.Sum256(content)
				if hex.EncodeToString(contentDigest[:]) != evidence.SHA256 {
					return nil, fmt.Errorf("backend.integration.email.attachment_identity_mismatch")
				}
				prepared := make(map[string]any, len(payload)+4)
				for key, value := range payload {
					prepared[key] = value
				}
				prepared["_runtime_attachment_base64"] = base64.StdEncoding.EncodeToString(content)
				prepared["_runtime_attachment_filename"] = evidence.Filename
				prepared["_runtime_attachment_content_type"] = evidence.ContentType
				prepared["_runtime_scan_evidence_ref"] = evidence.EvidenceRef
				return prepared, nil
			},
			RuntimeStatus:              deploymentpersistence.NewRuntimeStatusStore(store),
			Notifications:              notifications,
			IdentityProjection:         identityProjection,
			IntegrationOwnerDelivery:   integrationOwnerDelivery,
			IntegrationOwnerCatalog:    integrationOwnerCatalog,
			IntegrationOwnerManagement: integrationOwnerManagement,
			IntegrationOwnerOperations: integrationOwnerOperations,
			Worker:                     workerDependencies,
		},
	})
	lifecycleScope := lifecycleaccess.NewSystemScope(lifecycleaccess.SystemScopeInstallation, "install default lifecycle policies")
	lifecyclePrincipal := lifecycleaccess.NewSystemPrincipal("runtime-lifecycle", lifecycleScope)
	services.Applications().RuntimeStatus.ConfigureLifecycleHealth(ctx, lifecycleBinding.System())
	workflowScope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "initialize published workflow definitions")
	return completeRuntimeServiceAssembly(
		runtimeServiceAssembly{services: services, records: records, worker: workerDependencies, dataExchangeBinding: dataExchangeBinding, lifecycleBinding: lifecycleBinding},
		func() error {
			return lifecycleBinding.System().InstallDefaultPolicies(ctx, principalmodel.InstallationWorkspaceID, lifecyclePrincipal, time.Now().UTC())
		},
		func() {
			services.Applications().RecordTimers.ConfigureWorker(recordtimerapplication.WorkerConfig{Enabled: cfg.RecordTimerEnabled, PollInterval: cfg.RecordTimerPollInterval, BatchSize: cfg.RecordTimerBatchSize, LeaseTTL: cfg.RecordTimerLeaseTTL})
		},
		func() error {
			return services.Applications().Workflows.InitializePublishedWorkflowDefinitions(ctx, manifest.Workflows, workflowScope)
		},
	)
}

type agentSchemaOwner interface {
	EnsureSchema(context.Context) error
}

func ensureAgentRuntimeSchemas(ctx context.Context, migration agentSchemaOwner, tasks interface{ BackfillWorkerScopes(context.Context) error }) error {
	if err := migration.EnsureSchema(ctx); err != nil {
		return fmt.Errorf("ensure agent schema migration: %w", err)
	}
	if err := tasks.BackfillWorkerScopes(ctx); err != nil {
		return fmt.Errorf("backfill agent task worker scopes: %w", err)
	}
	return nil
}

func completeRuntimeServiceAssembly(result runtimeServiceAssembly, installLifecycle func() error, configure func(), initializeWorkflows func() error) (runtimeServiceAssembly, error) {
	if err := installLifecycle(); err != nil {
		return runtimeServiceAssembly{}, fmt.Errorf("install lifecycle policies: %w", err)
	}
	configure()
	if err := initializeWorkflows(); err != nil {
		return runtimeServiceAssembly{}, fmt.Errorf("initialize workflow definitions: %w", err)
	}
	return result, nil
}
