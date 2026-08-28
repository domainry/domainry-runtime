package operations

import (
	"net/http"
	"strings"

	operationsprojection "github.com/domainry/domainry-runtime/runtime/domain/operations/projection"
)

func (h *OperationsHandler) operationRunbook(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.URL.Query().Get("error_code"))
	link, found := operationsprojection.OperationsRunbookForError(code)
	if !found {
		h.writeJSON(w, http.StatusNotFound, map[string]any{"error_code": "backend.operations.runbook_not_found"})
		return
	}
	h.writeJSON(w, http.StatusOK, link)
}
