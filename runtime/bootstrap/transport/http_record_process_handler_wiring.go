package transport

import (
	"crypto/sha256"
	"strings"

	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	uploadapplication "github.com/domainry/domainry-runtime/runtime/application/upload"
	composition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	blobstore "github.com/domainry/domainry-runtime/runtime/infrastructure/blobstore"
	dispatchpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/dispatch"
	recordpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/record"
	reportpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/report"
	lifecyclemodule "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/lifecyclemodule"
	automationhttp "github.com/domainry/domainry-runtime/runtime/transport/http/automation"
	dispatchhttp "github.com/domainry/domainry-runtime/runtime/transport/http/dispatch"
	recordhttp "github.com/domainry/domainry-runtime/runtime/transport/http/records"
	uploadhttp "github.com/domainry/domainry-runtime/runtime/transport/http/uploads"
	workflowhttp "github.com/domainry/domainry-runtime/runtime/transport/http/workflows"
)

func (a *httpServerAssembly) wireRecordAndProcessHandlers() {
	records := a.dependencies.Records
	queries := a.recordQueries
	capabilities := a.schemaCapabilities()
	a.handlers.Records = recordhttp.NewRecordsHandler(recordhttp.RecordsDependencies{
		Queries: queries, Actions: records.Applications().Actions,
		Assurance: a.actionAssuranceApplication(records.Applications().Actions),
		Audit:     records.Applications().Audit, Permissions: records.Applications().Schema,
		Principal: a.callbacks.Principal, WriteJSON: a.callbacks.WriteJSON,
		WriteError: a.callbacks.WriteError, WriteServiceError: a.callbacks.WriteServiceError, DecodeJSON: a.callbacks.DecodeJSON,
	})
	if capabilities.Uploads {
		uploadDir := strings.TrimSpace(a.dependencies.Config.UploadDir)
		if uploadDir == "" {
			uploadDir = "../data/uploads"
		}
		var artifacts lifecyclecontract.UploadArtifactStore
		var artifactContent lifecyclecontract.ArtifactContentStore
		var scans *uploadapplication.FileScanReceiptVerifier
		var tickets *uploadapplication.FileDownloadTicketService
		var uploadSubjects *uploadapplication.UploadSubjectRegistry
		if a.dependencies.Store != nil {
			uploadSubjects = uploadapplication.NewUploadSubjectRegistry(a.dependencies.Store)
		}
		if a.dependencies.BlobStore != nil {
			artifactContent = blobstore.LifecycleContentStore{Blobs: a.dependencies.BlobStore}
		}
		if a.dependencies.Store != nil && a.dependencies.LifecycleBinding != nil {
			fileStore, err := a.dependencies.LifecycleBinding.UploadArtifacts(lifecyclesdk.UploadArtifactOptions{
				Root:              uploadDir,
				Content:           artifactContent,
				Fields:            lifecyclemodule.NewUploadFieldCatalog(a.dependencies.ProjectModel.Objects),
				References:        recordpersistence.NewUploadArtifactReferences(a.dependencies.Store, a.dependencies.ProjectModel.Objects),
				ExpiredReferences: reportpersistence.NewUploadArtifactCleaner(a.dependencies.Store, a.dependencies.ProjectModel.Objects),
			})
			if err != nil {
				panic("open Lifecycle upload artifacts: " + err.Error())
			}
			artifacts = fileStore
			key := sha256.Sum256([]byte("domainry-file-scan-receipt-v1:" + a.dependencies.Config.IntegrationSecretKey))
			scans = uploadapplication.NewFileScanReceiptVerifier(fileStore, key[:])
			downloadKey := sha256.Sum256([]byte("domainry-file-download-ticket-v1:" + a.dependencies.Config.IntegrationSecretKey))
			tickets, err = uploadapplication.NewFileDownloadTicketService(downloadKey[:], nil)
			if err != nil {
				panic("initialize file download tickets: " + err.Error())
			}
		}
		a.handlers.Uploads = uploadhttp.NewUploadsHandler(uploadhttp.UploadsDependencies{
			Access: uploadapplication.NewUploadAccessApplicationService(records.Applications().Schema, records.Applications().Audit, queries, uploadSubjects),
			Blobs:  a.dependencies.BlobStore, Principal: a.callbacks.Principal, WriteJSON: a.callbacks.WriteJSON,
			WriteError: a.callbacks.WriteError, WriteServiceError: a.callbacks.WriteServiceError,
			Artifacts: artifacts, Scans: scans, Tickets: tickets,
			Subjects: uploadSubjects,
		})
	}
	if capabilities.Workflow {
		workflows := records.Applications().Workflows
		a.handlers.Workflows = workflowhttp.NewWorkflowsHandler(workflowhttp.WorkflowsDependencies{
			Definitions: workflows, Processes: workflows, Executions: workflows,
			Operations: a.operations,
			Principal:  a.callbacks.Principal, WriteJSON: a.callbacks.WriteJSON,
			WriteError: a.callbacks.WriteError, WriteServiceError: a.callbacks.WriteServiceError, DecodeJSON: a.callbacks.DecodeJSON,
		})
	}
	if capabilities.Automation {
		a.handlers.Automation = automationhttp.NewAutomationHandler(automationhttp.AutomationDependencies{
			Commands: records.Applications().Automations, Principal: a.callbacks.Principal,
			WriteJSON: a.callbacks.WriteJSON, WriteServiceError: a.callbacks.WriteServiceError,
			DecodeJSON: a.callbacks.DecodeJSON,
		})
	}
	a.handlers.Dispatch = dispatchhttp.NewExecutionHandler(dispatchhttp.TargetExecutionDependencies{
		WriteJSON:         a.callbacks.WriteJSON,
		WriteServiceError: a.callbacks.WriteServiceError,
		Executor:          composition.NewTargetExecutionDispatcher(records.Applications().TargetExecutions, records.Applications().PublicationHandoff, nil),
		Receipts:          dispatchpersistence.NewCallbackReceiptStore(a.dependencies.Store),
		RuntimeID:         a.dependencies.RuntimeInstanceID,
		SigningSecret:     []byte(a.dependencies.Config.IntegrationSecretKey),
	})
}
