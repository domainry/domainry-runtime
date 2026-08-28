package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	connector "github.com/domainry/domainry-connector-sdk"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	agentapplication "github.com/domainry/domainry-runtime/runtime/application/agent/runtime"
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/audit"
	integrationapplication "github.com/domainry/domainry-runtime/runtime/application/integration"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	schedulerapplication "github.com/domainry/domainry-runtime/runtime/application/scheduler"
	deploymentseed "github.com/domainry/domainry-runtime/runtime/application/seed/deployment"
	uploadapplication "github.com/domainry/domainry-runtime/runtime/application/upload"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	composition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	lifecyclecontract "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/contract"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	agenthttp "github.com/domainry/domainry-runtime/runtime/infrastructure/agentrunner/http"
	localartifact "github.com/domainry/domainry-runtime/runtime/infrastructure/lifecycleartifact/filesystem"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	actionpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/action"
	agentpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/agent"
	auditpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/audit"
	automationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/automation"
	automationnotification "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/automationnotification"
	changeplanpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/changeplan"
	deploymentpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/deployment"
	frontendcapabilitypersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/frontendcapability"
	integrationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/integration"
	lifecyclepersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/lifecycle"
	metadatapersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/metadata"
	partypersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/party"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	recordnotification "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/recordnotification"
	reportpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/report"
	reportnotification "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/reportnotification"
	workflowpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workflow"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-runtime/runtime/platform/ratelimit"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

// runtimeServiceAssembly is the result of wiring domain ports to adapters.
type runtimeServiceAssembly struct {
	services *composition.RuntimeServices
	records  recordrepository.RecordRepository
	worker   workerplatform.Dependencies
}

type runtimeExtensionRegistries struct {
	businessHandlers                   *runtimeext.BusinessHandlerRegistry
	connectorProviders                 *connector.Registry
	notificationCompiler               func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
	taskNotificationCommitter          workflowapplication.WorkflowTaskNotificationCommitter
	integrationNotificationPublisher   integrationapplication.IntegrationNotificationPublisher
	integrationCredentialNotifications integrationapplication.IntegrationCredentialNotificationCommitter
	integrationCredentialExpirySource  integrationapplication.IntegrationCredentialExpirySource
	notificationSubjectLifecycle       lifecyclecontract.SubjectDataHandler
}

