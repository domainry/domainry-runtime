package agentdialog

import (
	"context"
	"net/http"
	"sort"
	"strings"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

type agentAnalysisQueryRequest struct {
	Intent     string         `json:"intent"`
	SQL        string         `json:"sql,omitempty"`
	MetricSpec map[string]any `json:"metric_spec,omitempty"`
	DryRun     bool           `json:"dry_run,omitempty"`
	MaxRows    int            `json:"max_rows,omitempty"`
}

func (h *AgentDialogHandler) agentDialogAnalysisQuery(w http.ResponseWriter, r *http.Request) {
	var payload agentAnalysisQueryRequest
	if !h.decodeJSON(w, r, &payload) {
		return
	}
	intent := strings.TrimSpace(payload.Intent)
	if intent == "" {
		h.writeError(w, r, http.StatusBadRequest, "agent_analysis.intent_required")
		return
	}
	sqlText := strings.TrimSpace(payload.SQL)
	if sqlText != "" {
		if reason := agentAnalysisSQLRejectReason(sqlText); reason != "" {
			h.writeError(w, r, http.StatusBadRequest, reason)
			return
		}
	}
	principal := h.principal(r)
	snapshot := h.analysisCatalog.ForPrincipal(r.Context(), principal)
	objectKey := agentAnalysisSpecString(payload.MetricSpec, "object_key")
	reportKey := agentAnalysisSpecString(payload.MetricSpec, "report_key")
	if objectKey != "" && !agentAnalysisObjectVisible(snapshot.Objects, objectKey) {
		h.writeError(w, r, http.StatusForbidden, "agent_analysis.object_denied")
		return
	}
	if reportKey != "" && !agentAnalysisReportVisible(snapshot.Reports, reportKey) {
		h.writeError(w, r, http.StatusForbidden, "agent_analysis.report_denied")
		return
	}
	maxRows := payload.MaxRows
	if maxRows <= 0 || maxRows > 100 {
		maxRows = 100
	}
	maskedFields, err := h.agentAnalysisMaskedFields(r.Context(), objectKey, principal)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	queryRef := agentAnalysisQueryRef(intent, sqlText, payload.MetricSpec, principal.WorkspaceID, principal.RoleKey)
	proposalSuggestion := agentAnalysisProposalSuggestion(payload.MetricSpec, intent, queryRef)
	metadata := map[string]any{"query_ref": queryRef, "object_key": objectKey, "report_key": reportKey, "masked_fields": maskedFields, "has_sql": sqlText != "", "dry_run": payload.DryRun, "max_rows": maxRows, "gateway": "agent_analysis_query_gateway", "scope": "server_principal_scoped", "workspace_id": principal.WorkspaceID, "user_id": principal.UserID, "role": principal.RoleKey}
	if len(proposalSuggestion) > 0 {
		metadata["proposal_suggestion"] = proposalSuggestion
	}
	agentDialogAttachExecutionIdentity(metadata, principal.UserID)
	if payload.DryRun || sqlText != "" || objectKey == "" {
		h.writeAgentAnalysisValidation(w, r, payload, principal, intent, queryRef, objectKey, reportKey, maskedFields, proposalSuggestion, metadata)
		return
	}
	page, err := h.analysisRecords.ListRecords(r.Context(), objectKey, recordmodel.RecordListQuery{Page: 1, PageSize: maxRows, Filters: agentAnalysisFilters(payload.MetricSpec)}, principal)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeAgentAnalysisResult(w, r, principal, intent, queryRef, objectKey, reportKey, maskedFields, proposalSuggestion, metadata, page)
}

func (h *AgentDialogHandler) writeAgentAnalysisValidation(w http.ResponseWriter, r *http.Request, payload agentAnalysisQueryRequest, principal principalmodel.Principal, intent, queryRef, objectKey, reportKey string, maskedFields []string, proposalSuggestion, metadata map[string]any) {
	auditEventKey := "agent_analysis_query_validated"
	h.securityAuditForPrincipal(r, principal, auditEventKey, "Agent analysis query validated", metadata)
	governanceRefs := h.agentDialogRecordReportGovernance(r.Context(), queryRef, reportKey, objectKey, "gateway_validation", 0, 0, false, auditEventKey, principal.WorkspaceID, principal.UserID, principal.RoleKey)
	scopeNote := "Validated by the generated backend with the current workspace, role, and visible schema. Raw SQL execution is deferred until the permission-aware SQL executor is bound."
	htmlFragment := agentAnalysisValidationHTML(intent, queryRef, reportKey, scopeNote) + agentAnalysisProposalHTML(proposalSuggestion)
	h.writeJSON(w, http.StatusOK, map[string]any{"query_ref": queryRef, "status": "validated", "execution_mode": "gateway_validation", "html_fragment": htmlFragment, "rendered_report": agentAnalysisRenderedReport("message_card", htmlFragment, queryRef, reportKey), "report_provenance": agentAnalysisReportProvenance(queryRef, reportKey, objectKey, 0, 0, false, auditEventKey), "report_governance": governanceRefs, "scope_note": scopeNote, "report_center_ref": reportKey, "proposal_suggestion": proposalSuggestion, "workspace_id": principal.WorkspaceID, "role": principal.RoleKey, "object_key": objectKey, "report_key": reportKey, "masked_fields": maskedFields, "truncated": false, "audit_event_key": auditEventKey})
}

func (h *AgentDialogHandler) writeAgentAnalysisResult(w http.ResponseWriter, r *http.Request, principal principalmodel.Principal, intent, queryRef, objectKey, reportKey string, maskedFields []string, proposalSuggestion, metadata map[string]any, page recordmodel.RecordPageResult) {
	auditEventKey := "agent_analysis_query_executed"
	h.securityAuditForPrincipal(r, principal, auditEventKey, "Agent analysis query executed through scoped record APIs", metadata)
	governanceRefs := h.agentDialogRecordReportGovernance(r.Context(), queryRef, reportKey, objectKey, "scoped_record_query", len(page.Items), page.Total, page.HasNext, auditEventKey, principal.WorkspaceID, principal.UserID, principal.RoleKey)
	scopeNote := "Result rows were read through generated record APIs using the current principal, data scope, and field visibility."
	htmlFragment := agentAnalysisReportHTML(intent, queryRef, objectKey, reportKey, page.Items, scopeNote) + agentAnalysisProposalHTML(proposalSuggestion)
	h.writeJSON(w, http.StatusOK, map[string]any{"query_ref": queryRef, "status": "completed", "execution_mode": "scoped_record_query", "html_fragment": htmlFragment, "rendered_report": agentAnalysisRenderedReport("report_card", htmlFragment, queryRef, reportKey), "report_provenance": agentAnalysisReportProvenance(queryRef, reportKey, objectKey, len(page.Items), page.Total, page.HasNext, auditEventKey), "report_governance": governanceRefs, "scope_note": scopeNote, "report_center_ref": reportKey, "proposal_suggestion": proposalSuggestion, "workspace_id": principal.WorkspaceID, "role": principal.RoleKey, "object_key": objectKey, "report_key": reportKey, "rows": page.Items, "row_count": len(page.Items), "total": page.Total, "truncated": page.HasNext, "masked_fields": maskedFields, "audit_event_key": auditEventKey})
}

func (h *AgentDialogHandler) agentAnalysisMaskedFields(ctx context.Context, objectKey string, principal principalmodel.Principal) ([]string, error) {
	objectKey = strings.TrimSpace(objectKey)
	if objectKey == "" {
		return []string{}, nil
	}
	snapshot, err := h.analysisCatalog.FeaturePermissions(ctx, principal)
	if err != nil {
		return nil, err
	}
	fields := make([]string, 0)
	for _, field := range snapshot.Fields {
		if field.ObjectKey == objectKey && field.Masked {
			fields = append(fields, field.FieldKey)
		}
	}
	sort.Strings(fields)
	return fields, nil
}
