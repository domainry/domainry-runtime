package integrations

import (
	"net/http"
	"strings"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func (h *IntegrationsHandler) listIntegrationInvocations(w http.ResponseWriter, r *http.Request) {
	limit, ok := h.readQueryLimit(w, r, 200)
	if !ok {
		return
	}
	invocations, err := h.runtimeExecution.ListIntegrationInvocations(r.Context(), strings.TrimSpace(r.URL.Query().Get("connector_key")), strings.TrimSpace(r.URL.Query().Get("record_id")), strings.TrimSpace(r.URL.Query().Get("workflow_execution_id")), strings.TrimSpace(r.URL.Query().Get("status")), strings.TrimSpace(r.URL.Query().Get("provider")), strings.TrimSpace(r.URL.Query().Get("external_principal")), limit, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"invocations": invocations, "count": len(invocations)})
}

func (h *IntegrationsHandler) recordIntegrationInvocation(w http.ResponseWriter, r *http.Request) {
	var request integrationmodel.IntegrationInvocationRecordRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	invocation, err := h.runtimeExecution.RecordIntegrationInvocation(r.Context(), request, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusCreated, invocation)
}

func (h *IntegrationsHandler) updateIntegrationInvocationStatus(w http.ResponseWriter, r *http.Request) {
	var request integrationmodel.IntegrationInvocationStatusRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	invocation, err := h.runtimeExecution.UpdateIntegrationInvocationStatus(r.Context(), strings.TrimSpace(r.PathValue("invocationID")), request, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, invocation)
}
