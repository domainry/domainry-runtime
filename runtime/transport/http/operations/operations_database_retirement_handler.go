package operations

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
)

type databaseRetirementDiscoverRequest struct {
	Object operationsmodel.DatabaseObjectIdentity `json:"object"`
	Owner  string                                 `json:"owner"`
}

type databaseRetirementAdvanceRequest struct {
	State    operationsmodel.DatabaseRetirementState    `json:"state"`
	Evidence operationsmodel.DatabaseRetirementEvidence `json:"evidence"`
}

func (h *OperationsHandler) discoverDatabaseRetirement(w http.ResponseWriter, r *http.Request) {
	if !h.requireDatabaseRetirementService(w, r) {
		return
	}
	var body databaseRetirementDiscoverRequest
	if h.decodeJSON == nil || !h.decodeJSON(w, r, &body) {
		return
	}
	retirement, err := h.databaseRetirement.Discover(r.Context(), body.Object, body.Owner, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusCreated, retirement)
}

func (h *OperationsHandler) listDatabaseRetirements(w http.ResponseWriter, r *http.Request) {
	if !h.requireDatabaseRetirementService(w, r) {
		return
	}
	limit, _ := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
	items, err := h.databaseRetirement.List(r.Context(), operationsmodel.DatabaseRetirementState(strings.TrimSpace(r.URL.Query().Get("state"))), limit, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"items": items, "count": len(items)})
}

func (h *OperationsHandler) getDatabaseRetirement(w http.ResponseWriter, r *http.Request) {
	if !h.requireDatabaseRetirementService(w, r) {
		return
	}
	retirement, err := h.databaseRetirement.OperationalStatus(r.Context(), strings.TrimSpace(r.PathValue("retirementID")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, retirement)
}

func (h *OperationsHandler) previewDatabaseRetirement(w http.ResponseWriter, r *http.Request) {
	if !h.requireDatabaseRetirementService(w, r) {
		return
	}
	plan, err := h.databaseRetirement.Preview(r.Context(), strings.TrimSpace(r.PathValue("retirementID")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.auditDatabaseRetirement(r, "preview", plan.RetirementID)
	h.writeJSON(w, http.StatusOK, plan)
}

func (h *OperationsHandler) advanceDatabaseRetirement(w http.ResponseWriter, r *http.Request) {
	if !h.requireDatabaseRetirementService(w, r) {
		return
	}
	var body databaseRetirementAdvanceRequest
	if h.decodeJSON == nil || !h.decodeJSON(w, r, &body) {
		return
	}
	id := strings.TrimSpace(r.PathValue("retirementID"))
	retirement, err := h.databaseRetirement.Advance(r.Context(), id, body.State, body.Evidence, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.auditDatabaseRetirement(r, "advance", id)
	h.writeJSON(w, http.StatusOK, retirement)
}

func (h *OperationsHandler) executeDatabaseRetirement(w http.ResponseWriter, r *http.Request) {
	if !h.requireDatabaseRetirementService(w, r) {
		return
	}
	id := strings.TrimSpace(r.PathValue("retirementID"))
	result, err := h.databaseRetirement.Execute(r.Context(), id, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.auditDatabaseRetirement(r, "execute", id)
	h.writeJSON(w, http.StatusOK, result)
}

func (h *OperationsHandler) requireDatabaseRetirementService(w http.ResponseWriter, r *http.Request) bool {
	if h.databaseRetirement != nil {
		return true
	}
	h.writeServiceError(w, r, apperror.New(apperror.KindInternal, "backend.operations.database_retirement_service_unavailable", nil, nil))
	return false
}

func (h *OperationsHandler) auditDatabaseRetirement(r *http.Request, operation, retirementID string) {
	if h.securityAudit != nil {
		h.securityAudit(r, h.principal(r), "database_retirement_"+operation, "Database retirement control-plane action", map[string]any{"retirement_id": retirementID, "operation": operation})
	}
}
