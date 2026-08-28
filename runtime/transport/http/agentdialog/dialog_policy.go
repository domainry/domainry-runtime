package agentdialog

import (
	"net/http"
	"strings"
)

type agentDialogPolicy struct {
	AgentMode string
	RunMode   string
	RiskLevel string
}

func agentDialogPolicyFromMetadata(metadata map[string]any) agentDialogPolicy {
	policy := agentDialogPolicy{
		AgentMode: agentPolicyStringFromAny(metadata["agent_mode"]),
		RunMode:   agentPolicyStringFromAny(metadata["run_mode"]),
		RiskLevel: agentPolicyStringFromAny(metadata["risk_level"]),
	}
	if policy.RunMode == "" {
		policy.RunMode = agentDialogDefaultRunMode(policy.AgentMode)
	}
	if policy.RiskLevel == "" {
		policy.RiskLevel = "medium"
	}
	return policy
}

func agentDialogDefaultRunMode(agentMode string) string {
	switch strings.TrimSpace(agentMode) {
	case "domain-flow":
		return "suggested_write"
	case "data-analysis", "system-ops":
		return "read_only"
	default:
		return "read_only"
	}
}

func agentDialogWritePolicyError(action string, metadata map[string]any) string {
	policy := agentDialogPolicyFromMetadata(metadata)
	switch strings.TrimSpace(action) {
	case "create_proposal":
		if policy.RunMode == "read_only" {
			return "agent_dialog.policy_read_only"
		}
		if policy.RunMode != "suggested_write" {
			return "agent_dialog.policy_run_mode_denied"
		}
		return ""
	case "approve_proposal", "reject_proposal":
		if policy.RunMode == "read_only" {
			return "agent_dialog.policy_read_only"
		}
		if policy.RunMode != "suggested_write" {
			return "agent_dialog.policy_run_mode_denied"
		}
		if policy.RiskLevel == "critical" {
			return "agent_dialog.policy_risk_denied"
		}
		return ""
	default:
		return "agent_dialog.policy_unknown_action"
	}
}

func (h *AgentDialogHandler) enforceAgentDialogWritePolicy(w http.ResponseWriter, r *http.Request, action string, metadata map[string]any) bool {
	if reason := agentDialogWritePolicyError(action, metadata); reason != "" {
		h.securityAudit(r, "agent_dialog_policy_denied", "Agent dialog action denied by run mode policy", map[string]any{
			"action":     action,
			"reason":     reason,
			"agent_mode": agentPolicyStringFromAny(metadata["agent_mode"]),
			"run_mode":   agentPolicyStringFromAny(metadata["run_mode"]),
			"risk_level": agentPolicyStringFromAny(metadata["risk_level"]),
		})
		h.writeError(w, r, http.StatusForbidden, reason)
		return false
	}
	return true
}

func agentPolicyStringFromAny(value any) string {
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return ""
}

func agentDialogAttachExecutionIdentity(metadata map[string]any, requestingUser string) {
	if metadata == nil {
		return
	}
	if strings.TrimSpace(requestingUser) == "" {
		requestingUser = "anonymous"
	}
	metadata["requesting_user"] = requestingUser
	metadata["service_role"] = "agent_service_user"
	metadata["automation_user"] = "agent_automation"
}
