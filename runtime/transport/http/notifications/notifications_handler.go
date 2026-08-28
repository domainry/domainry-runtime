package notifications

import (
	"context"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"net/http"
	"strconv"
	"strings"
	"time"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"

	notificationcontract "github.com/domainry/domainry-runtime/runtime/domain/notification/contract"
)

type NotificationsHandler struct {
	management         NotificationManagement
	delivery           NotificationDelivery
	inbox              NotificationInbox
	deliveryLedger     NotificationDeliveryLedger
	principal          func(*http.Request) principalmodel.Principal
	writeJSON          func(http.ResponseWriter, int, any)
	writeError         func(http.ResponseWriter, *http.Request, int, string, ...string)
	writeServiceError  func(http.ResponseWriter, *http.Request, error)
	decodeJSON         func(http.ResponseWriter, *http.Request, any) bool
	authenticated      func(http.HandlerFunc) http.HandlerFunc
	streamPollInterval time.Duration
	streamHeartbeat    time.Duration
}

type NotificationsDependencies struct {
	Management         NotificationManagement
	Delivery           NotificationDelivery
	Inbox              NotificationInbox
	DeliveryLedger     NotificationDeliveryLedger
	Principal          func(*http.Request) principalmodel.Principal
	WriteJSON          func(http.ResponseWriter, int, any)
	WriteError         func(http.ResponseWriter, *http.Request, int, string, ...string)
	WriteServiceError  func(http.ResponseWriter, *http.Request, error)
	DecodeJSON         func(http.ResponseWriter, *http.Request, any) bool
	Authenticated      func(http.HandlerFunc) http.HandlerFunc
	StreamPollInterval time.Duration
	StreamHeartbeat    time.Duration
}

func NewNotificationsHandler(deps NotificationsDependencies) *NotificationsHandler {
	poll, heartbeat := deps.StreamPollInterval, deps.StreamHeartbeat
	if poll <= 0 {
		poll = 2 * time.Second
	}
	if heartbeat <= 0 {
		heartbeat = 15 * time.Second
	}
	return &NotificationsHandler{management: deps.Management, delivery: deps.Delivery, inbox: deps.Inbox, deliveryLedger: deps.DeliveryLedger, principal: deps.Principal, writeJSON: deps.WriteJSON, writeError: deps.WriteError, writeServiceError: deps.WriteServiceError, decodeJSON: deps.DecodeJSON, authenticated: deps.Authenticated, streamPollInterval: poll, streamHeartbeat: heartbeat}
}

type NotificationDeliveryLedger interface {
	ListIntegrationOutboxMessages(context.Context, string, string, int, principalmodel.Principal) ([]integrationmodel.IntegrationOutboxMessage, error)
}

func (h *NotificationsHandler) listPublications(w http.ResponseWriter, r *http.Request) {
	if !h.require(w, r, notificationcontract.PermissionTemplateRead) {
		return
	}
	values, err := h.management.ListPublicationRequests(r.Context(), r.URL.Query().Get("template_key"), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"publications": values, "count": len(values)})
}

type publicationRequestInput struct {
	ExpectedUpdatedAt string `json:"expected_updated_at,omitempty"`
	ScheduledFor      string `json:"scheduled_for,omitempty"`
}

func (h *NotificationsHandler) requestPublication(w http.ResponseWriter, r *http.Request) {
	if !h.require(w, r, notificationcontract.PermissionTemplatePublish) {
		return
	}
	var request publicationRequestInput
	if !h.decodeJSON(w, r, &request) {
		return
	}
	value, err := h.management.RequestPublication(r.Context(), r.PathValue("templateKey"), request.ScheduledFor, request.ExpectedUpdatedAt, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusCreated, value)
}

func (h *NotificationsHandler) approvePublication(w http.ResponseWriter, r *http.Request) {
	if !h.require(w, r, notificationcontract.PermissionTemplateApprove) {
		return
	}
	value, err := h.management.ApprovePublication(r.Context(), r.PathValue("publicationID"), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, value)
}

type publicationReviewInput struct {
	Reason string `json:"reason,omitempty"`
}

func (h *NotificationsHandler) rejectPublication(w http.ResponseWriter, r *http.Request) {
	if !h.require(w, r, notificationcontract.PermissionTemplateApprove) {
		return
	}
	var request publicationReviewInput
	if !h.decodeJSON(w, r, &request) {
		return
	}
	value, err := h.management.RejectPublication(r.Context(), r.PathValue("publicationID"), request.Reason, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, value)
}

func (h *NotificationsHandler) cancelPublication(w http.ResponseWriter, r *http.Request) {
	if !h.requireAny(w, r, notificationcontract.PermissionTemplatePublish, notificationcontract.PermissionTemplateApprove) {
		return
	}
	value, err := h.management.CancelPublication(r.Context(), r.PathValue("publicationID"), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, value)
}

func (h *NotificationsHandler) capabilities(w http.ResponseWriter, r *http.Request) {
	if !h.require(w, r, notificationcontract.PermissionTemplateRead) {
		return
	}
	values, err := h.management.Capabilities(r.Context(), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"capabilities": values, "count": len(values)})
}

