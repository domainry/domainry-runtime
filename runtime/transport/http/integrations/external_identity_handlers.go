package integrations

import (
	"net/http"
	"strings"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func (h *IntegrationsHandler) listIntegrationExternalIdentities(w http.ResponseWriter, r *http.Request) {
	identities, err := h.connections.ListIntegrationExternalIdentities(r.Context(), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"identities": identities, "count": len(identities)})
}

func (h *IntegrationsHandler) upsertIntegrationExternalIdentity(w http.ResponseWriter, r *http.Request) {
	var request integrationmodel.IntegrationExternalIdentityUpsertRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	identity, err := h.connections.UpsertIntegrationExternalIdentity(r.Context(), strings.TrimSpace(r.PathValue("identityKey")), request, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, identity)
}

func (h *IntegrationsHandler) disableIntegrationExternalIdentity(w http.ResponseWriter, r *http.Request) {
	identity, err := h.connections.DisableIntegrationExternalIdentity(r.Context(), strings.TrimSpace(r.PathValue("identityKey")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, identity)
}

func (h *IntegrationsHandler) resolveIntegrationExternalIdentity(w http.ResponseWriter, r *http.Request) {
	var request integrationmodel.IntegrationExternalIdentityResolveRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	result, _, err := h.connections.ResolveIntegrationExternalIdentity(r.Context(), request, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}
