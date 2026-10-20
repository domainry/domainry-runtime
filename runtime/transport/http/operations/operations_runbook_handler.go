package operations

import (
	"net/http"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	operationsprojection "github.com/domainry/domainry-runtime/runtime/domain/operations/projection"
)

func (h *OperationsHandler) operationRunbook(w http.ResponseWriter, r *http.Request) {
	code := strings.TrimSpace(r.URL.Query().Get("error_code"))
	link, found := operationsprojection.OperationsRunbookForError(code)
	if !found {
		h.writeServiceError(w, r, apperror.New(apperror.KindNotFound, "backend.operations.runbook_not_found", nil, nil))
		return
	}
	h.writeJSON(w, http.StatusOK, link)
}
