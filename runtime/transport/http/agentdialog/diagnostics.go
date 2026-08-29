package agentdialog

import (
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"context"
	"net/http"
	"strings"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
)

func (h *AgentDialogHandler) agentDialogDiagnostics(w http.ResponseWriter, r *http.Request) {
	principal := h.principal(r)
	values := r.URL.Query()
	runtimeInput := map[string]any{
		"workspace":       values.Get("workspace"),
		"module_key":      values.Get("module_key"),
		"surface":         values.Get("surface"),
		"view_key":        values.Get("view_key"),
		"object_key":      values.Get("object_key"),
		"record_id":       values.Get("record_id"),
		"data_scope_note": values.Get("data_scope_note"),
	}
	policyInput := map[string]any{
		"agent_mode": agentDiagnosticsValue(values.Get("agent_mode"), "domain-flow"),
		"run_mode":   agentDiagnosticsValue(values.Get("run_mode"), "suggested_write"),
		"risk_level": agentDiagnosticsValue(values.Get("risk_level"), "medium"),
	}
	events, eventError := h.agentDialogDiagnosticEvents(r.Context(), principal, values.Get("object_key"))
	h.writeJSON(w, http.StatusOK, map[string]any{
		"runtime_context_preview": agentDialogRuntimeContext(runtimeInput, principal),
		"agent_http": map[string]any{
			"configured": h.config.AgentID > 0 && strings.TrimSpace(h.config.APIKey) != "",
			"base_url":   h.config.BaseURL,
			"agent_id":   h.config.AgentID,
			"api_key":    "redacted",
			"timeout_ms": h.config.Timeout.Milliseconds(),
		},
		"skill_bindings": []map[string]any{
			{"family": "domain-flow", "skill": "agents/skills/domain-flow", "default_run_mode": "suggested_write"},
			{"family": "data-analysis", "skill": "agents/skills/data-analysis", "default_run_mode": "read_only"},
			{"family": "system-ops", "skill": "agents/skills/system-ops", "default_run_mode": "read_only"},
		},
		"effective_tools": []map[string]any{
			agentDialogDiagnosticTool("generated_api_read", "generated_api", "read_only", "low", false, ""),
			agentDialogDiagnosticTool("analysis_query_gateway", "analysis", "read_only", "medium", false, ""),
			agentDialogDiagnosticTool("mysql_readonly_query", "database", "read_only", "medium", false, "fallback_diagnosis_only"),
			agentDialogDiagnosticPolicyTool("create_proposal", "medium", false, policyInput),
			agentDialogDiagnosticPolicyTool("approve_proposal", "high", true, policyInput),
			agentDialogDiagnosticPolicyTool("reject_proposal", "medium", true, policyInput),
		},
		"proposal_queue":      h.agentDialogDiagnosticProposalQueue(r.Context(), principal, values.Get("object_key")),
		"execution_logs":      events,
		"execution_log_error": eventError,
		"guardrails": []string{
			"admin_only",
			"no_secret_material",
			"server_principal_scoped",
			"proposal_actions_require_suggested_write",
			"database_access_wrapper_only",
		},
	})
}

func agentDiagnosticsValue(value string, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}

func (h *AgentDialogHandler) agentDialogDiagnosticEvents(ctx context.Context, principal principalmodel.Principal, objectKey string) ([]auditmodel.AuditEvent, string) {
	events, err := h.diagnosticAudit.Events(ctx, auditmodel.AuditEventQuery{Limit: 50}, principal)
	if err != nil {
		return []auditmodel.AuditEvent{}, err.Error()
	}
	filtered := make([]auditmodel.AuditEvent, 0, len(events))
	objectKey = strings.TrimSpace(objectKey)
	for _, event := range events {
		if strings.HasPrefix(event.Event, "agent_dialog") || strings.HasPrefix(event.Event, "agent_analysis") {
			if objectKey != "" && event.ObjectKey != objectKey && strings.TrimSpace(agentPolicyStringFromAny(event.Metadata["object_key"])) != objectKey && !strings.HasPrefix(event.Event, "agent_dialog_proposal_") {
				continue
			}
			filtered = append(filtered, event)
		}
	}
	return filtered, ""
}

