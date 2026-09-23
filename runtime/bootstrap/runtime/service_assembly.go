package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	auditcontract "github.com/domainry/domainry-audit-sdk/contract"
	publicationmodel "github.com/domainry/domainry-runtime/runtime/domain/publication/model"
	"io"
	"strings"
	"time"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	agentlifecycle "github.com/domainry/domainry-agent-sdk/lifecycle"

	auditsdk "github.com/domainry/domainry-audit-sdk"
	connector "github.com/domainry/domainry-connector-sdk"
	dataexchangesdk "github.com/domainry/domainry-data-exchange-sdk"
	dataexchangemodulehost "github.com/domainry/domainry-data-exchange-sdk/modulehost"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	organizationunit "github.com/domainry/domainry-identity-sdk/organizationunit"
	integrationsdk "github.com/domainry/domainry-integration-sdk"
	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	lifecycleaccess "github.com/domainry/domainry-lifecycle-sdk/access"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	reportsdk "github.com/domainry/domainry-report-sdk"
	reportmodulehost "github.com/domainry/domainry-report-sdk/modulehost"
	"github.com/domainry/domainry-runtime/pkg/runtimeext"
	"github.com/domainry/domainry-runtime/pkg/runtimefile"
	actionapplication "github.com/domainry/domainry-runtime/runtime/application/action"
	auditapplication "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	auditrepository "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	publicresourceapplication "github.com/domainry/domainry-runtime/runtime/application/publicresource"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	recordtimerapplication "github.com/domainry/domainry-runtime/runtime/application/recordtimer"
	uploadapplication "github.com/domainry/domainry-runtime/runtime/application/upload"
	workflowapplication "github.com/domainry/domainry-runtime/runtime/application/workflow"
	composition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	projectmodel "github.com/domainry/domainry-runtime/runtime/domain/project/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	blobstore "github.com/domainry/domainry-runtime/runtime/infrastructure/blobstore"
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
	publicresourcepersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/publicresource"
	ratelimitpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/ratelimit"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	reportpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/report"
	reportnotification "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/reportnotification"
	workflowpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workflow"
	workspaceaggregatepersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workspaceaggregate"
	workspaceprovisionpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/workspaceprovision"
	lifecyclemodule "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/lifecyclemodule"
	subjectevidence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/subjectevidence"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

// runtimeServiceAssembly is the result of wiring domain ports to adapters.
type runtimeServiceAssembly struct {
	services            *composition.RuntimeServices
	records             recordrepository.RecordRepository
	worker              workerplatform.Dependencies
	dataExchangeBinding dataexchangesdk.Binding
	lifecycleBinding    lifecyclesdk.Binding
	fileScanProcessor   *uploadapplication.FileScanProcessor
	publicResources     *publicresourceapplication.Service
	blobStore           runtimefile.BlobStore
}

type runtimeExtensionRegistries struct {
	projectExtensions               *runtimeext.ProjectExtensionRegistry
	connectorProviders              *connector.Registry
	notificationCompiler            func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
	taskNotificationCommitter       workflowapplication.WorkflowTaskNotificationCommitter
	notificationPublisher           notificationIntentPublisher
	integrationOwnerDelivery        integrationsdk.Delivery
	integrationOwnerCatalog         integrationsdk.Catalog
	integrationOwnerManagement      integrationsdk.Management
	integrationOwnerOperations      integrationsdk.Operations
	integrationOwnerSubjects        integrationsdk.SubjectLifecycle
	integrationSubjectPersistence   integrationsdk.SubjectLifecyclePersistenceBinding
	dataExchangeProviderKey         string
	dataExchangeImportProvider      dataexchangemodulehost.ImportProvider
	dataExchangeExportProvider      dataexchangemodulehost.ExportProvider
	integrationMode                 integrationsdk.DeploymentMode
	notificationSubjectLifecycle    lifecyclecontract.SubjectExecutionHandler
	identitySubjectLifecycle        lifecyclecontract.SubjectExecutionHandler
	identityBinding                 identitysdk.Binding
	notificationRetention           lifecyclecontract.OwnerLifecycleExecutor
	notificationArchives            *notificationSDKRetentionArchiveStore
	auditRepository                 auditrepository.AuditRepository
	auditSubjectLifecycle           lifecyclecontract.SubjectExecutionHandler
	auditBinding                    auditsdk.Binding
	lifecycleFactory                lifecyclesdk.Factory
	dataExchangeFactory             dataexchangesdk.Factory
	agentBinding                    agentsdk.Binding
	reportBinding                   reportsdk.Binding
	reportAnalysisTables            reportmodulehost.AnalysisTableSource
	identityHandlerDeliveryBinder   identitysdk.HandlerDeliveryUnitOfWorkBinder
	organizationUnitDeliveryBinder  organizationunit.UnitOfWorkBinder
	storeOrganizationDeliveryBinder identitysdk.StoreOrganizationDeliveryUnitOfWorkBinder
	workspaceIdentityUsageBinder    identitysdk.WorkspaceIdentityUsageUnitOfWorkBinder
	blobStore                       runtimefile.BlobStore
	fileScanner                     runtimefile.FileScanner
}

