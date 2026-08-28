package integrations

import (
	"net/http"
	"strconv"
	"strings"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func (h *IntegrationsHandler) listIntegrationWebhookSubscriptions(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	subscriptions, err := h.webhooks.ListIntegrationWebhookSubscriptions(r.Context(), strings.TrimSpace(r.URL.Query().Get("connector_key")), strings.TrimSpace(r.URL.Query().Get("event_type")), strings.TrimSpace(r.URL.Query().Get("status")), limit, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"subscriptions": subscriptions, "count": len(subscriptions)})
}

func (h *IntegrationsHandler) upsertIntegrationWebhookSubscription(w http.ResponseWriter, r *http.Request) {
	var request integrationmodel.IntegrationWebhookSubscriptionUpsertRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	subscription, err := h.webhooks.UpsertIntegrationWebhookSubscription(r.Context(), strings.TrimSpace(r.PathValue("subscriptionKey")), request, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, subscription)
}

func (h *IntegrationsHandler) disableIntegrationWebhookSubscription(w http.ResponseWriter, r *http.Request) {
	subscription, err := h.webhooks.DisableIntegrationWebhookSubscription(r.Context(), strings.TrimSpace(r.PathValue("subscriptionKey")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, subscription)
}

func (h *IntegrationsHandler) deleteIntegrationWebhookSubscription(w http.ResponseWriter, r *http.Request) {
	if err := h.webhooks.DeleteIntegrationWebhookSubscription(r.Context(), strings.TrimSpace(r.PathValue("subscriptionKey")), h.principal(r)); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *IntegrationsHandler) publishIntegrationWebhookEvent(w http.ResponseWriter, r *http.Request) {
	var request integrationmodel.IntegrationWebhookPublishRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	result, err := h.webhooks.PublishIntegrationWebhookEvent(r.Context(), request, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusAccepted, result)
}
