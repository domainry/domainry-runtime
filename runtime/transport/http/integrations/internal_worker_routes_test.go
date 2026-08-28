package integrations

import "net/http"

// RegisterInternalWorkerRoutesForTest keeps transport-level coverage for
// internal ingestion/state handlers after their legacy public HTTP routes were
// removed. Production RegisterRoutes must never call this helper.
func RegisterInternalWorkerRoutesForTest(h *IntegrationsHandler, mux *http.ServeMux) {
	mux.HandleFunc("GET /integrations/events", h.authenticated(h.listIntegrationEvents))
	mux.HandleFunc("POST /integrations/events", h.authenticated(h.recordIntegrationEvent))
	mux.HandleFunc("POST /integrations/events/recover-offline", h.authenticated(h.recoverOfflineIntegrationEvents))
	mux.HandleFunc("POST /integrations/events/process-due", h.authenticated(h.processDueIntegrationEvents))
	mux.HandleFunc("POST /integrations/events/{eventID}/status", h.authenticated(h.updateIntegrationEventStatus))
	mux.HandleFunc("POST /integrations/events/{eventID}/retry", h.authenticated(h.scheduleIntegrationEventRetry))
	mux.HandleFunc("POST /integrations/events/{eventID}/replay", h.authenticated(h.replayIntegrationEvent))
	mux.HandleFunc("GET /integrations/invocations", h.authenticated(h.listIntegrationInvocations))
	mux.HandleFunc("POST /integrations/invocations", h.authenticated(h.recordIntegrationInvocation))
	mux.HandleFunc("POST /integrations/invocations/{invocationID}/status", h.authenticated(h.updateIntegrationInvocationStatus))
	mux.HandleFunc("GET /integrations/outbox", h.authenticated(h.listIntegrationOutboxMessages))
	mux.HandleFunc("POST /integrations/outbox", h.authenticated(h.enqueueIntegrationOutboxMessage))
	mux.HandleFunc("POST /integrations/outbox/process-due", h.authenticated(h.processDueIntegrationOutbox))
	mux.HandleFunc("POST /integrations/outbox/{messageID}/status", h.authenticated(h.updateIntegrationOutboxStatus))
	mux.HandleFunc("POST /integrations/outbox/{messageID}/retry", h.authenticated(h.scheduleIntegrationOutboxRetry))
}
