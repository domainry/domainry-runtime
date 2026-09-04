package workflows

import "net/http"

func (h *WorkflowsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /workflow/tasks", h.listParticipantWorkflowTasks)
	mux.HandleFunc("GET /workflow/team-tasks", h.listBusinessTeamWorkflowTasks)
	mux.HandleFunc("GET /workflow/processes", h.listParticipantWorkflowProcesses)
	mux.HandleFunc("GET /workflow/processes/{processID}", h.getParticipantWorkflowProcess)
	mux.HandleFunc("POST /workflow/processes/{processID}/withdraw", h.withdrawParticipantWorkflowProcess)
	mux.HandleFunc("POST /workflow/processes/{processID}/retry", h.retryParticipantWorkflowProcess)
	mux.HandleFunc("POST /workflow/tasks/{taskID}/approve", h.approveParticipantWorkflowTask)
	mux.HandleFunc("POST /workflow/tasks/{taskID}/reject", h.rejectParticipantWorkflowTask)
	mux.HandleFunc("POST /workflow/tasks/{taskID}/return", h.returnParticipantWorkflowTask)
	mux.HandleFunc("POST /workflow/definitions/{workflowKey}/run", h.runParticipantWorkflow)
	mux.HandleFunc("GET /workflow/operations/executions", h.listOpsWorkflowExecutions)
	mux.HandleFunc("POST /workflow/operations/executions/process", h.processOpsWorkflowExecutions)
	mux.HandleFunc("POST /workflow/operations/executions/{executionID}/retry", h.retryOpsWorkflowExecution)
	mux.HandleFunc("POST /workflow/operations/executions/{executionID}/resolve", h.resolveOpsWorkflowExecution)
	mux.HandleFunc("GET /workflow/operations/processes", h.listOpsWorkflowProcesses)
	mux.HandleFunc("GET /workflow/operations/processes/{processID}", h.getOpsWorkflowProcess)
	mux.HandleFunc("POST /workflow/operations/processes/{processID}/retry", h.retryOpsWorkflowProcess)
	mux.HandleFunc("POST /workflow/operations/processes/{processID}/resolve", h.resolveOpsWorkflowProcess)
	mux.HandleFunc("POST /workflow/authoring-fragments/{capabilityKey}/validate", h.validateAuthoringFragment)
	mux.HandleFunc("POST /workflow/definitions/{workflowKey}/validate", h.validateWorkflowDefinition)
	mux.HandleFunc("POST /workflow/definitions/{workflowKey}/simulate", h.simulateWorkflow)
}
