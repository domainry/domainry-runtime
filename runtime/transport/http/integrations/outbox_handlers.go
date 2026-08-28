package integrations

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	operationshttp "github.com/domainry/domainry-runtime/runtime/transport/http/operations"
)

func (h *IntegrationsHandler) listIntegrationOutboxMessages(w http.ResponseWriter, r *http.Request) {
	limit, ok := h.readQueryLimit(w, r, 200)
	if !ok {
		return
	}
	messages, err := h.runtimeExecution.ListIntegrationOutboxMessages(r.Context(), strings.TrimSpace(r.URL.Query().Get("connector_key")), strings.TrimSpace(r.URL.Query().Get("status")), limit, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"messages": messages, "count": len(messages)})
}

func (h *IntegrationsHandler) getBusinessIntegrationIntent(w http.ResponseWriter, r *http.Request) {
	result, err := h.runtimeExecution.GetBusinessIntegrationIntent(r.Context(), strings.TrimSpace(r.PathValue("messageID")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *IntegrationsHandler) enqueueIntegrationOutboxMessage(w http.ResponseWriter, r *http.Request) {
	var request integrationmodel.IntegrationOutboxEnqueueRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	headerKey := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if headerKey != "" && strings.TrimSpace(request.RequestRef) != "" && headerKey != strings.TrimSpace(request.RequestRef) {
		h.writeError(w, r, http.StatusBadRequest, "backend.idempotency.key_mismatch")
		return
	}
	if request.RequestRef == "" {
		request.RequestRef = headerKey
	}
	if strings.TrimSpace(request.DedupKey) == "" {
		request.DedupKey = headerKey
	}
	message, err := h.runtimeExecution.EnqueueIntegrationOutboxMessage(r.Context(), request, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusCreated, message)
}

func (h *IntegrationsHandler) processDueIntegrationOutbox(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	result, err := h.runtimeExecution.ProcessDueIntegrationOutbox(r.Context(), limit, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *IntegrationsHandler) updateIntegrationOutboxStatus(w http.ResponseWriter, r *http.Request) {
	var request integrationmodel.IntegrationOutboxStatusRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	message, err := h.runtimeExecution.UpdateIntegrationOutboxStatus(r.Context(), strings.TrimSpace(r.PathValue("messageID")), request, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, message)
}

func (h *IntegrationsHandler) scheduleIntegrationOutboxRetry(w http.ResponseWriter, r *http.Request) {
	var request integrationmodel.IntegrationOutboxRetryRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	resourceID, principal := strings.TrimSpace(r.PathValue("messageID")), h.principal(r)
	operation, err := h.executeOwnerOperation(r, "integration.outbox.retry", "integration_outbox", resourceID, request, func(ctx context.Context) (any, error) {
		return h.runtimeExecution.ScheduleIntegrationOutboxRetry(ctx, resourceID, request, principal)
	})
	operationshttp.WriteOwnerReceiptHeaders(w, operation)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, operation.Value)
}
