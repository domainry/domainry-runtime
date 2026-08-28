package integrations

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	integrationapplication "github.com/domainry/domainry-runtime/runtime/application/integration"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	operationshttp "github.com/domainry/domainry-runtime/runtime/transport/http/operations"
)

func (h *IntegrationsHandler) tenantAdminIntegrationCatalog(w http.ResponseWriter, r *http.Request) {
	result, err := h.connections.TenantAdminIntegrationCatalog(r.Context(), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *IntegrationsHandler) tenantAdminIntegrationSecrets(w http.ResponseWriter, r *http.Request) {
	items, err := h.connections.TenantAdminIntegrationSecrets(r.Context(), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

func (h *IntegrationsHandler) opsIntegrationActivity(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	result, err := h.runtimeExecution.OpsIntegrationActivityWithQuery(r.Context(), integrationapplication.OpsIntegrationActivityQuery{
		Kind:         r.URL.Query().Get("kind"),
		Status:       r.URL.Query().Get("status"),
		ConnectorKey: r.URL.Query().Get("connector_key"),
		Provider:     r.URL.Query().Get("provider"),
		ResourceID:   r.URL.Query().Get("resource_id"),
		Search:       r.URL.Query().Get("search"),
		Limit:        limit,
	}, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *IntegrationsHandler) retryOpsIntegrationEvent(w http.ResponseWriter, r *http.Request) {
	var request integrationmodel.IntegrationEventRetryRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	resourceID, principal := strings.TrimSpace(r.PathValue("eventID")), h.principal(r)
	if err := integrationapplication.AuthorizeOpsIntegrationRetry(principal); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	operation, err := h.executeOwnerOperation(r, "integration.event.retry", "integration_event", resourceID, request, func(ctx context.Context) (any, error) {
		value, executeErr := h.runtimeExecution.ScheduleIntegrationEventRetry(ctx, resourceID, request, principal)
		return integrationapplication.ProjectOpsIntegrationEvent(value), executeErr
	})
	operationshttp.WriteOwnerReceiptHeaders(w, operation)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, operation.Value)
}

func (h *IntegrationsHandler) replayOpsIntegrationEvent(w http.ResponseWriter, r *http.Request) {
	resourceID, principal := strings.TrimSpace(r.PathValue("eventID")), h.principal(r)
	if err := integrationapplication.AuthorizeOpsIntegrationRetry(principal); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	operation, err := h.executeOwnerOperation(r, "integration.event.replay", "integration_event", resourceID, nil, func(ctx context.Context) (any, error) {
		value, executeErr := h.runtimeExecution.ReplayIntegrationEvent(ctx, resourceID, principal)
		return integrationapplication.ProjectOpsIntegrationEvent(value), executeErr
	})
	operationshttp.WriteOwnerReceiptHeaders(w, operation)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, operation.Value)
}

func (h *IntegrationsHandler) retryOpsIntegrationOutbox(w http.ResponseWriter, r *http.Request) {
	var request integrationmodel.IntegrationOutboxRetryRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	resourceID, principal := strings.TrimSpace(r.PathValue("messageID")), h.principal(r)
	if err := integrationapplication.AuthorizeOpsIntegrationRetry(principal); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	operation, err := h.executeOwnerOperation(r, "integration.outbox.retry", "integration_outbox", resourceID, request, func(ctx context.Context) (any, error) {
		value, executeErr := h.runtimeExecution.ScheduleIntegrationOutboxRetry(ctx, resourceID, request, principal)
		return integrationapplication.ProjectOpsIntegrationOutbox(value), executeErr
	})
	operationshttp.WriteOwnerReceiptHeaders(w, operation)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, operation.Value)
}

func (h *IntegrationsHandler) processDueOpsIntegrationEvents(w http.ResponseWriter, r *http.Request) {
	principal := h.principal(r)
	if err := integrationapplication.AuthorizeOpsIntegrationRetry(principal); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	result, err := h.runtimeExecution.ProcessDueIntegrationEvents(r.Context(), limit, principal)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *IntegrationsHandler) processDueOpsIntegrationOutbox(w http.ResponseWriter, r *http.Request) {
	principal := h.principal(r)
	if err := integrationapplication.AuthorizeOpsIntegrationRetry(principal); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	result, err := h.runtimeExecution.ProcessDueIntegrationOutbox(r.Context(), limit, principal)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}
