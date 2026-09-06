package records

import (
	"net/http"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
)

func (h *RecordsHandler) listActions(w http.ResponseWriter, r *http.Request) {
	actions, err := h.actions.ActionsForObject(r.Context(), strings.TrimSpace(r.PathValue("objectKey")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, actions)
}

func (h *RecordsHandler) executeBulkAction(w http.ResponseWriter, r *http.Request) {
	var req actionmodel.ActionBulkRequest
	if r.Body != nil && r.ContentLength != 0 && !h.decodeJSON(w, r, &req) {
		return
	}
	key, err := recordsActionIdempotencyKey(r)
	if err != nil {
		h.writeActionServiceError(w, r, err)
		return
	}
	req.IdempotencyKey = key
	result, err := h.actions.ExecuteBulkAction(r.Context(), strings.TrimSpace(r.PathValue("objectKey")), strings.TrimSpace(r.PathValue("actionKey")), req, h.principal(r))
	if err != nil {
		h.writeActionServiceError(w, r, err)
		return
	}
	recordsMarkActionReplay(w, result.Message)
	h.writeJSON(w, http.StatusOK, result)
}

func (h *RecordsHandler) executeObjectAction(w http.ResponseWriter, r *http.Request) {
	var req actionmodel.ActionObjectRequest
	if r.Body != nil && r.ContentLength != 0 && !h.decodeJSON(w, r, &req) {
		return
	}
	key, err := recordsActionIdempotencyKey(r)
	if err != nil {
		h.writeActionServiceError(w, r, err)
		return
	}
	invoked, err := h.actions.Invoke(r.Context(), actionmodel.ActionSourceHTTP, actionmodel.ActionInvocation{
		ActionKey: strings.TrimSpace(r.PathValue("actionKey")), ObjectKey: strings.TrimSpace(r.PathValue("objectKey")),
		Input: req.Data, IdempotencyKey: key, TargetOrganizationID: req.TargetOrganizationID, AssuranceToken: req.AssuranceToken, Principal: h.principal(r),
	})
	if err != nil {
		h.writeActionServiceError(w, r, err)
		return
	}
	result := *invoked.Object
	if result.NoStore || invoked.NoStore {
		w.Header().Set("Cache-Control", "no-store")
	}
	recordsMarkActionReplay(w, result.Message)
	h.writeJSON(w, http.StatusOK, result)
}

func (h *RecordsHandler) executeAction(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Data                 map[string]any `json:"data"`
		TargetOrganizationID string         `json:"target_organization_id,omitempty"`
		AssuranceToken       string         `json:"assurance_token,omitempty"`
	}
	if r.Body != nil && r.ContentLength != 0 && !h.decodeJSON(w, r, &req) {
		return
	}
	objectKey := strings.TrimSpace(r.PathValue("objectKey"))
	if _, exists := req.Data["idempotency_key"]; exists {
		h.writeActionServiceError(w, r, apperror.New(apperror.KindBadRequest, "backend.validation.unknown_field", nil, map[string]string{
			"field": "idempotency_key", "object": objectKey,
		}))
		return
	}
	key, err := recordsActionIdempotencyKey(r)
	if err != nil {
		h.writeActionServiceError(w, r, err)
		return
	}
	invoked, err := h.actions.Invoke(r.Context(), actionmodel.ActionSourceHTTP, actionmodel.ActionInvocation{
		ActionKey: strings.TrimSpace(r.PathValue("actionKey")), ObjectKey: objectKey, RecordID: strings.TrimSpace(r.PathValue("recordID")),
		Input: req.Data, IdempotencyKey: key, TargetOrganizationID: req.TargetOrganizationID, AssuranceToken: req.AssuranceToken, Principal: h.principal(r),
	})
	if err != nil {
		h.writeActionServiceError(w, r, err)
		return
	}
	result := *invoked.Record
	if result.NoStore || invoked.NoStore {
		w.Header().Set("Cache-Control", "no-store")
	}
	recordsMarkActionReplay(w, result.Message)
	h.writeJSON(w, http.StatusOK, result)
}
