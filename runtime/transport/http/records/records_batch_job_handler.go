package records

import (
	"bufio"
	"io"
	"net/http"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func (h *RecordsHandler) enqueueImportJob(w http.ResponseWriter, r *http.Request) {
	var csvSource io.Reader = r.Body
	isCSV := strings.Contains(r.Header.Get("Content-Type"), "text/csv")
	if isCSV {
		buffered := bufio.NewReaderSize(r.Body, 1)
		if _, err := buffered.Peek(1); err != nil && err != io.EOF {
			h.writeError(w, r, http.StatusBadRequest, "backend.import.read_csv_failed")
			return
		}
		csvSource = buffered
	}
	key, _ := recordsActionIdempotencyKey(r, "")
	if key == "" {
		h.writeServiceError(w, r, apperror.New(apperror.KindBadRequest, idempotency.ErrorCodeMissingKey, nil, map[string]string{"use_case": "record.import.async"}))
		return
	}
	objectKey := strings.TrimSpace(r.PathValue("objectKey"))
	var job recordmodel.RecordBatchJob
	var replayed bool
	var err error
	if isCSV {
		job, replayed, err = h.queries.EnqueueImportStream(r.Context(), objectKey, csvSource, objectKey+".csv", r.Header.Get("Content-Type"), 128<<20, key, h.principal(r))
	} else {
		rawCSV, ok := h.readCSVPayload(w, r)
		if !ok {
			return
		}
		job, replayed, err = h.queries.EnqueueImportJob(r.Context(), objectKey, rawCSV, key, h.principal(r))
	}
	if err != nil {
		if apperror.CodeOf(err) == "backend.import.read_csv_failed" || apperror.KindOf(err) == apperror.KindBadRequest {
			h.writeError(w, r, http.StatusBadRequest, "backend.import.read_csv_failed")
			return
		}
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
	job, content, err := h.queries.OpenBatchJobDownload(r.Context(), strings.TrimSpace(r.PathValue("jobID")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	defer content.Close()
	w.Header().Set("Content-Type", job.ResultType)
	w.Header().Set("Content-Disposition", "attachment; filename="+job.ResultFilename)
	w.WriteHeader(http.StatusOK)
	ctx := r.Context()
	_, _ = io.Copy(w, readerFunc(func(buffer []byte) (int, error) {
		if err := ctx.Err(); err != nil {
			return 0, err
		}
		return content.Read(buffer)
	}))
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(buffer []byte) (int, error) {
	return f(buffer)
}
