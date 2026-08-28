package transport

import (
	"crypto/sha256"
	"strings"

	uploadapplication "github.com/domainry/domainry-runtime/runtime/application/upload"
	lifecyclecontract "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/contract"
	lifecyclepersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/lifecycle"
	automationhttp "github.com/domainry/domainry-runtime/runtime/transport/http/automation"
	capabilityhttp "github.com/domainry/domainry-runtime/runtime/transport/http/capabilities"
	recordhttp "github.com/domainry/domainry-runtime/runtime/transport/http/records"
	reporthttp "github.com/domainry/domainry-runtime/runtime/transport/http/reports"
	schedulerhttp "github.com/domainry/domainry-runtime/runtime/transport/http/scheduler"
	surfacecontexthttp "github.com/domainry/domainry-runtime/runtime/transport/http/surfacecontext"
	uploadhttp "github.com/domainry/domainry-runtime/runtime/transport/http/uploads"
	workflowhttp "github.com/domainry/domainry-runtime/runtime/transport/http/workflows"
)

func (a *httpServerAssembly) wireRecordAndProcessHandlers() {
	records := a.dependencies.Records
	queries := a.recordQueries
	a.handlers.Records = recordhttp.NewRecordsHandler(recordhttp.RecordsDependencies{
		Queries: queries, Actions: records.Applications().Actions,
		Audit: records.Applications().Audit, Permissions: records.Applications().Schema,
		Principal: a.callbacks.Principal, WriteJSON: a.callbacks.WriteJSON,
		WriteError: a.callbacks.WriteError, WriteServiceError: a.callbacks.WriteServiceError, DecodeJSON: a.callbacks.DecodeJSON,
	})
	a.handlers.SurfaceContext = surfacecontexthttp.NewSurfaceContextHandler(surfacecontexthttp.SurfaceContextDependencies{
		Service: records.Applications().SurfaceContext, Principal: a.callbacks.Principal,
		WriteJSON: a.callbacks.WriteJSON, WriteError: a.callbacks.WriteError, DecodeJSON: a.callbacks.DecodeJSON,
	})
	uploadDir := strings.TrimSpace(a.dependencies.Config.UploadDir)
	if uploadDir == "" {
		uploadDir = "../data/uploads"
	}
	var artifacts lifecyclecontract.UploadArtifactStore
	var scans *uploadapplication.FileScanReceiptVerifier
	if a.dependencies.Store != nil {
		fileStore := lifecyclepersistence.NewFileArtifactStore(a.dependencies.Store, a.dependencies.Manifest.Objects, uploadDir)
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
		DecodeJSON: a.callbacks.DecodeJSON, LegacyHeaders: capabilityhttp.WriteLegacyProjectionHeaders,
	})
	a.handlers.Scheduler = schedulerhttp.NewSchedulerHandler(schedulerhttp.SchedulerDependencies{
		Service: records.Applications().Scheduler, Operations: a.operations, Principal: a.callbacks.Principal,
		WriteJSON: a.callbacks.WriteJSON, WriteError: a.callbacks.WriteError,
		WriteServiceError: a.callbacks.WriteServiceError, DecodeJSON: a.callbacks.DecodeJSON, Admin: a.identityHTTP.PermissionFunc("workspace.admin"),
		Authenticated: a.identityHTTP.AuthenticatedFunc,
	})
	a.handlers.Reports = reporthttp.NewReportsHandler(reporthttp.ReportsDependencies{
		Service: records.Applications().Reports, Principal: a.callbacks.Principal,
		WriteJSON: a.callbacks.WriteJSON, WriteServiceError: a.callbacks.WriteServiceError,
	})
}
