package integrations

import (
	"net/http"
	"strings"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func (h *IntegrationsHandler) upsertIntegrationSecret(w http.ResponseWriter, r *http.Request) {
	var request integrationmodel.IntegrationSecretUpsertRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	secret, err := h.connections.UpsertIntegrationSecret(r.Context(), strings.TrimSpace(r.PathValue("secretKey")), request, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, secret)
}

func (h *IntegrationsHandler) disableIntegrationSecret(w http.ResponseWriter, r *http.Request) {
	secret, err := h.connections.DisableIntegrationSecret(r.Context(), strings.TrimSpace(r.PathValue("secretKey")), h.principal(r))
	h.writeIntegrationSecretResult(w, r, secret, err)
}

func (h *IntegrationsHandler) rotateIntegrationSecret(w http.ResponseWriter, r *http.Request) {
	var request integrationmodel.IntegrationSecretUpsertRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	secret, err := h.connections.RotateIntegrationSecret(r.Context(), strings.TrimSpace(r.PathValue("secretKey")), request, h.principal(r))
	h.writeIntegrationSecretResult(w, r, secret, err)
}

func (h *IntegrationsHandler) expireIntegrationSecret(w http.ResponseWriter, r *http.Request) {
	secret, err := h.connections.ExpireIntegrationSecret(r.Context(), strings.TrimSpace(r.PathValue("secretKey")), h.principal(r))
	h.writeIntegrationSecretResult(w, r, secret, err)
}

func (h *IntegrationsHandler) revokeIntegrationSecret(w http.ResponseWriter, r *http.Request) {
	secret, err := h.connections.RevokeIntegrationSecret(r.Context(), strings.TrimSpace(r.PathValue("secretKey")), h.principal(r))
	h.writeIntegrationSecretResult(w, r, secret, err)
}

func (h *IntegrationsHandler) writeIntegrationSecretResult(w http.ResponseWriter, r *http.Request, secret integrationmodel.IntegrationSecret, err error) {
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, secret)
}
