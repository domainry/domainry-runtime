package scheduler

import "net/http"

func (h *SchedulerHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /tenant-admin/scheduler/definitions", h.authenticated(h.listTenantAdminSchedulerDefinitions))
	mux.HandleFunc("GET /tenant-admin/scheduler/definitions/{definitionID}", h.authenticated(h.getTenantAdminSchedulerDefinition))
	mux.HandleFunc("GET /tenant-admin/scheduler/definitions/{definitionID}/versions", h.authenticated(h.listTenantAdminSchedulerDefinitionVersions))
	mux.HandleFunc("GET /tenant-admin/scheduler/authoring-contract", h.authenticated(h.getTenantAdminSchedulerAuthoringContract))
	mux.HandleFunc("POST /tenant-admin/scheduler/definitions/validate", h.authenticated(h.previewSchedulerJob))
	mux.HandleFunc("POST /tenant-admin/scheduler/schedules/preview", h.authenticated(h.previewSchedulerSchedule))
	mux.HandleFunc("POST /tenant-admin/scheduler/definitions/{definitionID}/simulate", h.authenticated(h.simulateSchedulerJob))
	mux.HandleFunc("GET /operations/scheduler/state", h.authenticated(h.getOpsSchedulerState))
	mux.HandleFunc("POST /operations/scheduler/definitions/{definitionID}/run", h.authenticated(h.runOpsSchedulerJob))
	mux.HandleFunc("POST /operations/scheduler/definitions/{definitionID}/reschedule", h.authenticated(h.rescheduleOpsSchedulerDefinition))
	mux.HandleFunc("POST /operations/scheduler/runs/{runID}/retry", h.authenticated(h.retryOpsSchedulerRun))
	mux.HandleFunc("POST /operations/scheduler/runs/{runID}/cancel", h.authenticated(h.cancelOpsSchedulerRun))
	mux.HandleFunc("POST /operations/scheduler/dead-letters/{deadLetterID}/resolve", h.authenticated(h.resolveOpsSchedulerDeadLetter))
	mux.HandleFunc("POST /operations/scheduler/dead-letters/{deadLetterID}/requeue", h.authenticated(h.requeueOpsSchedulerDeadLetter))

}
