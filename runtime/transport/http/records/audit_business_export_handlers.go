package records

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/logging"
	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
)

func (h *RecordsHandler) prepareBusinessAuditEventExport(w http.ResponseWriter, r *http.Request) {
	var request auditmodel.AuditBusinessExportRequest
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 32<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		h.writeServiceError(w, r, apperror.New(apperror.KindBadRequest, "backend.audit.export_request_invalid", err, nil))
		return
	}
	if err := ensureAuditExportJSONEnd(decoder); err != nil {
		h.writeServiceError(w, r, apperror.New(apperror.KindBadRequest, "backend.audit.export_request_invalid", err, nil))
		return
	}
	result, err := h.audit.PrepareBusinessEventExport(r.Context(), request, strings.TrimSpace(r.Header.Get("Idempotency-Key")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Cache-Control", "no-store, private")
	h.writeJSON(w, http.StatusCreated, result)
}

func (h *RecordsHandler) downloadBusinessAuditEventExport(w http.ResponseWriter, r *http.Request) {
	content, filename, err := h.audit.DownloadBusinessEventExport(r.Context(), strings.TrimSpace(r.PathValue("token")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+filename+`"`)
	w.Header().Set("Cache-Control", "no-store, private")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(content); err != nil {
		logging.FromContext(r.Context()).Error("write business audit export failed", logging.StableErrorFields(err)...)
	}
}

func ensureAuditExportJSONEnd(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		if err == nil {
			return apperror.New(apperror.KindBadRequest, "backend.audit.export_request_invalid", nil, nil)
		}
		return err
	}
	return nil
}