func assembleRuntimeServices(ctx context.Context, cfg config.Config, projectModel projectmodel.RuntimeModel, projectDefinitions runtimeext.ProjectDefinitions, integrations appschemamodel.IntegrationSchema, notifications composition.NotificationRenderer, store *persistence.RuntimeStore, identityProjection identitysdk.Projection, identityPrincipals identitysdk.PrincipalResolver, auditApplication *auditapplication.AuditApplicationService, workerDependencies workerplatform.Dependencies, extensionRegistries ...runtimeExtensionRegistries) (runtimeServiceAssembly, error) {
	schemaCapabilities := store.RuntimeSchemaCapabilities()
	projectExtensions := runtimeext.NewProjectExtensionRegistry()
	connectorProviders := connector.NewRegistry()
	var notificationCompiler func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
	var taskNotificationCommitter workflowapplication.WorkflowTaskNotificationCommitter
	var notificationPublisher notificationIntentPublisher
	var integrationOwnerDelivery integrationsdk.Delivery
	var integrationOwnerCatalog integrationsdk.Catalog
	var integrationOwnerManagement integrationsdk.Management
	var integrationOwnerOperations integrationsdk.Operations
	var integrationOwnerSubjects integrationsdk.SubjectLifecycle
	var integrationSubjectPersistence integrationsdk.SubjectLifecyclePersistenceBinding
	var notificationSubjectLifecycle lifecyclecontract.SubjectExecutionHandler
	var identitySubjectLifecycle lifecyclecontract.SubjectExecutionHandler
	var identityBinding identitysdk.Binding
	var notificationRetention lifecyclecontract.OwnerLifecycleExecutor
	var notificationArchives *notificationSDKRetentionArchiveStore
	var auditRepository auditrepository.AuditRepository
	var auditSubjectLifecycle lifecyclecontract.SubjectExecutionHandler
	var auditBinding auditsdk.Binding
	var lifecycleFactory lifecyclesdk.Factory
	var dataExchangeFactory dataexchangesdk.Factory
	var agentBinding agentsdk.Binding
	var reportBinding reportsdk.Binding
	var reportAnalysisTables reportmodulehost.AnalysisTableSource
	var identityHandlerDeliveryBinder identitysdk.HandlerDeliveryUnitOfWorkBinder
	var organizationUnitDeliveryBinder organizationunit.UnitOfWorkBinder
	var storeOrganizationDeliveryBinder identitysdk.StoreOrganizationDeliveryUnitOfWorkBinder
	var workspaceIdentityUsageBinder identitysdk.WorkspaceIdentityUsageUnitOfWorkBinder
	var dataExchangeProviderKey string
	var dataExchangeImportProvider dataexchangemodulehost.ImportProvider
	var dataExchangeExportProvider dataexchangemodulehost.ExportProvider
	var configuredBlobStore runtimefile.BlobStore
	var configuredFileScanner runtimefile.FileScanner
	if len(extensionRegistries) > 0 && extensionRegistries[0].projectExtensions != nil {
		projectExtensions = extensionRegistries[0].projectExtensions
	} else {
		projectExtensions.Freeze()
	}
	actionDefinitions, err := runtimeext.ProjectActionDefinitions(projectExtensions)
	if err != nil {
		return runtimeServiceAssembly{}, fmt.Errorf("build project operation catalog: %w", err)
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
		integrationOwnerSubjects = extensionRegistries[0].integrationOwnerSubjects
		integrationSubjectPersistence = extensionRegistries[0].integrationSubjectPersistence
		notificationSubjectLifecycle = extensionRegistries[0].notificationSubjectLifecycle
		identitySubjectLifecycle = extensionRegistries[0].identitySubjectLifecycle
		identityBinding = extensionRegistries[0].identityBinding
		notificationRetention = extensionRegistries[0].notificationRetention
		notificationArchives = extensionRegistries[0].notificationArchives
		if extensionRegistries[0].auditRepository != nil {
			auditRepository = extensionRegistries[0].auditRepository
		}
		if extensionRegistries[0].auditSubjectLifecycle != nil {
			auditSubjectLifecycle = extensionRegistries[0].auditSubjectLifecycle
		}
		auditBinding = extensionRegistries[0].auditBinding
		lifecycleFactory = extensionRegistries[0].lifecycleFactory
		dataExchangeFactory = extensionRegistries[0].dataExchangeFactory
		agentBinding = extensionRegistries[0].agentBinding
		reportBinding = extensionRegistries[0].reportBinding
		reportAnalysisTables = extensionRegistries[0].reportAnalysisTables
		identityHandlerDeliveryBinder = extensionRegistries[0].identityHandlerDeliveryBinder
		organizationUnitDeliveryBinder = extensionRegistries[0].organizationUnitDeliveryBinder
		storeOrganizationDeliveryBinder = extensionRegistries[0].storeOrganizationDeliveryBinder
		workspaceIdentityUsageBinder = extensionRegistries[0].workspaceIdentityUsageBinder
		dataExchangeProviderKey = extensionRegistries[0].dataExchangeProviderKey
		dataExchangeImportProvider = extensionRegistries[0].dataExchangeImportProvider
		dataExchangeExportProvider = extensionRegistries[0].dataExchangeExportProvider
		configuredBlobStore = extensionRegistries[0].blobStore
		configuredFileScanner = extensionRegistries[0].fileScanner
	}
	uploadDirectory := cfg.UploadDir
	if uploadDirectory == "" {
		uploadDirectory = "../data/uploads"
	}
	blobs := configuredBlobStore
	if blobs == nil {
		blobs, err = blobstore.NewLocalStore(uploadDirectory)
		if err != nil {
			return runtimeServiceAssembly{}, fmt.Errorf("initialize local blob store: %w", err)
		}
	}
	artifactContent := blobstore.LifecycleContentStore{Blobs: blobs}
	if auditBinding == nil {
		return runtimeServiceAssembly{}, fmt.Errorf("Audit Binding is required from the project composition root")
	}
	if auditRepository == nil {
		auditRepository = runtimeauditmodule.NewAuditStore(auditBinding)
	}
	if auditSubjectLifecycle == nil {
		auditSubjectLifecycle = runtimeauditmodule.NewSubjectLifecycle(auditBinding)
	}
	records := recordpersistence.NewRecordStore(store)
	workspaceAggregates := workspaceaggregatepersistence.NewStore(store)
	installationIdentity, err := store.InstallationIdentity(ctx)
	if err != nil {
		return runtimeServiceAssembly{}, fmt.Errorf("load Runtime installation identity: %w", err)
	}
	workspaceIdentityUsageCursor, err := actionapplication.NewWorkspaceIdentityUsageCursorCodec([]byte(cfg.AuditExportTokenKey), installationIdentity, workerDependencies.Clock.Now)
	if err != nil {
		return runtimeServiceAssembly{}, fmt.Errorf("initialize Workspace identity usage cursor: %w", err)
	}
	dataExchangeBinding, dataExchangeProviders, err := openOptionalDataExchangeBinding(
		ctx,
		dataExchangeFactory,
		dataexchangesdk.ApplicationRef{ApplicationID: valueOrDefault(projectModel.ProjectKey, "domainry-runtime"), RuntimeID: valueOrDefault(cfg.RuntimeVersion, "domainry-runtime")},
		store,
		dataExchangeProviderKey,
		dataExchangeImportProvider,
		dataExchangeExportProvider,
	)
	if err != nil {
		return runtimeServiceAssembly{}, err
	}
	lifecycleContent := artifactContent
	var lifecycleBinding lifecyclesdk.Binding
	var lifecycleArtifacts lifecyclecontract.SubjectArtifactStore
	var lifecycleExecutorPorts []lifecyclecontract.OwnerLifecycleExecutor
	var agentSubjectHandlers []lifecyclecontract.SubjectExecutionHandler
	if schemaCapabilities.Lifecycle {
		if lifecycleFactory == nil {
			return runtimeServiceAssembly{}, fmt.Errorf("Lifecycle SDK Factory is required when Lifecycle is enabled")
		}
		lifecycleBinding, err = lifecycleFactory.OpenModule(ctx,
			lifecyclesdk.ApplicationRef{RuntimeID: valueOrDefault(cfg.RuntimeVersion, "domainry-runtime")}, lifecyclemodule.NewHost(store, auditBinding, lifecycleContent))
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
		if notificationArchives != nil {
			notificationArchives.Bind(lifecycleArchives)
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
			if subjects, ok := agentBinding.(agentlifecycle.SubjectBinding); ok {
				agentSubjectHandlers = subjects.LifecycleSubjectHandlers()
				for _, handler := range agentSubjectHandlers {
					if handler == nil || strings.TrimSpace(handler.Owner(ctx)) == "" {
						_ = lifecycleBinding.Close(context.WithoutCancel(ctx))
						return runtimeServiceAssembly{}, fmt.Errorf("Agent Binding returned an invalid lifecycle subject handler")
					}
				}
			}
		}
		lifecycleArtifacts, err = lifecycleBinding.SubjectArtifacts(uploadDirectory, lifecycleContent)
		if err != nil {
			_ = lifecycleBinding.Close(context.WithoutCancel(ctx))
			return runtimeServiceAssembly{}, fmt.Errorf("open Lifecycle subject artifacts: %w", err)
		}
		lifecycleExecutors := []lifecyclecontract.OwnerLifecycleExecutor{
			ratelimitpersistence.LifecycleExecutor(store, lifecycleArchives),
			actionpersistence.LifecycleExecutor(store, lifecycleArchives),
			recordpersistence.LifecycleExecutor(store, lifecycleArchives, projectModel.Objects...),
			operationspersistence.LifecycleExecutor(store, lifecycleArchives),
			notificationpublicationpersistence.LifecycleExecutor(store, lifecycleArchives),
			runtimeauditmodule.LifecycleExecutor(store, lifecycleArchives),
		}
		if schemaCapabilities.Workflow {
			lifecycleExecutors = append(lifecycleExecutors, workflowpersistence.LifecycleExecutor(store, lifecycleArchives))
		}
		if schemaCapabilities.Automation {
			lifecycleExecutors = append(lifecycleExecutors, automationpersistence.LifecycleExecutor(store, lifecycleArchives))
		}
		if reportBinding != nil {
			lifecycleExecutors = append(lifecycleExecutors, reportpersistence.LifecycleExecutor(store, lifecycleArchives, projectModel.Objects...))
		}
		if agentLifecycleExecutor != nil {
			lifecycleExecutors = append(lifecycleExecutors, agentLifecycleExecutor)
		}
		lifecycleExecutorPorts = append([]lifecyclecontract.OwnerLifecycleExecutor(nil), lifecycleExecutors...)
		if notificationRetention != nil {
			lifecycleExecutorPorts = append(lifecycleExecutorPorts, notificationRetention)
		}
	}
	workerDependencies = workerplatform.NormalizeDependencies(workerDependencies)
	var lifecycleFileArtifacts lifecyclecontract.UploadFileArtifactStore
	var fileScanProcessor *uploadapplication.FileScanProcessor
	var fileScans *uploadapplication.FileScanReceiptVerifier
	var fileCapabilities *uploadapplication.FileCapabilityService
	var agentTaskAttachmentFiles *uploadapplication.AgentTaskAttachmentFileService
	if schemaCapabilities.Uploads {
		if lifecycleBinding == nil {
			return runtimeServiceAssembly{}, fmt.Errorf("Uploads require the Lifecycle capability")
		}
		lifecycleFileArtifacts, err = lifecycleBinding.UploadArtifacts(lifecyclesdk.UploadArtifactOptions{
			Root:              uploadDirectory,
			Content:           lifecycleContent,
			Fields:            lifecyclemodule.NewUploadFieldCatalog(projectModel.Objects),
			References:        recordpersistence.NewUploadArtifactReferences(store, projectModel.Objects),
			ExpiredReferences: reportpersistence.NewUploadArtifactCleaner(store, projectModel.Objects),
		})
		if err != nil {
			_ = lifecycleBinding.Close(context.WithoutCancel(ctx))
			return runtimeServiceAssembly{}, fmt.Errorf("open Lifecycle upload artifacts: %w", err)
		}
		durableFileScans, ok := lifecycleFileArtifacts.(uploadapplication.DurableFileScanStore)
		if !ok {
			_ = lifecycleBinding.Close(context.WithoutCancel(ctx))
			return runtimeServiceAssembly{}, fmt.Errorf("Lifecycle Binding does not disclose durable file scan capability")
		}
		fileScanner := configuredFileScanner
		if fileScanner == nil {
			fileScanner = uploadapplication.NewBuiltinFileScanner()
		}
		fileScanProcessor, err = uploadapplication.NewFileScanProcessor(durableFileScans, blobs, fileScanner, workerDependencies.Clock.Now)
		if err != nil {
			_ = lifecycleBinding.Close(context.WithoutCancel(ctx))
			return runtimeServiceAssembly{}, fmt.Errorf("initialize file scan processor: %w", err)
		}
		fileScanKey := sha256.Sum256([]byte("domainry-file-scan-receipt-v1:" + cfg.IntegrationSecretKey))
		fileScans = uploadapplication.NewFileScanReceiptVerifier(lifecycleFileArtifacts, fileScanKey[:])
		fileDownloadTicketKey := sha256.Sum256([]byte("domainry-file-download-ticket-v1:" + cfg.IntegrationSecretKey))
		fileDownloadTickets, ticketErr := uploadapplication.NewFileDownloadTicketService(fileDownloadTicketKey[:], workerDependencies.Clock.Now)
		if ticketErr != nil {
			_ = lifecycleBinding.Close(context.WithoutCancel(ctx))
			return runtimeServiceAssembly{}, fmt.Errorf("initialize file download tickets: %w", ticketErr)
		}
		fileCapabilities, err = uploadapplication.NewFileCapabilityService(lifecycleFileArtifacts, fileScans, blobs, workerDependencies.Clock.Now, fileDownloadTickets)
		if err != nil {
			_ = lifecycleBinding.Close(context.WithoutCancel(ctx))
			return runtimeServiceAssembly{}, fmt.Errorf("initialize file capabilities: %w", err)
		}
		uploadSubjects := uploadapplication.NewUploadSubjectRegistry(store)
		fileCapabilities.BindUploadSubjects(uploadSubjects)
		agentTaskAttachmentFiles = uploadapplication.NewAgentTaskAttachmentFileService(uploadSubjects, fileScans, fileCapabilities)
	}
	publicResources := publicresourceapplication.NewService(projectModel.Objects, publicresourcepersistence.NewStore(store), records, fileCapabilities, fileScans)
	var recordSubjectLifecycle *recordapplication.RecordSubjectLifecycleApplicationService
	if schemaCapabilities.Lifecycle {
		recordSubjectLifecycle = recordapplication.NewRecordSubjectLifecycleApplicationService(records, projectModel.Objects, lifecycleArtifacts, projectModel.IdentityProfiles)
		if schemaCapabilities.Uploads {
			recordSubjectLifecycle.BindSubjectUploads(store.SubjectUploadReferences)
		}
		if audit, ok := auditSubjectLifecycle.(*runtimeauditmodule.SubjectLifecycle); ok {
			audit.BindSubjectResourceResolver(func(ctx context.Context, workspaceID, subjectID string) ([]auditcontract.SubjectResource, error) {
				refs, err := recordSubjectLifecycle.SubjectRecordReferences(ctx, workspaceID, subjectID)
				if err != nil {
					return nil, err
				}
				resources := []auditcontract.SubjectResource{{ObjectKey: "identity_user", RecordID: subjectID}}
				for _, ref := range refs {
					resources = append(resources, auditcontract.SubjectResource{ObjectKey: ref.ObjectKey, RecordID: ref.RecordID})
				}
				return resources, nil
			})
		}
	}

	var reportSQLStore *reportpersistence.ReportSQLStore
	var reportExportPrepareReceipts *reportpersistence.ReportExportPrepareReceiptStore
	var reportSnapshotNotificationCommitter composition.ReportSnapshotNotificationCommitter
	if reportBinding != nil {
		reportSQLStore = reportpersistence.NewReportSQLStore(store)
		reportExportPrepareReceipts = reportpersistence.NewReportExportPrepareReceiptStore(store)
		reportSnapshotNotificationCommitter = reportnotification.NewReportSnapshotNotificationCommitter(store, reportBinding.Snapshots())
	} else {
		reportAnalysisTables = nil
	}
	var agentTaskRunner agentsdk.TaskRunner
	var agentScheduledTasks agentsdk.ScheduledConversationTaskService
	var agentBusinessEvents agentsdk.BusinessEventConversationTaskService
	if agentBinding != nil {
		agentTaskRunner = agentBinding.TaskRunner()
		if agentBinding.Descriptor().HasCapability(agentsdk.CapabilityScheduledConversationTask) {
			conversations, ok := agentBinding.(agentsdk.ConversationBinding)
			if !ok || conversations.Conversations() == nil {
				return runtimeServiceAssembly{}, fmt.Errorf("Agent Binding advertises scheduled conversation tasks without a conversation binding")
			}
			agentScheduledTasks, ok = conversations.Conversations().(agentsdk.ScheduledConversationTaskService)
			if !ok || agentScheduledTasks == nil {
				return runtimeServiceAssembly{}, fmt.Errorf("Agent Binding advertises scheduled conversation tasks without the task service")
			}
		}
		if agentBinding.Descriptor().HasCapability(agentsdk.CapabilityBusinessEventConversationTask) {
			conversations, ok := agentBinding.(agentsdk.ConversationBinding)
			if !ok || conversations.Conversations() == nil {
				return runtimeServiceAssembly{}, fmt.Errorf("Agent Binding advertises business-event conversation tasks without a conversation binding")
			}
			agentBusinessEvents, ok = conversations.Conversations().(agentsdk.BusinessEventConversationTaskService)
			if !ok || agentBusinessEvents == nil {
				return runtimeServiceAssembly{}, fmt.Errorf("Agent Binding advertises business-event conversation tasks without the task service")
			}
		}
	}
	projectRevision, metadataRevision := projectModel.ContentHash, projectModel.ContentHash
	var accountErasures lifecyclecontract.AccountErasures
	if lifecycleBinding != nil {
		subjectEvidence := subjectevidence.New(store, recordSubjectLifecycle.SubjectRecordReferences, subjectEvidenceEvents(auditRepository))
		subjectHandlers := []lifecyclecontract.SubjectExecutionHandler{recordSubjectLifecycle, auditSubjectLifecycle, subjectEvidence}
		if integrationOwnerSubjects != nil {
			subjectHandlers = append(subjectHandlers, integrationSubjectLifecycle{subjects: integrationOwnerSubjects, evidence: subjectEvidence})
		}
		if dataExchangeBinding != nil {
			subjectHandlers = append(subjectHandlers, dataExchangeSubjectLifecycle{binding: dataExchangeBinding})
		}
		if identitySubjectLifecycle != nil {
			subjectHandlers = append(subjectHandlers, identitySubjectLifecycle)
		}
		subjectHandlers = append(subjectHandlers, agentSubjectHandlers...)
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
		if err := bindEmbeddedIdentitySubjectLifecyclePersistence(identityBinding); err != nil {
			_ = lifecycleBinding.Close(context.WithoutCancel(ctx))
			return runtimeServiceAssembly{}, err
		}
		if integrationSubjectPersistence != nil {
			if err := integrationSubjectPersistence.BindSubjectLifecyclePersistence(ctx); err != nil {
				_ = lifecycleBinding.Close(context.WithoutCancel(ctx))
				return runtimeServiceAssembly{}, fmt.Errorf("bind embedded Integration shared subject lifecycle persistence: %w", err)
			}
		}
		if err := bindEmbeddedDataExchangeSubjectLifecyclePersistence(dataExchangeBinding); err != nil {
			_ = lifecycleBinding.Close(context.WithoutCancel(ctx))
			return runtimeServiceAssembly{}, err
		}
		store.BindSubjectLifecyclePersistence()
		accountErasureBinding, ok := lifecycleBinding.(lifecyclesdk.AccountErasureBinding)
		if !ok || !lifecycleBinding.Descriptor().Capabilities.AccountErasure || accountErasureBinding.AccountErasures() == nil {
			_ = lifecycleBinding.Close(context.WithoutCancel(ctx))
			return runtimeServiceAssembly{}, fmt.Errorf("Lifecycle Binding returned no approved account erasure queue")
		}
		accountErasures = accountErasureBinding.AccountErasures()
		if lifecycleBinding.Governance() == nil || lifecycleBinding.System() == nil {
			_ = lifecycleBinding.Close(context.WithoutCancel(ctx))
			return runtimeServiceAssembly{}, fmt.Errorf("Lifecycle Binding returned no business capabilities")
		}
	}
	var verifyFileClean func(context.Context, string, runtimeext.FileVerificationRequest) (runtimeext.FileVerificationEvidence, error)
	var openVerifiedFile func(context.Context, string, runtimeext.VerifiedFileRequest) (runtimeext.VerifiedFile, error)
	var issueFileDownload func(context.Context, string, runtimeext.Principal, runtimeext.FileDownloadRequest) (runtimeext.FileDownloadTicket, error)
	var createDerivedFile func(context.Context, string, runtimeext.DerivedFileRequest) (runtimeext.DerivedFileEvidence, error)
	var validateFileReferences func(context.Context, definitionmodel.ObjectSchema, map[string]any, principalmodel.Principal) error
	if fileCapabilities != nil {
		verifyFileClean = fileCapabilities.VerifyClean
		openVerifiedFile = fileCapabilities.OpenVerified
		issueFileDownload = fileCapabilities.IssueDownload
		createDerivedFile = fileCapabilities.CreateDerived
		validateFileReferences = fileCapabilities.ValidateRecordReferences
	}
	services := composition.NewRuntimeServices(ctx, composition.RuntimeServicesConfig{
		ProjectModel: projectModel, ProjectDefinitions: projectDefinitions,
		Actions: actionDefinitions, Integrations: integrations,
		Dependencies: composition.RuntimeServicesDependencies{
			ProductBrandName:                    cfg.EffectiveProductBrandName(),
			IdentityPrincipals:                  identityPrincipals,
			AgentTaskRunner:                     agentTaskRunner,
			AgentScheduledTasks:                 agentScheduledTasks,
			AgentBusinessEvents:                 agentBusinessEvents,
			AgentTaskAttachmentFiles:            agentTaskAttachmentFiles,
			ActionRuntimeRevision:               cfg.RuntimeVersion,
			ActionProjectRevision:               projectRevision,
			ActionMetadataRevision:              metadataRevision,
			Records:                             records,
			ReportObjectSQL:                     reportSQLStore,
			ReportSnapshotSources:               reportSQLStore,
			ReportAnalysisTables:                reportAnalysisTables,
			ReportExportPrepareReceipts:         reportExportPrepareReceipts,
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
			WorkflowRoutes:                      workflowpersistence.NewWorkflowRouteStore(store),
			WorkflowNotificationCompiler:        notificationCompiler,
			WorkflowTaskNotificationCommitter:   taskNotificationCommitter,
			RecordNotificationCompiler:          notificationCompiler,
			ReportNotificationCompiler:          notificationCompiler,
			ReportSnapshotNotificationCommitter: reportSnapshotNotificationCommitter,
			AutomationNotificationCompiler:      notificationCompiler,
			AutomationNotificationCommitter:     automationnotification.NewAutomationExecutionNotificationCommitter(store),
			NotificationIntentPublisher:         notificationIntentPublisherCallback(notificationPublisher),
			NotificationEventPublisher:          notificationEventPublisherCallback(notificationPublisher),
			AutomationWorker:                    automationpersistence.NewAutomationWorkerStore(store),
			AutomationExecutions:                automationpersistence.NewAutomationExecutionStore(store),
			ActionExecutions:                    actionpersistence.NewActionBusinessExecutionStore(store),
			ActionAssurance:                     actionpersistence.NewActionAssuranceStore(store),
			ProjectExtensions:                   projectExtensions,
			WorkspaceAggregateCatalog:           workspaceAggregates,
			WorkspaceActiveResolver:             workspaceAggregates,
			WorkspaceUsageResolver:              workspaceAggregates,
			WorkspaceAggregateRepository:        workspaceAggregates,
			WorkspaceCommercialConfiguration:    workspaceprovisionpersistence.NewCommercialConfigurationStore(store),
			VerifyFileClean:                     verifyFileClean,
			OpenVerifiedFile:                    openVerifiedFile,
			IssueFileDownload:                   issueFileDownload,
			CreateDerivedFile:                   createDerivedFile,
			ValidateFileReferences:              validateFileReferences,
			PrepareOutboxPayload: func(ctx context.Context, message publicationmodel.Message, payload map[string]any) (map[string]any, error) {
				if strings.TrimSpace(message.ConnectorKey) != "email" || strings.TrimSpace(message.Operation) != "send_file_email" {
					return payload, nil
				}
				if fileScans == nil {
					return nil, fmt.Errorf("backend.upload.capability_unselected")
				}
				fileID, contentHash, receipt := strings.TrimSpace(fmt.Sprint(payload["file_id"])), strings.TrimSpace(fmt.Sprint(payload["content_sha256"])), strings.TrimSpace(fmt.Sprint(payload["scan_receipt"]))
				evidence, err := fileScans.VerifyClean(ctx, message.WorkspaceID, fileID, contentHash, receipt)
				if err != nil {
					return nil, err
				}
				reader, err := blobs.Open(ctx, message.WorkspaceID, evidence.Filename)
				if err != nil {
					return nil, fmt.Errorf("backend.integration.email.attachment_unavailable: %w", err)
				}
				content, readErr := io.ReadAll(io.LimitReader(reader, evidence.Size+1))
				closeErr := reader.Close()
				if readErr != nil || closeErr != nil {
					return nil, fmt.Errorf("backend.integration.email.attachment_unavailable: %w", errors.Join(readErr, closeErr))
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
			RuntimeStatus:                   deploymentpersistence.NewRuntimeStatusStore(store),
			Notifications:                   notifications,
			IdentityProjection:              identityProjection,
			IdentityHandlerDeliveryBinder:   identityHandlerDeliveryBinder,
			AccountErasures:                 accountErasures,
			OrganizationUnitDeliveryBinder:  organizationUnitDeliveryBinder,
			StoreOrganizationDeliveryBinder: storeOrganizationDeliveryBinder,
			WorkspaceIdentityUsageBinder:    workspaceIdentityUsageBinder,
			WorkspaceIdentityUsageCursor:    workspaceIdentityUsageCursor,
			IntegrationOwnerDelivery:        integrationOwnerDelivery,
			IntegrationOwnerCatalog:         integrationOwnerCatalog,
			IntegrationOwnerManagement:      integrationOwnerManagement,
			IntegrationOwnerOperations:      integrationOwnerOperations,
			Worker:                          workerDependencies,
		},
	})
	installLifecycle := func() error { return nil }
	if lifecycleBinding != nil {
		lifecycleScope := lifecycleaccess.NewSystemScope(lifecycleaccess.SystemScopeInstallation, "install default lifecycle policies")
		lifecyclePrincipal := lifecycleaccess.NewSystemPrincipal("runtime-lifecycle", lifecycleScope)
		services.Applications().RuntimeStatus.ConfigureLifecycleHealth(ctx, lifecycleBinding.System())
		installLifecycle = func() error {
			return lifecycleBinding.System().InstallDefaultPolicies(ctx, principalmodel.InstallationWorkspaceID, lifecyclePrincipal, time.Now().UTC())
		}
	}
	return completeRuntimeServiceAssembly(
		runtimeServiceAssembly{services: services, records: records, worker: workerDependencies, dataExchangeBinding: dataExchangeBinding, lifecycleBinding: lifecycleBinding, fileScanProcessor: fileScanProcessor, publicResources: publicResources, blobStore: blobs},
		installLifecycle,
		func() {
			services.Applications().RecordTimers.ConfigureWorker(recordtimerapplication.WorkerConfig{Enabled: cfg.RecordTimerEnabled, PollInterval: cfg.RecordTimerPollInterval, BatchSize: cfg.RecordTimerBatchSize, LeaseTTL: cfg.RecordTimerLeaseTTL})
		},
	)
}

func bindEmbeddedDataExchangeSubjectLifecyclePersistence(binding dataexchangesdk.Binding) error {
	if binding == nil || binding.Descriptor().Mode != dataexchangesdk.DeploymentModeModule {
		return nil
	}
	persistenceBinding, ok := binding.(dataexchangesdk.SubjectLifecyclePersistenceBinding)
	if !ok {
		return fmt.Errorf("embedded Data Exchange Binding returned no shared subject lifecycle persistence binder")
	}
	if err := persistenceBinding.BindSubjectLifecyclePersistence(); err != nil {
		return fmt.Errorf("bind embedded Data Exchange shared subject lifecycle persistence: %w", err)
	}
	return nil
}

func bindEmbeddedIdentitySubjectLifecyclePersistence(binding identitysdk.Binding) error {
	if binding == nil || binding.Descriptor().Mode != identitysdk.DeploymentModeModule {
		return nil
	}
	persistenceBinding, ok := binding.(identitysdk.SubjectLifecyclePersistenceBinding)
	if !ok {
		return fmt.Errorf("embedded Identity Binding returned no shared subject lifecycle persistence binder")
	}
	if err := persistenceBinding.BindSubjectLifecyclePersistence(); err != nil {
		return fmt.Errorf("bind embedded Identity shared subject lifecycle persistence: %w", err)
	}
	return nil
}

func completeRuntimeServiceAssembly(result runtimeServiceAssembly, installLifecycle func() error, configure func()) (runtimeServiceAssembly, error) {
	if err := installLifecycle(); err != nil {
		return runtimeServiceAssembly{}, fmt.Errorf("install lifecycle policies: %w", err)
	}
	configure()
	return result, nil
}