func assembleRuntimeServices(ctx context.Context, cfg config.Config, manifest manifestmodel.ManifestSchema, notifications composition.NotificationRenderer, store *persistence.RuntimeStore, identityDirectory identitysdk.Directory, identityPrincipals identitysdk.PrincipalResolver, auditApplication *auditapplication.AuditApplicationService, apiLimiter ratelimit.Limiter, workerDependencies workerplatform.Dependencies, extensionRegistries ...runtimeExtensionRegistries) (runtimeServiceAssembly, error) {
	businessHandlers := runtimeext.NewBusinessHandlerRegistry()
	connectorProviders := connector.NewRegistry()
	var notificationCompiler func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
	var taskNotificationCommitter workflowapplication.WorkflowTaskNotificationCommitter
	var integrationNotificationPublisher integrationapplication.IntegrationNotificationPublisher
	var integrationCredentialNotifications integrationapplication.IntegrationCredentialNotificationCommitter
	var integrationCredentialExpirySource integrationapplication.IntegrationCredentialExpirySource
	var notificationSubjectLifecycle lifecyclecontract.SubjectDataHandler
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
		integrationNotificationPublisher = extensionRegistries[0].integrationNotificationPublisher
		integrationCredentialNotifications = extensionRegistries[0].integrationCredentialNotifications
		integrationCredentialExpirySource = extensionRegistries[0].integrationCredentialExpirySource
		notificationSubjectLifecycle = extensionRegistries[0].notificationSubjectLifecycle
	}
	records := recordpersistence.NewRecordStore(store)
	agentTaskRuns := agentpersistence.NewAgentTaskRunStore(store)
	if err := ensureAgentRuntimeSchemas(ctx, agentpersistence.NewAgentStateStore(store), agentTaskRuns); err != nil {
		return runtimeServiceAssembly{}, err
	}
	workerDependencies = workerplatform.NormalizeDependencies(workerDependencies)
	lifecycleExecutors := lifecyclepersistence.DefaultOwnerExecutors(store, manifest.Objects...)
	lifecycleExecutorPorts := append([]lifecyclecontract.OwnerLifecycleExecutor(nil), lifecycleExecutors...)
	uploadDirectory := cfg.UploadDir
	if uploadDirectory == "" {
		uploadDirectory = "../data/uploads"
	}
	integrationSubjectLifecycle := integrationpersistence.NewIntegrationSubjectLifecycleStore(store)
	auditSubjectLifecycle := auditpersistence.NewAuditSubjectLifecycleStore(store)
	lifecycleArtifacts := localartifact.NewSubjectStore(uploadDirectory)
	lifecycleFileArtifacts := lifecyclepersistence.NewFileArtifactStore(store, manifest.Objects, uploadDirectory)
	fileScanKey := sha256.Sum256([]byte("domainry-file-scan-receipt-v1:" + cfg.IntegrationSecretKey))
	fileScans := uploadapplication.NewFileScanReceiptVerifier(lifecycleFileArtifacts, fileScanKey[:])
	recordSubjectLifecycle := recordapplication.NewRecordSubjectLifecycleApplicationService(records, manifest.Objects, lifecycleArtifacts, manifest.IdentityProfileExtensions)
	reportDatasetStore := reportpersistence.NewReportDatasetStore(store)
	agentTaskRunner, interactiveAgentRunner := configuredAgentRunners(cfg)
	agentTaskCredentialKey := sha256.Sum256([]byte("domainry-agent-task-credential-v1:" + cfg.IntegrationSecretKey))
	projectRevision, metadataRevision := runtimeActionRevisions(manifest)
	subjectHandlers := []lifecyclecontract.SubjectDataHandler{recordSubjectLifecycle, integrationSubjectLifecycle, auditSubjectLifecycle}
	if notificationSubjectLifecycle != nil {
		subjectHandlers = append(subjectHandlers, notificationSubjectLifecycle)
	}
	services := composition.NewRuntimeServices(ctx, composition.RuntimeServicesConfig{
		Manifest: manifest,
		Dependencies: composition.RuntimeServicesDependencies{
			ProductBrandName:                    cfg.EffectiveProductBrandName(),
			AgentTaskRuns:                       agentTaskRuns,
			AgentPrincipals:                     identityPrincipals,
			AgentTaskRunner:                     agentTaskRunner,
			AgentInteractiveRunner:              interactiveAgentRunner,
			AgentTaskCredentialKey:              agentTaskCredentialKey[:],
			AgentTaskWorkerConfig:               agentapplication.AgentTaskWorkerConfig{SystemScope: principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "claim durable Agent tasks"), LeaseTTL: cfg.SchedulerLeaseTTL, HeartbeatInterval: cfg.SchedulerLeaseTTL / 3, RetryBaseDelay: cfg.SchedulerPollInterval},
			ActionRuntimeRevision:               cfg.RuntimeVersion,
			ActionProjectRevision:               projectRevision,
			ActionMetadataRevision:              metadataRevision,
			Records:                             records,
			ReportDatasetRows:                   reportDatasetStore,
			ReportObjectSQL:                     reportDatasetStore,
			ReportSnapshots:                     reportpersistence.NewReportSnapshotStore(store),
			ReportExportArtifacts:               reportpersistence.NewReportExportArtifactStore(store),
			ReportSnapshotSources:               reportDatasetStore,
			RecordExecutions:                    records,
			Audit:                               auditpersistence.NewAuditStore(store),
			AuditExports:                        auditpersistence.NewAuditBusinessExportStore(store),
			AuditApplication:                    auditApplication,
			AuditExportTokenKey:                 []byte(cfg.AuditExportTokenKey),
			Metadata:                            metadatapersistence.NewMetadataStore(store),
			IntegrationConfig:                   integrationpersistence.NewIntegrationConfigStore(store),
			IntegrationEvents:                   integrationpersistence.NewIntegrationEventStore(store),
			IntegrationDelivery:                 integrationpersistence.NewIntegrationDeliveryStore(store),
			IntegrationWorker:                   integrationpersistence.NewIntegrationWorkerStore(store),
			WorkerWakeups:                       store.WorkerWakeups(),
			WorkflowWorker:                      workflowpersistence.NewWorkflowWorkerStore(store),
			WorkflowDefinitions:                 workflowpersistence.NewWorkflowDefinitionStore(store),
			WorkflowProcesses:                   workflowpersistence.NewWorkflowProcessStore(store),
			WorkflowDecisions:                   workflowpersistence.NewWorkflowDecisionStore(store),
			WorkflowNotificationCompiler:        notificationCompiler,
			WorkflowTaskNotificationCommitter:   taskNotificationCommitter,
			RecordNotificationCompiler:          notificationCompiler,
			RecordBatchNotificationCommitter:    recordnotification.NewRecordBatchNotificationCommitter(store),
			ReportNotificationCompiler:          notificationCompiler,
			ReportSnapshotNotificationCommitter: reportnotification.NewReportSnapshotNotificationCommitter(store),
			AutomationNotificationCompiler:      notificationCompiler,
			AutomationNotificationCommitter:     automationnotification.NewAutomationExecutionNotificationCommitter(store),
			NotificationIntentPublisher:         notificationIntentPublisherCallback(notificationIntentPublisher(integrationNotificationPublisher)),
			AutomationWorker:                    automationpersistence.NewAutomationWorkerStore(store),
			AutomationExecutions:                automationpersistence.NewAutomationExecutionStore(store),
			BusinessChangePlans:                 changeplanpersistence.NewBusinessChangePlanStore(store),
			BusinessEvidence:                    changeplanpersistence.NewBusinessEvidenceStore(store),
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
			PrepareOutboxPayload: func(ctx context.Context, message integrationmodel.IntegrationOutboxMessage, payload map[string]any) (map[string]any, error) {
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
			ConnectorProviders:                 connectorProviders,
			RuntimeStatus:                      deploymentpersistence.NewRuntimeStatusStore(store),
			FrontendCapabilities:               frontendcapabilitypersistence.NewFrontendCapabilityStore(store),
			Notifications:                      notifications,
			IdentityDirectory:                  identityDirectory,
			PartyDirectory:                     partypersistence.NewSQLPartyStore(store.DB(), store.Driver(), store.DatabaseSchema()),
			IntegrationAPILimiter:              apiLimiter,
			IntegrationNotificationCompiler:    notificationCompiler,
			IntegrationNotificationPublisher:   integrationNotificationPublisher,
			IntegrationCredentialNotifications: integrationCredentialNotifications,
			IntegrationCredentialExpirySource:  integrationCredentialExpirySource,
			Lifecycle:                          lifecyclepersistence.NewLifecycleStore(store),
			LifecycleExecutors:                 lifecycleExecutorPorts,
			LifecycleArtifacts:                 lifecycleArtifacts,
			LifecycleUploadArtifacts:           lifecycleFileArtifacts,
			LifecycleSubjectHandlers:           subjectHandlers,
			LifecycleExternalErasure:           integrationSubjectLifecycle,
			BatchJobQueueLimit:                 cfg.CapacityBatchJobQueueLimit,
			BatchJobWorkspaceQueueLimit:        cfg.CapacityBatchJobWorkspaceQueueLimit,
			Worker:                             workerDependencies,
		},
	})
	lifecyclePrincipal := principalmodel.NewSystemPrincipal("runtime-lifecycle", principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "install default lifecycle policies"))
	workflowScope := principalmodel.NewSystemScope(principalmodel.SystemScopeInstallation, "initialize published workflow definitions")
	return completeRuntimeServiceAssembly(
		runtimeServiceAssembly{services: services, records: records, worker: workerDependencies},
		func() error {
			return services.Applications().Lifecycle.InstallDefaultPolicies(ctx, principalmodel.InstallationWorkspaceID, lifecyclePrincipal, time.Now().UTC())
		},
		func() error {
			return deploymentseed.InstallFrontendCapabilityManifest(ctx, services.Applications().FrontendCapabilities, cfg.FrontendCapabilityManifestPath)
		},
		func() {
			services.Applications().Scheduler.ConfigureWorker(schedulerapplication.WorkerConfig{Enabled: cfg.SchedulerEnabled, PollInterval: cfg.SchedulerPollInterval, BatchSize: cfg.SchedulerBatchSize, LeaseTTL: cfg.SchedulerLeaseTTL, MaxCatchupWindows: cfg.SchedulerMaxCatchupWindows})
			services.Applications().Integrations.RegisterSharedIntegrationOutboxSenders()
			services.Applications().Integrations.RegisterProviderIntegrationOutboxSenders()
		},
		func() error {
			return services.Applications().Workflows.InitializePublishedWorkflowDefinitions(ctx, manifest.Workflows, workflowScope)
		},
	)
}

