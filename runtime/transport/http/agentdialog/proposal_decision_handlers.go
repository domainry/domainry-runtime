package agentdialog

import (
	"net/http"
	"strconv"
	"strings"
)

type agentDialogProposalDecisionRequest struct {
	Reason   string         `json:"reason,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

type agentDialogCreateProposalRequest struct {
	Title     string         `json:"title,omitempty"`
	Summary   string         `json:"summary,omitempty"`
	Source    string         `json:"source,omitempty"`
	Reference string         `json:"reference,omitempty"`
	Proposed  map[string]any `json:"proposed,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

func (h *AgentDialogHandler) agentDialogCreateProposal(w http.ResponseWriter, r *http.Request) {
	var payload agentDialogCreateProposalRequest
	if !h.decodeJSON(w, r, &payload) {
		return
	}
	principal := h.principal(r)
	proposalID := "agent_proposal_" + strconv.FormatInt(timeNowUnixNano(), 36)
	metadata := cloneStringAnyMap(payload.Metadata)
	if metadata == nil {
		metadata = map[string]any{}
	}
	if !h.enforceAgentDialogWritePolicy(w, r, "create_proposal", metadata) {
		return
	}
	proposed := h.proposalDecisions.NormalizeGuardedWrite(r.Context(), cloneStringAnyMap(payload.Proposed), principal)
	if binding := mapFromAny(proposed["action_binding"]); len(binding) > 0 && agentDialogTruthy(binding["guarded_write"]) {
		metadata["guarded_write_action_key"] = strings.TrimSpace(agentPolicyStringFromAny(binding["action_key"]))
		metadata["guarded_write_operation"] = strings.TrimSpace(agentPolicyStringFromAny(binding["operation"]))
		metadata["guarded_write_rewrite"] = true
	}
	metadata["proposal_id"], metadata["title"] = proposalID, strings.TrimSpace(payload.Title)
	metadata["source"], metadata["reference"] = strings.TrimSpace(payload.Source), strings.TrimSpace(payload.Reference)
	agentDialogAttachExecutionIdentity(metadata, principal.UserID)
	h.securityAuditForPrincipal(r, principal, "agent_dialog_proposal_created", "Agent dialog proposal created", metadata)
	proposal, err := h.agentDialogStoreProposal(r.Context(), agentDialogProposalRecord{ProposalID: proposalID, Status: "draft", Title: strings.TrimSpace(payload.Title), Summary: strings.TrimSpace(payload.Summary), Source: strings.TrimSpace(payload.Source), Reference: strings.TrimSpace(payload.Reference), Actor: principal.UserID, WorkspaceID: principal.WorkspaceID, UserID: principal.UserID, Role: principal.RoleKey, Proposed: proposed, Metadata: cloneStringAnyMap(metadata)})
	if err != nil {
		h.writeError(w, r, http.StatusInternalServerError, "agent_dialog.state_repository_failed")
		return
	}
	h.writeJSON(w, http.StatusCreated, proposal)
}

func (h *AgentDialogHandler) agentDialogApproveProposal(w http.ResponseWriter, r *http.Request) {
	h.agentDialogProposalDecision(w, r, "approved")
}

func (h *AgentDialogHandler) agentDialogRejectProposal(w http.ResponseWriter, r *http.Request) {
	h.agentDialogProposalDecision(w, r, "rejected")
}

func (h *AgentDialogHandler) agentDialogProposalDecision(w http.ResponseWriter, r *http.Request, decision string) {
	proposalID := strings.TrimSpace(r.PathValue("proposalID"))
	if proposalID == "" {
		h.writeError(w, r, http.StatusBadRequest, "agent_dialog.proposal_id_required")
		return
	}
	var payload agentDialogProposalDecisionRequest
	if r.Body != nil && r.ContentLength != 0 && !h.decodeJSON(w, r, &payload) {
		return
	}
	principal := h.principal(r)
	metadata := cloneStringAnyMap(payload.Metadata)
	if metadata == nil {
		metadata = map[string]any{}
	}
	action := "reject_proposal"
	if decision == "approved" {
		action = "approve_proposal"
	}
	if !h.enforceAgentDialogWritePolicy(w, r, action, metadata) {
		return
	}
	metadata["proposal_id"], metadata["decision"], metadata["reason"] = proposalID, decision, strings.TrimSpace(payload.Reason)
	agentDialogAttachExecutionIdentity(metadata, principal.UserID)
	proposal, err := h.proposalDecisions.Decide(r.Context(), proposalID, decision, payload.Reason, metadata, principal)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if decision == "approved" {
		metadata["execution_status"] = agentPolicyStringFromAny(proposal.Execution["status"])
		metadata["execution_kind"] = agentPolicyStringFromAny(proposal.Execution["kind"])
	}
	h.securityAuditForPrincipal(r, principal, "agent_dialog_proposal_"+decision, "Agent dialog proposal "+decision, metadata)
	h.writeJSON(w, http.StatusOK, proposal)
}
