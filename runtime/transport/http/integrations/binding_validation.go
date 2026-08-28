package integrations

import (
	"net/http"

	businessintegration "github.com/domainry/domainry-runtime/runtime/application/integration"
)

func (h *IntegrationsHandler) validateIntegrationBinding(w http.ResponseWriter, r *http.Request) {
	var request businessintegration.BindingValidationRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	result, err := h.bindings.ValidateIntegrationBinding(r.Context(), request, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}