func (h *NotificationsHandler) list(w http.ResponseWriter, r *http.Request) {
	if !h.require(w, r, notificationcontract.PermissionTemplateRead) {
		return
	}
	values, err := h.management.List(r.Context(), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"templates": values, "count": len(values)})
}

func (h *NotificationsHandler) get(w http.ResponseWriter, r *http.Request) {
	if !h.require(w, r, notificationcontract.PermissionTemplateRead) {
		return
	}
	value, found, err := h.management.Get(r.Context(), r.PathValue("templateKey"), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if !found {
		h.writeError(w, r, http.StatusNotFound, "backend.notification.template_not_found")
		return
	}
	h.writeJSON(w, http.StatusOK, value)
}

func (h *NotificationsHandler) listVersions(w http.ResponseWriter, r *http.Request) {
	if !h.require(w, r, notificationcontract.PermissionTemplateRead) {
		return
	}
	values, err := h.management.ListVersions(r.Context(), r.PathValue("templateKey"), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"versions": values, "count": len(values)})
}

func (h *NotificationsHandler) restoreVersionDraft(w http.ResponseWriter, r *http.Request) {
	if !h.require(w, r, notificationcontract.PermissionTemplateManage) {
		return
	}
	var request lifecycleRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	version, err := strconv.Atoi(strings.TrimSpace(r.PathValue("version")))
	if err != nil || version < 1 {
		h.writeError(w, r, http.StatusBadRequest, "backend.notification.template_version_invalid")
		return
	}
	value, err := h.management.RestoreVersionDraft(r.Context(), r.PathValue("templateKey"), version, request.ExpectedUpdatedAt, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, value)
}

type draftRequest struct {
	Template          notificationmodel.NotificationTemplate `json:"template"`
	ExpectedUpdatedAt string                                 `json:"expected_updated_at,omitempty"`
}

func (h *NotificationsHandler) saveDraft(w http.ResponseWriter, r *http.Request) {
	if !h.require(w, r, notificationcontract.PermissionTemplateManage) {
		return
	}
	var request draftRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	value, err := h.management.SaveDraft(r.Context(), r.PathValue("templateKey"), request.Template, request.ExpectedUpdatedAt, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, value)
}

type lifecycleRequest struct {
	ExpectedUpdatedAt string `json:"expected_updated_at,omitempty"`
}

func (h *NotificationsHandler) publish(w http.ResponseWriter, r *http.Request) {
	if !h.require(w, r, notificationcontract.PermissionTemplateApprove) {
		return
	}
	// Keep the legacy endpoint registered so older clients receive a precise
	// migration error instead of a misleading 404, but never let it bypass the
	// immutable request snapshot and two-person approval workflow.
	h.writeError(w, r, http.StatusConflict, "backend.notification.publication_request_required", "template_key", r.PathValue("templateKey"))
}

func (h *NotificationsHandler) disable(w http.ResponseWriter, r *http.Request) {
	if !h.require(w, r, notificationcontract.PermissionTemplatePublish) {
		return
	}
	var request lifecycleRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	value, err := h.management.Disable(r.Context(), r.PathValue("templateKey"), request.ExpectedUpdatedAt, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, value)
}

type previewRequest struct {
	Locale     string         `json:"locale,omitempty"`
	Recipients []string       `json:"recipients,omitempty"`
	Variables  map[string]any `json:"variables,omitempty"`
}

type templatePreviewRequest struct {
	Template   notificationmodel.NotificationTemplate `json:"template"`
	Locale     string                                 `json:"locale,omitempty"`
	Recipients []string                               `json:"recipients,omitempty"`
	Variables  map[string]any                         `json:"variables,omitempty"`
}

func (h *NotificationsHandler) previewTemplate(w http.ResponseWriter, r *http.Request) {
	if !h.requireAny(w, r, notificationcontract.PermissionTemplateManage, notificationcontract.PermissionTemplateTest) {
		return
	}
	var request templatePreviewRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	if len(request.Recipients) == 0 {
		request.Recipients = []string{"preview@example.com"}
	}
	value, err := h.management.PreviewTemplate(r.Context(), request.Template, request.Locale, request.Recipients, request.Variables, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, value)
}

func (h *NotificationsHandler) preview(w http.ResponseWriter, r *http.Request) {
	if !h.requireAny(w, r, notificationcontract.PermissionTemplateRead, notificationcontract.PermissionTemplateTest) {
		return
	}
	var request previewRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	if len(request.Recipients) == 0 {
		request.Recipients = []string{"preview@example.com"}
	}
	value, err := h.management.Preview(r.Context(), r.PathValue("templateKey"), request.Locale, request.Recipients, request.Variables, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, value)
}

func (h *NotificationsHandler) require(w http.ResponseWriter, r *http.Request, permission string) bool {
	return h.requireAny(w, r, permission)
}

func (h *NotificationsHandler) requireAny(w http.ResponseWriter, r *http.Request, permissions ...string) bool {
	principal := h.principal(r)
	if principal.Known && principal.HasPermission("workspace.admin") {
		return true
	}
	for _, permission := range permissions {
		if principal.Known && principal.HasPermission(strings.TrimSpace(permission)) {
			return true
		}
	}
	h.writeError(w, r, http.StatusForbidden, "auth.permission_denied")
	return false
}
