package agentdialog

import (
	"context"
	"net/http"

	agent "github.com/domainry/domainry-runtime/runtime/application/agent"
)

type agentDialogProposalRecord = agent.AgentProposal

func (h *AgentDialogHandler) agentDialogListProposals(w http.ResponseWriter, r *http.Request) {
	result, err := h.proposalState.ListProposals(r.Context(), r.URL.Query().Get("status"), h.principal(r))
	if err != nil {
		h.writeError(w, r, http.StatusInternalServerError, "agent_dialog.state_repository_failed")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"proposals": result})
}

func (h *AgentDialogHandler) agentDialogGetProposal(w http.ResponseWriter, r *http.Request) {
	result, err := h.proposalState.GetProposal(r.Context(), r.PathValue("proposalID"), h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *AgentDialogHandler) agentDialogStoreProposal(ctx context.Context, record agentDialogProposalRecord) (agentDialogProposalRecord, error) {
	return h.proposalState.StoreProposal(ctx, record)
}
