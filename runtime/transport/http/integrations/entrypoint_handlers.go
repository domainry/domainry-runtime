package integrations

import (
	"net/http"
	"strings"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func (h *IntegrationsHandler) runIntegrationWorkflow(w http.ResponseWriter, r *http.Request) {
	var request integrationmodel.IntegrationEntrypointWorkflowRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	result, err := h.runtimeExecution.RunIntegrationWorkflow(r.Context(), strings.TrimSpace(r.PathValue("workflowKey")), request, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *IntegrationsHandler) executeIntegrationAction(w http.ResponseWriter, r *http.Request) {
	var request integrationmodel.IntegrationEntrypointActionRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	result, err := h.runtimeExecution.ExecuteIntegrationAction(r.Context(), strings.TrimSpace(r.PathValue("objectKey")), strings.TrimSpace(r.PathValue("recordID")), strings.TrimSpace(r.PathValue("actionKey")), request, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *IntegrationsHandler) invokeIntegrationAgentTool(w http.ResponseWriter, r *http.Request) {
	var request integrationmodel.IntegrationAgentToolInvocationRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	result, err := h.runtimeExecution.InvokeIntegrationAgentTool(r.Context(), strings.TrimSpace(r.PathValue("agentKey")), strings.TrimSpace(r.PathValue("toolKey")), request, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}
