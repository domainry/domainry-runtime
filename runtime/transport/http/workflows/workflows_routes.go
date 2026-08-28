package workflows

import "net/http"

func (h *WorkflowsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /business/workflow/tasks", h.listBusinessWorkflowTasks)
	mux.HandleFunc("GET /business/workflow/team-tasks", h.listBusinessTeamWorkflowTasks)
	mux.HandleFunc("GET /business/workflow/processes", h.listBusinessWorkflowProcesses)
	mux.HandleFunc("GET /business/workflow/processes/{processID}", h.getBusinessWorkflowProcess)
	mux.HandleFunc("POST /business/workflow/processes/{processID}/withdraw", h.withdrawBusinessWorkflowProcess)
	mux.HandleFunc("POST /business/workflow/processes/{processID}/retry", h.retryBusinessWorkflowProcess)
	mux.HandleFunc("POST /business/workflow/tasks/{taskID}/approve", h.approveBusinessWorkflowTask)
	mux.HandleFunc("POST /business/workflow/tasks/{taskID}/reject", h.rejectBusinessWorkflowTask)
	mux.HandleFunc("POST /business/workflow/tasks/{taskID}/return", h.returnBusinessWorkflowTask)
	mux.HandleFunc("POST /business/workflows/{workflowKey}/run", h.runBusinessWorkflow)
	mux.HandleFunc("POST /portal/workflows/{workflowKey}/run", h.runBusinessWorkflow)
	mux.HandleFunc("GET /operations/workflow/executions", h.listOpsWorkflowExecutions)
	mux.HandleFunc("POST /operations/workflow/executions/process", h.processOpsWorkflowExecutions)
	mux.HandleFunc("POST /operations/workflow/executions/{executionID}/retry", h.retryOpsWorkflowExecution)
	mux.HandleFunc("POST /operations/workflow/executions/{executionID}/resolve", h.resolveOpsWorkflowExecution)
	mux.HandleFunc("GET /operations/workflow/processes", h.listOpsWorkflowProcesses)
	mux.HandleFunc("GET /operations/workflow/processes/{processID}", h.getOpsWorkflowProcess)
	mux.HandleFunc("POST /operations/workflow/processes/{processID}/retry", h.retryOpsWorkflowProcess)
	mux.HandleFunc("POST /operations/workflow/processes/{processID}/resolve", h.resolveOpsWorkflowProcess)
	mux.HandleFunc("POST /tenant-admin/workflows/authoring-fragments/{capabilityKey}/validate", h.validateAuthoringFragment)
	mux.HandleFunc("POST /tenant-admin/workflows/{workflowKey}/validate", h.validateWorkflowDefinition)
	mux.HandleFunc("POST /tenant-admin/workflows/{workflowKey}/simulate", h.simulateWorkflow)
}
