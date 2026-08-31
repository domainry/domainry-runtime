package reports

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/logging"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	reportexport "github.com/domainry/domainry-runtime/runtime/application/report/export"
	reportexportapplication "github.com/domainry/domainry-runtime/runtime/application/report/export/application"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type ReportsHandler struct {
	queries           ReportQueryUseCases
	snapshots         ReportSnapshotUseCases
	exports           ReportExportUseCases
	principal         func(*http.Request) principalmodel.Principal
	writeJSON         func(http.ResponseWriter, int, any)
	writeServiceError func(http.ResponseWriter, *http.Request, error)
}

type ReportsDependencies struct {
	Queries           ReportQueryUseCases
	Snapshots         ReportSnapshotUseCases
	Exports           ReportExportUseCases
	Principal         func(*http.Request) principalmodel.Principal
	WriteJSON         func(http.ResponseWriter, int, any)
	WriteServiceError func(http.ResponseWriter, *http.Request, error)
}

type ReportSnapshotUseCases interface {
	RefreshSnapshot(context.Context, string, string, principalmodel.Principal) (reportmodel.ReportSnapshot, error)
}

type ReportQueryUseCases interface {
	SummaryScopedPage(context.Context, string, string, string, []string, reportmodel.ReportPageRequest, principalmodel.Principal) (reportmodel.ReportSummary, error)
	QueryObjectSQLPage(context.Context, string, map[string]any, reportmodel.ReportPageRequest, principalmodel.Principal) (reportmodel.ReportSummary, error)
}

type ReportExportUseCases interface {
	PrepareExportRouted(context.Context, string, string, string, string, reportmodel.ReportExportScopeRequest, principalmodel.Principal) (reportexportapplication.ReportExportPreparation, error)
	GetExportJob(context.Context, string, principalmodel.Principal) (reportexport.ExchangeJob, error)
	CancelExportJob(context.Context, string, principalmodel.Principal) (reportexport.ExchangeJob, error)
	DownloadExport(context.Context, string, principalmodel.Principal) ([]byte, string, error)
}

func NewReportsHandler(deps ReportsDependencies) *ReportsHandler {
	return &ReportsHandler{
		queries: deps.Queries, snapshots: deps.Snapshots, exports: deps.Exports, principal: deps.Principal,
		writeJSON: deps.WriteJSON, writeServiceError: deps.WriteServiceError,
	}
}

func (h *ReportsHandler) reportSummary(w http.ResponseWriter, r *http.Request) {
	page, err := reportPageRequest(r)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	summary, err := h.queries.SummaryScopedPage(r.Context(), strings.TrimSpace(r.PathValue("reportKey")), strings.TrimSpace(r.URL.Query().Get("mode")), strings.TrimSpace(r.URL.Query().Get("query_key")), r.URL.Query()["tags"], page, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, summary)
}

func (h *ReportsHandler) queryReportObjectSQL(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Parameters map[string]any `json:"parameters"`
		PageSize   int            `json:"page_size,omitempty"`
		Cursor     string         `json:"cursor,omitempty"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(&request); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		h.writeServiceError(w, r, err)
		return
	}
	if request.Parameters == nil {
		request.Parameters = map[string]any{}
	}
	summary, err := h.queries.QueryObjectSQLPage(r.Context(), strings.TrimSpace(r.PathValue("reportKey")), request.Parameters, reportmodel.ReportPageRequest{PageSize: request.PageSize, Cursor: request.Cursor}, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, summary)
}

func reportPageRequest(r *http.Request) (reportmodel.ReportPageRequest, error) {
	raw := strings.TrimSpace(r.URL.Query().Get("page_size"))
	pageSize := 0
	if raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil {
			return reportmodel.ReportPageRequest{}, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.report.page_size_invalid"}
		}
		pageSize = value
	}
	return reportmodel.ReportPageRequest{PageSize: pageSize, Cursor: strings.TrimSpace(r.URL.Query().Get("cursor"))}, nil
}

func (h *ReportsHandler) refreshReportSnapshot(w http.ResponseWriter, r *http.Request) {
	snapshot, err := h.snapshots.RefreshSnapshot(r.Context(), strings.TrimSpace(r.PathValue("reportKey")), strings.TrimSpace(r.Header.Get("Idempotency-Key")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, snapshot)
}

func (h *ReportsHandler) prepareReportExport(w http.ResponseWriter, r *http.Request) {
	var request struct {
		AuditID string                               `json:"audit_id"`
		Scope   reportmodel.ReportExportScopeRequest `json:"scope"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(&request); err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	result, err := h.exports.PrepareExportRouted(
		r.Context(), strings.TrimSpace(r.PathValue("reportKey")), strings.TrimSpace(r.PathValue("objectKey")),
		strings.TrimSpace(request.AuditID), strings.TrimSpace(r.Header.Get("Idempotency-Key")), request.Scope, h.principal(r),
	)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	w.Header().Set("Location", "/report-exports/"+result.Job.ID)
	h.writeJSON(w, http.StatusAccepted, result.Job)
}

func (h *ReportsHandler) getReportExportJob(w http.ResponseWriter, r *http.Request) {
	result, err := h.exports.GetExportJob(r.Context(), strings.TrimSpace(r.PathValue("jobID")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *ReportsHandler) cancelReportExportJob(w http.ResponseWriter, r *http.Request) {
	result, err := h.exports.CancelExportJob(r.Context(), strings.TrimSpace(r.PathValue("jobID")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *ReportsHandler) downloadReportExport(w http.ResponseWriter, r *http.Request) {
	content, filename, err := h.exports.DownloadExport(r.Context(), strings.TrimSpace(r.PathValue("token")), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeExportFile(w, r, content, filename)
}

func (h *ReportsHandler) writeExportFile(w http.ResponseWriter, r *http.Request, content []byte, filename string) {
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", "attachment; filename="+filename)
	w.Header().Set("Cache-Control", "no-store, private")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write(content); err != nil {
		logging.FromContext(r.Context()).Error("write report export failed", logging.StableErrorFields(err)...)
	}
}
