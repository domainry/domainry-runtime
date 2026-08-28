package agentdialog

import "net/http"

func (h *AgentDialogHandler) RegisterRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /agent-dialog/runs", h.agentDialogRateLimited(h.agentDialogRun))
	mux.HandleFunc("POST /agent-dialog/runs/stream", h.agentDialogRateLimited(h.agentDialogRunStream))
	mux.HandleFunc("POST /agent-dialog/task-tools/invoke", h.agentTaskToolInvoke)
	mux.HandleFunc("GET /agent-dialog/runs/{runID}", h.agentDialogRunStatus)
	mux.HandleFunc("GET /agent-dialog/task-runs/{taskRunID}", h.agentDialogGetTaskRun)
	mux.HandleFunc("GET /operations/agent/tasks", h.listAgentTaskRuns)
	mux.HandleFunc("GET /operations/agent/tasks/{taskRunID}", h.getAgentTaskRun)
	mux.HandleFunc("POST /operations/agent/tasks/{taskRunID}/retry", h.retryAgentTaskRun)
	mux.HandleFunc("POST /operations/agent/tasks/{taskRunID}/cancel", h.cancelAgentTaskRun)
	mux.HandleFunc("POST /operations/agent/tasks/{taskRunID}/resolve", h.resolveAgentTaskRun)
	mux.HandleFunc("POST /operations/agent/tasks/{taskRunID}/reconcile", h.reconcileAgentTaskRun)
	mux.HandleFunc("GET /agent-dialog/diagnostics", h.admin(h.agentDialogDiagnostics))
	mux.HandleFunc("POST /agent-dialog/analysis/query", h.agentDialogRateLimited(h.agentDialogAnalysisQuery))
	mux.HandleFunc("GET /agent-dialog/sessions", h.agentDialogListSessions)
	mux.HandleFunc("POST /agent-dialog/sessions", h.agentDialogUpsertSession)
	mux.HandleFunc("POST /agent-dialog/sessions/{externalSessionID}/archive", h.agentDialogArchiveSession)
	mux.HandleFunc("POST /agent-dialog/sessions/{externalSessionID}/restore", h.agentDialogRestoreSession)
	mux.HandleFunc("GET /agent-dialog/proposals", h.agentDialogListProposals)
	mux.HandleFunc("GET /agent-dialog/proposals/{proposalID}", h.agentDialogGetProposal)
	mux.HandleFunc("POST /agent-dialog/proposals", h.agentDialogRateLimited(h.agentDialogCreateProposal))
	mux.HandleFunc("POST /agent-dialog/proposals/{proposalID}/approve", h.agentDialogRateLimited(h.agentDialogApproveProposal))
	mux.HandleFunc("POST /agent-dialog/proposals/{proposalID}/reject", h.agentDialogRateLimited(h.agentDialogRejectProposal))
	mux.HandleFunc("GET /agent-dialog/report-query-runs/{queryRef}", h.agentDialogGetReportQueryRun)
	mux.HandleFunc("GET /agent-dialog/report-export-audits/{queryRef}", h.agentDialogGetReportExportAudit)
	mux.HandleFunc("GET /agent-dialog/download-tasks/{queryRef}", h.agentDialogGetReportDownloadTask)
	mux.HandleFunc("POST /agent-dialog/download-tasks/{queryRef}/prepare", h.agentDialogRateLimited(h.agentDialogPrepareReportDownloadHandoff))
}
