package operations

import (
	"net/http"
	"strconv"
	"strings"

	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/idempotency"
)

type operationsControlBody struct {
	Active           bool   `json:"active"`
	Reason           string `json:"reason"`
	Reference        string `json:"reference"`
	ExpectedRevision int64  `json:"expected_revision"`
}

func (h *OperationsHandler) setControl(w http.ResponseWriter, r *http.Request) {
	if h.controls == nil {
		h.writeServiceError(w, r, apperror.New(apperror.KindInternal, "backend.operations.control_unavailable", nil, nil))
		return
	}
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		h.writeServiceError(w, r, apperror.New(apperror.KindBadRequest, idempotency.ErrorCodeMissingKey, nil, nil))
		return
	}
	var body operationsControlBody
	if h.decodeJSON == nil || !h.decodeJSON(w, r, &body) {
		return
	}
	result, err := h.controls.Set(r.Context(), operationsapplication.OperationsControlRequest{
		Kind: operationsmodel.OperationsControlKind(strings.TrimSpace(r.PathValue("controlKind"))), Owner: strings.TrimSpace(r.PathValue("owner")),
		Active: body.Active, Reason: body.Reason, Reference: body.Reference, ExpectedRevision: body.ExpectedRevision,
	}, key, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Location", result.Receipt.StatusURL)
	h.writeJSON(w, http.StatusOK, result)
}

func (h *OperationsHandler) listControls(w http.ResponseWriter, r *http.Request) {
	if h.controls == nil {
		h.writeServiceError(w, r, apperror.New(apperror.KindInternal, "backend.operations.control_unavailable", nil, nil))
		return
	}
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	controls, err := h.controls.List(r.Context(), operationsmodel.OperationsControlKind(strings.TrimSpace(r.URL.Query().Get("kind"))), limit, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": controls, "count": len(controls)})
}
