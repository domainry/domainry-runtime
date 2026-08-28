package integrations

import (
	"net/http"
	"strings"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func (h *IntegrationsHandler) listIntegrationAPIKeys(w http.ResponseWriter, r *http.Request) {
	keys, err := h.connections.ListIntegrationAPIKeys(r.Context(), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"api_keys": keys, "count": len(keys)})
}

func (h *IntegrationsHandler) createIntegrationAPIKey(w http.ResponseWriter, r *http.Request) {
	var request integrationmodel.IntegrationAPIKeyCreateRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	result, err := h.connections.CreateIntegrationAPIKey(r.Context(), request, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusCreated, result)
}

func (h *IntegrationsHandler) disableIntegrationAPIKey(w http.ResponseWriter, r *http.Request) {
	key, err := h.connections.DisableIntegrationAPIKey(r.Context(), strings.TrimSpace(r.PathValue("apiKey")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, key)
}

func (h *IntegrationsHandler) rotateIntegrationAPIKey(w http.ResponseWriter, r *http.Request) {
	result, err := h.connections.RotateIntegrationAPIKey(r.Context(), strings.TrimSpace(r.PathValue("apiKey")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}
