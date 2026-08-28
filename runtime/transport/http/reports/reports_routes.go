package reports

import "net/http"

func (h *ReportsHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /reports/{reportKey}/summary", h.reportSummary)
	mux.HandleFunc("POST /reports/{reportKey}/query", h.queryReportObjectSQL)
	mux.HandleFunc("POST /reports/{reportKey}/snapshots/refresh", h.refreshReportSnapshot)
	mux.HandleFunc("POST /reports/{reportKey}/exports/{objectKey}/prepare", h.prepareReportExport)
	mux.HandleFunc("GET /report-exports/{jobID}", h.getReportExportJob)
	mux.HandleFunc("POST /report-exports/{jobID}/cancel", h.cancelReportExportJob)
	mux.HandleFunc("GET /report-exports/downloads/{token}", h.downloadReportExport)
}
