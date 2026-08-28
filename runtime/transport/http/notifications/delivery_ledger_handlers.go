package notifications

import (
	"net/http"
	"strconv"
	"strings"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

type notificationDeliveryProjection struct {
	ID            string         `json:"id"`
	ConnectorKey  string         `json:"connector_key"`
	ConnectionKey string         `json:"connection_key,omitempty"`
	Operation     string         `json:"operation"`
	Status        string         `json:"status"`
	Payload       map[string]any `json:"payload,omitempty"`
	ResponseRef   string         `json:"response_ref,omitempty"`
	Error         string         `json:"error,omitempty"`
	AttemptCount  int            `json:"attempt_count"`
	NextAttemptAt string         `json:"next_attempt_at,omitempty"`
	CreatedAt     string         `json:"created_at,omitempty"`
	UpdatedAt     string         `json:"updated_at,omitempty"`
}

func (h *NotificationsHandler) listDeliveries(w http.ResponseWriter, r *http.Request) {
	if !h.require(w, r, "integration.audit.view") {
		return
	}
	if h.deliveryLedger == nil {
		h.writeError(w, r, http.StatusServiceUnavailable, "backend.notification.delivery_ledger_unavailable")
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	values, err := h.deliveryLedger.ListIntegrationOutboxMessages(r.Context(), "", strings.TrimSpace(r.URL.Query().Get("status")), limit, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	projected := make([]notificationDeliveryProjection, 0, len(values))
	for _, value := range values {
		if !notificationOutboxMessage(value) {
			continue
		}
		projected = append(projected, projectNotificationDelivery(value))
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"deliveries": projected, "count": len(projected)})
}

func notificationOutboxMessage(value integrationmodel.IntegrationOutboxMessage) bool {
	templateKey, _ := value.Payload["template_key"].(string)
	return strings.TrimSpace(templateKey) != ""
}

func projectNotificationDelivery(value integrationmodel.IntegrationOutboxMessage) notificationDeliveryProjection {
	return notificationDeliveryProjection{
		ID: value.ID, ConnectorKey: value.ConnectorKey, ConnectionKey: value.ConnectionKey,
		Operation: value.Operation, Status: value.Status, Payload: value.Payload,
		ResponseRef: value.ResponseRef, Error: value.Error, AttemptCount: value.AttemptCount,
		NextAttemptAt: value.NextAttemptAt, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt,
	}
}