func agentDialogDiagnosticTool(key string, family string, runMode string, riskLevel string, approvalRequired bool, denialReason string) map[string]any {
	allowed := strings.TrimSpace(denialReason) == ""
	return map[string]any{
		"key":               key,
		"family":            family,
		"run_mode":          runMode,
		"risk_level":        riskLevel,
		"approval_required": approvalRequired,
		"allowed":           allowed,
		"denial_reason":     strings.TrimSpace(denialReason),
		"policy_source":     "agents/tool-risk-registry.json",
		"why":               agentDialogDiagnosticWhy(denialReason),
		"next_step":         agentDialogDiagnosticNextStep(key, denialReason),
	}
}

func agentDialogDiagnosticPolicyTool(key string, riskLevel string, approvalRequired bool, policyInput map[string]any) map[string]any {
	metadata := cloneStringAnyMap(policyInput)
	metadata["risk_level"] = riskLevel
	reason := agentDialogWritePolicyError(key, metadata)
	tool := agentDialogDiagnosticTool(key, "proposal", "suggested_write", riskLevel, approvalRequired, reason)
	tool["evaluated_agent_mode"] = agentPolicyStringFromAny(metadata["agent_mode"])
	tool["evaluated_run_mode"] = agentPolicyStringFromAny(metadata["run_mode"])
	tool["evaluated_risk_level"] = agentPolicyStringFromAny(metadata["risk_level"])
	tool["required_run_mode"] = "suggested_write"
	tool["policy_source"] = "agentDialogWritePolicyError"
	return tool
}

func agentDialogDiagnosticWhy(reason string) string {
	switch strings.TrimSpace(reason) {
	case "":
		return "tool is allowed for the evaluated runtime policy"
	case "agent_dialog.policy_read_only":
		return "current run mode is read_only, so proposal write actions are disabled"
	case "agent_dialog.policy_run_mode_denied":
		return "current run mode is not suggested_write"
	case "agent_dialog.policy_risk_denied":
		return "critical-risk proposal decisions require a stronger manual path"
	case "fallback_diagnosis_only":
		return "database access is only available as a fallback diagnostic path"
	default:
		return strings.TrimSpace(reason)
	}
}

func agentDialogDiagnosticNextStep(key string, reason string) string {
	switch strings.TrimSpace(reason) {
	case "":
		if key == "approve_proposal" || key == "reject_proposal" {
			return "select a draft proposal and make an explicit approval decision"
		}
		return "invoke the tool through the generated Agent Dialog action binder"
	case "agent_dialog.policy_read_only":
		return "switch this request to Business Flow / suggested_write before creating or deciding proposals"
	case "agent_dialog.policy_run_mode_denied":
		return "use suggested_write for proposal actions"
	case "agent_dialog.policy_risk_denied":
		return "lower the risk through a safer proposal or route the decision to manual admin review"
	case "fallback_diagnosis_only":
		return "prefer generated APIs and the analysis query gateway before database fallback"
	default:
		return "inspect runtime context, role permissions, and tool policy"
	}
}

func (h *AgentDialogHandler) agentDialogDiagnosticProposalQueue(ctx context.Context, principal principalmodel.Principal, objectKey string) []agentDialogProposalRecord {
	queue := []agentDialogProposalRecord{}
	objectKey = strings.TrimSpace(objectKey)
	proposals, err := h.proposalState.ListProposals(ctx, "", principal)
	if err != nil {
		return queue
	}
	for _, proposal := range proposals {
		if objectKey != "" && agentPolicyStringFromAny(proposal.Metadata["object_key"]) != objectKey {
			continue
		}
		queue = append(queue, proposal)
	}
	return queue
}
