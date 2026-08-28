package operations

import (
	"net/http"
	"strconv"
	"time"

	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
)

func (h *OperationsHandler) lifecycleCreateSubjectRequest(w http.ResponseWriter, r *http.Request) {
	var input lifecyclemodel.SubjectRequest
	if !h.decodeJSON(w, r, &input) {
		return
	}
	input.WorkspaceID = h.principal(r).WorkspaceID
	result, err := h.lifecycle.CreateSubjectRequest(r.Context(), input, h.principal(r))
	h.lifecycleWrite(w, r, result, err, http.StatusAccepted)
}

func (h *OperationsHandler) lifecycleReplayDeletions(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	principal := h.principal(r)
	count, err := h.lifecycle.ReplayRegisteredDeletions(r.Context(), principal.WorkspaceID, limit, principal)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"replayed": count})
}

func (h *OperationsHandler) lifecycleVerifySubjectRequest(w http.ResponseWriter, r *http.Request) {
	var input struct {
		SecondFactorRef string `json:"second_factor_ref"`
	}
	if !h.decodeJSON(w, r, &input) {
		return
	}
	principal := h.principal(r)
	result, err := h.lifecycle.VerifySubjectRequest(r.Context(), principal.WorkspaceID, r.PathValue("requestID"), input.SecondFactorRef, principal)
	h.lifecycleWrite(w, r, result, err, http.StatusOK)
}

func (h *OperationsHandler) lifecyclePreviewSubjectRequest(w http.ResponseWriter, r *http.Request) {
	principal := h.principal(r)
	result, err := h.lifecycle.PreviewSubjectRequest(r.Context(), principal.WorkspaceID, r.PathValue("requestID"), principal)
	h.lifecycleWrite(w, r, result, err, http.StatusOK)
}

func (h *OperationsHandler) lifecycleApproveSubjectRequest(w http.ResponseWriter, r *http.Request) {
	principal := h.principal(r)
	result, err := h.lifecycle.ApproveSubjectRequest(r.Context(), principal.WorkspaceID, r.PathValue("requestID"), principal)
	h.lifecycleWrite(w, r, result, err, http.StatusOK)
}

func (h *OperationsHandler) lifecycleExecuteSubjectRequest(w http.ResponseWriter, r *http.Request) {
	principal := h.principal(r)
	result, err := h.lifecycle.ExecuteSubjectRequest(r.Context(), principal.WorkspaceID, r.PathValue("requestID"), principal)
	h.lifecycleWrite(w, r, result, err, http.StatusOK)
}

func (h *OperationsHandler) lifecycleDownloadSubjectExport(w http.ResponseWriter, r *http.Request) {
	principal := h.principal(r)
	result, err := h.lifecycle.DownloadSubjectExport(r.Context(), principal.WorkspaceID, r.PathValue("requestID"), principal, time.Now().UTC())
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"data": result})
}

func (h *OperationsHandler) lifecycleExternalErasures(w http.ResponseWriter, r *http.Request) {
	result, err := h.lifecycle.ListExternalErasures(r.Context(), r.URL.Query().Get("request_id"), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": result, "count": len(result)})
}

func (h *OperationsHandler) lifecycleReconcileExternalErasure(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Evidence string `json:"evidence"`
	}
	if !h.decodeJSON(w, r, &input) {
		return
	}
	result, err := h.lifecycle.ReconcileExternalErasure(r.Context(), r.PathValue("erasureID"), input.Evidence, h.principal(r), time.Now().UTC())
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *OperationsHandler) lifecycleWrite(w http.ResponseWriter, r *http.Request, result lifecyclemodel.SubjectRequest, err error, status int) {
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	result.SubjectID, result.ResolvedIdentity, result.SecondFactorRef, result.ResultReference = "", "", "", ""
	result.ImpactPreview = nil
	h.writeJSON(w, status, result)
}
