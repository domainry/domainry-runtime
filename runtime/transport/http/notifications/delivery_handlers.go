package notifications

import notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"

import (
	"net/http"
	"strconv"
	"time"

	notificationcontract "github.com/domainry/domainry-runtime/runtime/domain/notification/contract"
)

func (h *NotificationsHandler) metrics(w http.ResponseWriter, r *http.Request) {
	if !h.requireAny(w, r, "integration.audit.view", notificationcontract.PermissionPolicyRead) {
		return
	}
	hours, _ := strconv.Atoi(r.URL.Query().Get("hours"))
	if hours <= 0 {
		hours = 24
	}
	if hours > 720 {
		hours = 720
	}
	since := time.Now().UTC().Add(-time.Duration(hours) * time.Hour).Format(time.RFC3339)
	value, err := h.delivery.DeliveryMetrics(r.Context(), since, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, value)
}

func (h *NotificationsHandler) governanceCatalog(w http.ResponseWriter, r *http.Request) {
	if !h.require(w, r, notificationcontract.PermissionTemplateRead) {
		return
	}
	value, err := h.management.GovernanceCatalog(r.Context(), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, value)
}

func (h *NotificationsHandler) inboxGovernanceMetrics(w http.ResponseWriter, r *http.Request) {
	if !h.requireAny(w, r, "integration.audit.view", notificationcontract.PermissionPolicyRead) {
		return
	}
	hours, _ := strconv.Atoi(r.URL.Query().Get("hours"))
	if hours <= 0 {
		hours = 24
	}
	if hours > 720 {
		hours = 720
	}
	since := time.Now().UTC().Add(-time.Duration(hours) * time.Hour).Format(time.RFC3339)
	value, err := h.inbox.InboxGovernanceMetrics(r.Context(), since, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, value)
}

func (h *NotificationsHandler) getPolicy(w http.ResponseWriter, r *http.Request) {
	if !h.requireAny(w, r, notificationcontract.PermissionPolicyRead, notificationcontract.PermissionPolicyManage) {
		return
	}
	value, err := h.delivery.GetDeliveryPolicy(r.Context(), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, value)
}

func (h *NotificationsHandler) savePolicy(w http.ResponseWriter, r *http.Request) {
	if !h.require(w, r, notificationcontract.PermissionPolicyManage) {
		return
	}
	var value notificationmodel.NotificationDeliveryPolicy
	if !h.decodeJSON(w, r, &value) {
		return
	}
	saved, err := h.delivery.SaveDeliveryPolicy(r.Context(), value, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, saved)
}

func (h *NotificationsHandler) listPreferences(w http.ResponseWriter, r *http.Request) {
	if !h.requireAny(w, r, notificationcontract.PermissionPolicyRead, notificationcontract.PermissionPolicyManage) {
		return
	}
	values, err := h.delivery.ListRecipientPreferences(r.Context(), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"preferences": values, "count": len(values)})
}

func (h *NotificationsHandler) savePreference(w http.ResponseWriter, r *http.Request) {
	if !h.require(w, r, notificationcontract.PermissionPolicyManage) {
		return
	}
	var value notificationmodel.NotificationRecipientPreference
	if !h.decodeJSON(w, r, &value) {
		return
	}
	value.RecipientKey = r.PathValue("recipientKey")
	saved, err := h.delivery.SaveRecipientPreference(r.Context(), value, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, saved)
}
