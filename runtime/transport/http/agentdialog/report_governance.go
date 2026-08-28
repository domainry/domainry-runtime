package agentdialog

import (
	"context"
	"net/http"
	"strings"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	agent "github.com/domainry/domainry-runtime/runtime/application/agent"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type agentReportQueryRunRecord = agent.AgentReportQueryRun
type agentReportExportAuditRecord = agent.AgentReportExportAudit
type agentReportDownloadTaskRecord = agent.AgentReportDownloadTask

func (h *AgentDialogHandler) agentDialogRecordReportGovernance(ctx context.Context, queryRef, reportKey, objectKey, executionMode string, rowCount, total int, truncated bool, auditEventKey, workspaceID, userID, role string) map[string]any {
	if strings.TrimSpace(queryRef) == "" {
		return nil
	}
	result, err := h.reportGovernance.RecordReportGovernance(ctx, agent.AgentReportGovernanceRequest{QueryRef: strings.TrimSpace(queryRef), ReportKey: strings.TrimSpace(reportKey), ObjectKey: strings.TrimSpace(objectKey), ExecutionMode: strings.TrimSpace(executionMode), RowCount: rowCount, Total: total, Truncated: truncated, AuditEventKey: strings.TrimSpace(auditEventKey)}, principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: strings.TrimSpace(workspaceID), UserID: strings.TrimSpace(userID), RoleKey: strings.TrimSpace(role)}})
	if err != nil {
		return nil
	}
	return result
}

func (h *AgentDialogHandler) agentDialogGetReportQueryRun(w http.ResponseWriter, r *http.Request) {
	result, err := h.reportGovernance.GetReportQueryRun(r.Context(), r.PathValue("queryRef"), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}
func (h *AgentDialogHandler) agentDialogGetReportExportAudit(w http.ResponseWriter, r *http.Request) {
	result, err := h.reportGovernance.GetReportExportAudit(r.Context(), r.PathValue("queryRef"), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}
func (h *AgentDialogHandler) agentDialogGetReportDownloadTask(w http.ResponseWriter, r *http.Request) {
	result, err := h.reportGovernance.GetReportDownloadTask(r.Context(), r.PathValue("queryRef"), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *AgentDialogHandler) agentDialogPrepareReportDownloadHandoff(w http.ResponseWriter, r *http.Request) {
	principal := h.principal(r)
	queryRef := strings.TrimSpace(r.PathValue("queryRef"))
	result, err := h.reportGovernance.PrepareReportHandoff(r.Context(), queryRef, principal)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	metadata := map[string]any{"query_ref": queryRef, "report_key": result.QueryRun.ReportKey, "report_query_run_id": queryRef, "report_export_audit_id": queryRef, "download_task_id": queryRef, "handoff": "report_center_export_download", "status": "prepared_for_report_center", "scope": "server_principal_scoped"}
	agentDialogAttachExecutionIdentity(metadata, principal.UserID)
	h.securityAuditForPrincipal(r, principal, "agent_report_download_handoff_prepared", "Agent report export/download handoff prepared", metadata)
	h.writeJSON(w, http.StatusOK, map[string]any{"query_ref": queryRef, "status": "prepared_for_report_center", "handoff": "report_center_export_download", "report_query_run": result.QueryRun, "report_export_audit": result.ExportAudit, "download_task": result.DownloadTask, "scope": "server_principal_scoped", "next_step": "Continue in the generated report center export/download workflow before exposing any file link."})
}
