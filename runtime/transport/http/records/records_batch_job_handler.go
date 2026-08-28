package records

import (
	"net/http"
	"strings"

	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/idempotency"
)

func (h *RecordsHandler) enqueueImportJob(w http.ResponseWriter, r *http.Request) {
	rawCSV, ok := h.readCSVPayload(w, r)
	if !ok {
		return
	}
	key, _ := recordsActionIdempotencyKey(r, "")
	if key == "" {
		h.writeServiceError(w, r, apperror.New(apperror.KindBadRequest, idempotency.ErrorCodeMissingKey, nil, map[string]string{"use_case": "record.import.async"}))
		return
	}
	job, replayed, err := h.queries.EnqueueImportJob(r.Context(), strings.TrimSpace(r.PathValue("objectKey")), rawCSV, key, h.principal(r))
	if err != nil {
		setBatchCapacityRetryAfter(w, err)
		h.writeServiceError(w, r, err)
		return
	}
	if replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	w.Header().Set("Location", "/record-batch-jobs/"+job.ID)
	h.writeJSON(w, http.StatusAccepted, job)
}

func (h *RecordsHandler) enqueueExportJob(w http.ResponseWriter, r *http.Request) {
	key, _ := recordsActionIdempotencyKey(r, "")
	if key == "" {
		h.writeServiceError(w, r, apperror.New(apperror.KindBadRequest, idempotency.ErrorCodeMissingKey, nil, map[string]string{"use_case": "record.export.async"}))
		return
	}
	query := r.URL.Query()
	job, replayed, err := h.queries.EnqueueExportJob(r.Context(), strings.TrimSpace(r.PathValue("objectKey")), key, recordapplication.RecordExportOptions{
		Fields: splitQueryCSV(query.Get("fields")), Reason: query.Get("reason"), MaskingPolicy: query.Get("masking_policy"), FilterSummary: query.Get("filter_summary"), Query: parseListQuery(r), AssuranceToken: r.Header.Get("X-Assurance-Token"),
	}, h.principal(r))
	if err != nil {
		setBatchCapacityRetryAfter(w, err)
		h.writeServiceError(w, r, err)
		return
	}
	if replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	w.Header().Set("Location", "/record-batch-jobs/"+job.ID)
	h.writeJSON(w, http.StatusAccepted, job)
}

func setBatchCapacityRetryAfter(w http.ResponseWriter, err error) {
	if kind := apperror.KindOf(err); kind == apperror.KindRateLimited || kind == apperror.KindUnavailable {
		w.Header().Set("Retry-After", "5")
	}
}

func (h *RecordsHandler) getBatchJob(w http.ResponseWriter, r *http.Request) {
	job, err := h.queries.GetBatchJob(r.Context(), strings.TrimSpace(r.PathValue("jobID")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, job)
}

func (h *RecordsHandler) cancelBatchJob(w http.ResponseWriter, r *http.Request) {
	key, _ := recordsActionIdempotencyKey(r, "")
	if key == "" {
		h.writeServiceError(w, r, apperror.New(apperror.KindBadRequest, idempotency.ErrorCodeMissingKey, nil, map[string]string{"use_case": "record.batch.cancel"}))
		return
	}
	job, err := h.queries.CancelBatchJob(r.Context(), strings.TrimSpace(r.PathValue("jobID")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, job)
}

func (h *RecordsHandler) downloadBatchJob(w http.ResponseWriter, r *http.Request) {
	job, chunks, err := h.queries.DownloadBatchJob(r.Context(), strings.TrimSpace(r.PathValue("jobID")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Content-Type", job.ResultType)
	w.Header().Set("Content-Disposition", "attachment; filename="+job.ResultFilename)
	w.WriteHeader(http.StatusOK)
	for _, chunk := range chunks {
		if err := r.Context().Err(); err != nil {
			return
		}
		if _, err := w.Write([]byte(chunk.Content)); err != nil {
			return
		}
	}
}
