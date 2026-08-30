package integrations

import (
	"net/http"
	"strings"
)

func (h *IntegrationsHandler) getBusinessIntegrationIntent(w http.ResponseWriter, r *http.Request) {
	result, err := h.runtimeExecution.GetBusinessIntegrationIntent(r.Context(), strings.TrimSpace(r.PathValue("messageID")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}
