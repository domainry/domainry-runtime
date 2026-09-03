package records

import (
	"bufio"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	"github.com/domainry/domainry-foundation/logging"
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
	key, _ := recordsActionIdempotencyKey(r)
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
	w.Header().Set("Location", "/data-exchange/jobs/"+job.ID+"?provider=records&operation=import")
	h.writeJSON(w, http.StatusAccepted, job)
}

func (h *RecordsHandler) dispatchExport(w http.ResponseWriter, r *http.Request) {
	key, _ := recordsActionIdempotencyKey(r)
	if key == "" {
		h.writeServiceError(w, r, apperror.New(apperror.KindBadRequest, idempotency.ErrorCodeMissingKey, nil, map[string]string{"use_case": "record.export"}))
		return
	}
	query := r.URL.Query()
	dispatch, err := h.queries.DispatchExportIdempotent(r.Context(), strings.TrimSpace(r.PathValue("objectKey")), key, recordapplication.RecordExportOptions{
		Fields: splitQueryCSV(query.Get("fields")), Reason: query.Get("reason"), MaskingPolicy: query.Get("masking_policy"), FilterSummary: query.Get("filter_summary"), Query: parseListQuery(r), AssuranceToken: r.Header.Get("X-Assurance-Token"),
	}, h.principal(r))
	if err != nil {
		setBatchCapacityRetryAfter(w, err)
		h.writeServiceError(w, r, err)
		return
	}
	w.Header().Set("X-Export-Delivery", dispatch.Delivery)
	if dispatch.Delivery == recordapplication.RecordExportDeliveryDirect {
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.Header().Set("Content-Disposition", "attachment; filename="+dispatch.Filename)
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write(dispatch.Content); err != nil {
			logging.FromContext(r.Context()).Error("write direct record export failed", logging.StableErrorFields(err)...)
		}
		return
	}
	if dispatch.Replayed {
		w.Header().Set("Idempotency-Replayed", "true")
	}
	w.Header().Set("Location", "/data-exchange/jobs/"+dispatch.Job.ID+"?provider=records&operation=export")
	h.writeJSON(w, http.StatusAccepted, dispatch.Job)
}

func (h *RecordsHandler) downloadExport(w http.ResponseWriter, r *http.Request) {
	artifact, err := h.queries.DownloadExport(r.Context(), strings.TrimSpace(r.PathValue("jobID")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	defer artifact.Content.Close()
	contentType := strings.TrimSpace(artifact.ContentType)
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": artifact.Filename}))
	if artifact.Size > 0 {
		w.Header().Set("Content-Length", fmt.Sprint(artifact.Size))
	}
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, artifact.Content); err != nil {
		logging.FromContext(r.Context()).Error("write record export artifact failed", logging.StableErrorFields(err)...)
	}
}

func setBatchCapacityRetryAfter(w http.ResponseWriter, err error) {
	if kind := apperror.KindOf(err); kind == apperror.KindRateLimited || kind == apperror.KindUnavailable {
		w.Header().Set("Retry-After", "5")
	}
}
