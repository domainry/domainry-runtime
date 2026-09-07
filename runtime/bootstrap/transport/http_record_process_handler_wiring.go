package transport

import (
	"crypto/sha256"
	"strings"

	lifecyclesdk "github.com/domainry/domainry-lifecycle-sdk"
	lifecyclecontract "github.com/domainry/domainry-lifecycle-sdk/contract"
	uploadapplication "github.com/domainry/domainry-runtime/runtime/application/upload"
	composition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
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
	a.handlers.Records = recordhttp.NewRecordsHandler(recordhttp.RecordsDependencies{
		Queries: queries, Actions: records.Applications().Actions,
		Assurance: a.actionAssuranceApplication(records.Applications().Actions),
		Audit:     records.Applications().Audit, Permissions: records.Applications().Schema,
		Principal: a.callbacks.Principal, WriteJSON: a.callbacks.WriteJSON,
		WriteError: a.callbacks.WriteError, WriteServiceError: a.callbacks.WriteServiceError, DecodeJSON: a.callbacks.DecodeJSON,
	})
	uploadDir := strings.TrimSpace(a.dependencies.Config.UploadDir)
	if uploadDir == "" {
		uploadDir = "../data/uploads"
	}
	var artifacts lifecyclecontract.UploadArtifactStore
	var scans *uploadapplication.FileScanReceiptVerifier
	if a.dependencies.Store != nil && a.dependencies.LifecycleBinding != nil {
		fileStore, err := a.dependencies.LifecycleBinding.UploadArtifacts(lifecyclesdk.UploadArtifactOptions{
			Root:              uploadDir,
			Fields:            lifecyclemodule.NewUploadFieldCatalog(a.dependencies.Manifest.Objects),
			References:        recordpersistence.NewUploadArtifactReferences(a.dependencies.Store, a.dependencies.Manifest.Objects),
			ExpiredReferences: reportpersistence.NewUploadArtifactCleaner(a.dependencies.Store, a.dependencies.Manifest.Objects),
		})
		if err != nil {
			panic("open Lifecycle upload artifacts: " + err.Error())
		}
		artifacts = fileStore
		key := sha256.Sum256([]byte("domainry-file-scan-receipt-v1:" + a.dependencies.Config.IntegrationSecretKey))
		scans = uploadapplication.NewFileScanReceiptVerifier(fileStore, key[:])
	}
	a.handlers.Uploads = uploadhttp.NewUploadsHandler(uploadhttp.UploadsDependencies{
		Access:    uploadapplication.NewUploadAccessApplicationService(records.Applications().Schema, records.Applications().Audit, queries),
		UploadDir: uploadDir, Principal: a.callbacks.Principal, WriteJSON: a.callbacks.WriteJSON,
		WriteError: a.callbacks.WriteError, WriteServiceError: a.callbacks.WriteServiceError,
		Artifacts: artifacts, Scans: scans,
	})
	workflows := records.Applications().Workflows
	a.handlers.Workflows = workflowhttp.NewWorkflowsHandler(workflowhttp.WorkflowsDependencies{
		Definitions: workflows, Processes: workflows, Executions: workflows,
		Operations: a.operations,
		Principal:  a.callbacks.Principal, WriteJSON: a.callbacks.WriteJSON,
		WriteError: a.callbacks.WriteError, WriteServiceError: a.callbacks.WriteServiceError, DecodeJSON: a.callbacks.DecodeJSON,
	})
	a.handlers.Automation = automationhttp.NewAutomationHandler(automationhttp.AutomationDependencies{
		Commands: records.Applications().Automations, Principal: a.callbacks.Principal,
		WriteJSON: a.callbacks.WriteJSON, WriteServiceError: a.callbacks.WriteServiceError,
		DecodeJSON: a.callbacks.DecodeJSON,
	})
	a.handlers.Dispatch = dispatchhttp.NewExecutionHandler(dispatchhttp.TargetExecutionDependencies{
		WriteJSON:         a.callbacks.WriteJSON,
		WriteServiceError: a.callbacks.WriteServiceError,
		Executor:          composition.NewTargetExecutionDispatcher(records.Applications().TargetExecutions, records.Applications().PublicationHandoff, composition.IntegrationConnectionRequirements(a.dependencies.Manifest.Integrations.Connections)),
		Receipts:          dispatchpersistence.NewCallbackReceiptStore(a.dependencies.Store),
		RuntimeID:         a.dependencies.RuntimeInstanceID,
		SigningSecret:     []byte(a.dependencies.Config.IntegrationSecretKey),
	})
}
