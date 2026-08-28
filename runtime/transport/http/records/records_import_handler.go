package records

import (
	"io"
	"net/http"
	"strings"

	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/idempotency"
)

const maxRecordImportPayloadBytes = 2 << 20

func (h *RecordsHandler) previewImport(w http.ResponseWriter, r *http.Request) {
	rawCSV, ok := h.readCSVPayload(w, r)
	if !ok {
		return
	}
	preview, err := h.queries.PreviewImport(r.Context(), strings.TrimSpace(r.PathValue("objectKey")), rawCSV, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, preview)
}

func (h *RecordsHandler) applyImport(w http.ResponseWriter, r *http.Request) {
	rawCSV, ok := h.readCSVPayload(w, r)
	if !ok {
		return
	}
	key, _ := recordsActionIdempotencyKey(r, "")
	if key == "" {
		h.writeActionServiceError(w, r, apperror.New(apperror.KindBadRequest, idempotency.ErrorCodeMissingKey, nil, map[string]string{"use_case": "record.import"}))
		return
	}
	result, replayed, err := h.queries.ApplyImportIdempotent(r.Context(), strings.TrimSpace(r.PathValue("objectKey")), rawCSV, key, h.principal(r))
	if err != nil {
		h.writeActionServiceError(w, r, err)
		return
	}
	if replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *RecordsHandler) readCSVPayload(w http.ResponseWriter, r *http.Request) ([]byte, bool) {
	contentType := r.Header.Get("Content-Type")
	if strings.Contains(contentType, "text/csv") {
		raw, err := io.ReadAll(io.LimitReader(r.Body, maxRecordImportPayloadBytes+1))
		if err != nil {
			h.writeError(w, r, http.StatusBadRequest, "backend.import.read_csv_failed")
			return nil, false
		}
		if len(raw) > maxRecordImportPayloadBytes {
			h.writeError(w, r, http.StatusRequestEntityTooLarge, "backend.import.payload_too_large")
			return nil, false
		}
		return raw, true
	}
	var req struct {
		CSV string `json:"csv"`
	}
	if !h.decodeJSON(w, r, &req) {
		return nil, false
	}
	if strings.TrimSpace(req.CSV) == "" {
		h.writeError(w, r, http.StatusBadRequest, "backend.import.csv_required")
		return nil, false
	}
	if len(req.CSV) > maxRecordImportPayloadBytes {
		h.writeError(w, r, http.StatusRequestEntityTooLarge, "backend.import.payload_too_large")
		return nil, false
	}
	return []byte(req.CSV), true
}
