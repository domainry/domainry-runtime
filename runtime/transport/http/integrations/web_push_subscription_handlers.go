package integrations

import (
	"net/http"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func (h *IntegrationsHandler) webPushReadiness(w http.ResponseWriter, r *http.Request) {
	value, err := h.connections.WebPushReadiness(r.Context(), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, value)
}

func (h *IntegrationsHandler) listWebPushSubscriptions(w http.ResponseWriter, r *http.Request) {
	values, err := h.connections.ListWebPushSubscriptions(r.Context(), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"subscriptions": values, "count": len(values)})
}
func (h *IntegrationsHandler) upsertWebPushSubscription(w http.ResponseWriter, r *http.Request) {
	var request integrationmodel.WebPushSubscriptionUpsertRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	value, err := h.connections.UpsertWebPushSubscription(r.Context(), strings.TrimSpace(r.PathValue("subscriptionID")), request, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, value)
}
func (h *IntegrationsHandler) revokeWebPushSubscription(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(r.Header.Get("Idempotency-Key")) == "" {
		h.writeServiceError(w, r, apperror.New(apperror.KindBadRequest, "backend.idempotency.key_required", nil, nil))
		return
	}
	value, err := h.connections.RevokeWebPushSubscription(r.Context(), strings.TrimSpace(r.PathValue("subscriptionID")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, value)
}
func (h *IntegrationsHandler) cleanupWebPushSubscriptions(w http.ResponseWriter, r *http.Request) {
	count, err := h.connections.CleanupExpiredWebPushSubscriptions(r.Context(), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"cleaned": count})
}
