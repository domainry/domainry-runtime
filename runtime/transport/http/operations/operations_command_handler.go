package operations

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	operationsapplication "github.com/domainry/domainry-runtime/runtime/application/operations"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
)

type operationsSubmitRequest struct {
	Kind         string `json:"kind"`
	ResourceType string `json:"resource_type"`
	ResourceID   string `json:"resource_id"`
	Reason       string `json:"reason"`
	Reference    string `json:"reference"`
	Payload      any    `json:"payload"`
}

func (h *OperationsHandler) operationCatalog(w http.ResponseWriter, _ *http.Request) {
	definitions := h.service.Definitions()
	h.writeJSON(w, http.StatusOK, map[string]any{"items": definitions, "count": len(definitions)})
}

func (h *OperationsHandler) submitOperation(w http.ResponseWriter, r *http.Request) {
	key := strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	if key == "" {
		h.writeServiceError(w, r, apperror.New(apperror.KindBadRequest, idempotency.ErrorCodeMissingKey, nil, nil))
		return
	}
	var body operationsSubmitRequest
	if h.decodeJSON == nil || !h.decodeJSON(w, r, &body) {
		return
	}
	receipt, decision, err := h.service.Submit(r.Context(), operationsapplication.OperationsSubmitRequest{
		Kind: body.Kind, ResourceType: body.ResourceType, ResourceID: body.ResourceID,
		Reason: body.Reason, Reference: body.Reference, Payload: body.Payload,
	}, key, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Location", receipt.StatusURL)
	status := http.StatusAccepted
	if decision == operationsmodel.OperationsSubmissionReplay {
		status = http.StatusOK
		w.Header().Set("Idempotency-Replayed", "true")
	}
	h.writeJSON(w, status, receipt)
}

func (h *OperationsHandler) getOperation(w http.ResponseWriter, r *http.Request) {
	receipt, err := h.service.Receipt(r.Context(), strings.TrimSpace(r.PathValue("operationID")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, receipt)
}

func (h *OperationsHandler) listOperations(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	values := r.URL.Query()
	page, err := h.service.SearchReceipts(r.Context(), operationsmodel.OperationsReceiptFilter{
		Status:       operationsmodel.OperationsStatus(strings.TrimSpace(values.Get("status"))),
		FailureClass: operationsmodel.OperationsFailureClass(strings.TrimSpace(values.Get("failure_class"))),
		Kind:         values.Get("kind"),
		ResourceType: values.Get("resource_type"),
		ResourceID:   values.Get("resource_id"),
		RequestedBy:  values.Get("requested_by"),
		Correlation:  values.Get("correlation"),
		CreatedFrom:  values.Get("created_from"),
		CreatedTo:    values.Get("created_to"),
		Search:       values.Get("search"),
		Limit:        limit,
	}, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, page)
}
