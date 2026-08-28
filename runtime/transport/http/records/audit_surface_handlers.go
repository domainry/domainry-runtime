package records

import (
	"net/http"
	"strings"

	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
)

func (h *RecordsHandler) listBusinessAuditEvents(w http.ResponseWriter, r *http.Request) {
	query := auditSurfaceQuery(r)
	principal := h.principal(r)
	if query.ObjectKey != "" && query.RecordID != "" {
		if _, err := h.queries.GetRecord(r.Context(), query.ObjectKey, query.RecordID, principal); err != nil {
			h.writeServiceError(w, r, err)
			return
		}
	}
	result, err := h.audit.BusinessEvents(r.Context(), query, principal)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *RecordsHandler) listTenantGovernanceAuditEvents(w http.ResponseWriter, r *http.Request) {
	result, err := h.audit.TenantGovernanceEvents(r.Context(), auditSurfaceQuery(r), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *RecordsHandler) exportTenantGovernanceAuditEvents(w http.ResponseWriter, r *http.Request) {
	result, err := h.audit.TenantGovernanceExport(r.Context(), auditSurfaceQuery(r), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="tenant-governance-audit.json"`)
	h.writeJSON(w, http.StatusOK, result)
}

func (h *RecordsHandler) listOperationsAuditEvents(w http.ResponseWriter, r *http.Request) {
	result, err := h.audit.OperationsEvents(r.Context(), auditSurfaceQuery(r), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *RecordsHandler) exportOperationsAuditEvents(w http.ResponseWriter, r *http.Request) {
	result, err := h.audit.OperationsExport(r.Context(), auditSurfaceQuery(r), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Content-Disposition", `attachment; filename="runtime-operations-audit.json"`)
	h.writeJSON(w, http.StatusOK, result)
}

func auditSurfaceQuery(r *http.Request) auditmodel.AuditEventQuery {
	values := r.URL.Query()
	pageSize := intQuery(values.Get("page_size"))
	if pageSize == 0 {
		pageSize = intQuery(values.Get("limit"))
	}
	return auditmodel.AuditEventQuery{
		ObjectKey:   strings.TrimSpace(values.Get("object_key")),
		RecordID:    strings.TrimSpace(values.Get("record_id")),
		Event:       strings.TrimSpace(values.Get("event")),
		ActorID:     strings.TrimSpace(values.Get("actor_id")),
		RoleKey:     strings.TrimSpace(values.Get("role_key")),
		RequestID:   strings.TrimSpace(values.Get("request_id")),
		CreatedFrom: strings.TrimSpace(values.Get("created_from")),
		CreatedTo:   strings.TrimSpace(values.Get("created_to")),
		Limit:       pageSize,
		Cursor:      strings.TrimSpace(values.Get("cursor")),
	}
}
