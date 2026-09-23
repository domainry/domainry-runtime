package operations

import (
	"net/http"
	"strconv"
	"strings"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
)

func (h *OperationsHandler) operationCatalog(w http.ResponseWriter, _ *http.Request) {
	definitions := h.service.Definitions()
	h.writeJSON(w, http.StatusOK, map[string]any{"items": definitions, "count": len(definitions)})
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
		Owner:        values.Get("owner"),
		Kind:         values.Get("kind"),
		ParentID:     values.Get("parent_operation_id"),
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
