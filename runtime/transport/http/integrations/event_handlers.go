package integrations

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	operationshttp "github.com/domainry/domainry-runtime/runtime/transport/http/operations"
)

func (h *IntegrationsHandler) listIntegrationEvents(w http.ResponseWriter, r *http.Request) {
	limit, ok := h.readQueryLimit(w, r, 100)
	if !ok {
		return
	}
	events, err := h.runtimeExecution.ListIntegrationEvents(r.Context(), strings.TrimSpace(r.URL.Query().Get("provider")), strings.TrimSpace(r.URL.Query().Get("status")), limit, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"events": events, "count": len(events)})
}

func (h *IntegrationsHandler) recordIntegrationEvent(w http.ResponseWriter, r *http.Request) {
	var request integrationmodel.IntegrationEventRecordRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	event, duplicate, err := h.runtimeExecution.RecordIntegrationEvent(r.Context(), request, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	status := http.StatusCreated
	if duplicate {
		status = http.StatusOK
	}
	h.writeJSON(w, status, map[string]any{"event": event, "duplicate": duplicate})
}

func (h *IntegrationsHandler) recoverOfflineIntegrationEvents(w http.ResponseWriter, r *http.Request) {
	var request integrationmodel.IntegrationOfflineEventRecoveryRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	result, err := h.runtimeExecution.RecoverOfflineIntegrationEvents(r.Context(), request, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *IntegrationsHandler) processDueIntegrationEvents(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	result, err := h.runtimeExecution.ProcessDueIntegrationEvents(r.Context(), limit, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *IntegrationsHandler) updateIntegrationEventStatus(w http.ResponseWriter, r *http.Request) {
	var request integrationmodel.IntegrationEventStatusRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	event, err := h.runtimeExecution.UpdateIntegrationEventStatus(r.Context(), strings.TrimSpace(r.PathValue("eventID")), request, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, event)
}

func (h *IntegrationsHandler) scheduleIntegrationEventRetry(w http.ResponseWriter, r *http.Request) {
	var request integrationmodel.IntegrationEventRetryRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	resourceID, principal := strings.TrimSpace(r.PathValue("eventID")), h.principal(r)
	operation, err := h.executeOwnerOperation(r, "integration.event.retry", "integration_event", resourceID, request, func(ctx context.Context) (any, error) {
		return h.runtimeExecution.ScheduleIntegrationEventRetry(ctx, resourceID, request, principal)
	})
	operationshttp.WriteOwnerReceiptHeaders(w, operation)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, operation.Value)
}

func (h *IntegrationsHandler) replayIntegrationEvent(w http.ResponseWriter, r *http.Request) {
	resourceID, principal := strings.TrimSpace(r.PathValue("eventID")), h.principal(r)
	operation, err := h.executeOwnerOperation(r, "integration.event.replay", "integration_event", resourceID, nil, func(ctx context.Context) (any, error) {
		return h.runtimeExecution.ReplayIntegrationEvent(ctx, resourceID, principal)
	})
	operationshttp.WriteOwnerReceiptHeaders(w, operation)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, operation.Value)
}