type agentSchemaOwner interface {
	EnsureSchema(context.Context) error
}

func ensureAgentRuntimeSchemas(ctx context.Context, lifecycle, tasks agentSchemaOwner) error {
	if err := lifecycle.EnsureSchema(ctx); err != nil {
		return fmt.Errorf("ensure agent lifecycle schema: %w", err)
	}
	if err := tasks.EnsureSchema(ctx); err != nil {
		return fmt.Errorf("ensure agent task schema: %w", err)
	}
	if backfiller, ok := tasks.(interface{ BackfillWorkerScopes(context.Context) error }); ok {
		if err := backfiller.BackfillWorkerScopes(ctx); err != nil {
			return fmt.Errorf("backfill agent task worker scopes: %w", err)
		}
	}
	return nil
}

func configuredAgentRunners(cfg config.Config) (agentapplication.AgentTaskRunner, agentapplication.InteractiveAgentRunner) {
	if strings.TrimSpace(cfg.AgentHTTPAPIKey) == "" || cfg.AgentHTTPAgentID <= 0 {
		return nil, nil
	}
	runnerConfig := agenthttp.Config{BaseURL: cfg.AgentHTTPBaseURL, APIKey: cfg.AgentHTTPAPIKey, AgentID: cfg.AgentHTTPAgentID, Timeout: cfg.AgentHTTPTimeout}
	return agenthttp.NewAgentTaskRunner(runnerConfig), agenthttp.NewInteractiveAgentRunner(runnerConfig)
}

func completeRuntimeServiceAssembly(result runtimeServiceAssembly, installLifecycle, installFrontend func() error, configure func(), initializeWorkflows func() error) (runtimeServiceAssembly, error) {
	if err := installLifecycle(); err != nil {
		return runtimeServiceAssembly{}, fmt.Errorf("install lifecycle policies: %w", err)
	}
	if err := installFrontend(); err != nil {
		return runtimeServiceAssembly{}, fmt.Errorf("install frontend capability manifest: %w", err)
	}
	configure()
	if err := initializeWorkflows(); err != nil {
		return runtimeServiceAssembly{}, fmt.Errorf("initialize workflow definitions: %w", err)
	}
	return result, nil
}
