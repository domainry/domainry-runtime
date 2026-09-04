package scheduler

import (
	"net/http"

	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
)

func (h *SchedulerHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /scheduler/triggers/accept", h.acceptSchedulerTrigger)
	contract, err := schedulersdk.SchedulerHTTPAdapterContract()
	if err != nil {
		panic("compile Scheduler HTTP adapter: " + err.Error())
	}
	handlers := map[string]http.HandlerFunc{
		schedulersdk.ActionSchedulerDefinitionsList:       h.listManagementSchedulerDefinitions,
		schedulersdk.ActionSchedulerDefinitionsGet:        h.getManagementSchedulerDefinition,
		schedulersdk.ActionSchedulerAuthoringContractGet:  h.getManagementSchedulerAuthoringContract,
		schedulersdk.ActionSchedulerDefinitionsValidate:   h.previewSchedulerJob,
		schedulersdk.ActionSchedulerSchedulesPreview:      h.previewSchedulerSchedule,
		schedulersdk.ActionSchedulerDefinitionsSimulate:   h.simulateSchedulerJob,
		schedulersdk.ActionSchedulerStateGet:              h.getOpsSchedulerState,
		schedulersdk.ActionSchedulerDefinitionsRun:        h.runOpsSchedulerJob,
		schedulersdk.ActionSchedulerDefinitionsReschedule: h.rescheduleOpsSchedulerDefinition,
		schedulersdk.ActionSchedulerRunsRetry:             h.retryOpsSchedulerRun,
		schedulersdk.ActionSchedulerRunsCancel:            h.cancelOpsSchedulerRun,
		schedulersdk.ActionSchedulerDeadLettersResolve:    h.resolveOpsSchedulerDeadLetter,
		schedulersdk.ActionSchedulerDeadLettersRequeue:    h.requeueOpsSchedulerDeadLetter,
	}
	for _, route := range contract.Routes {
		handler, found := handlers[route.Action.Key]
		if !found {
			panic("Scheduler Action has no Runtime handler: " + route.Action.Key)
		}
		mux.HandleFunc(route.Pattern(), h.authenticated(handler))
		delete(handlers, route.Action.Key)
	}
	if len(handlers) != 0 {
		panic("Runtime Scheduler handler has no source Action")
	}
}
