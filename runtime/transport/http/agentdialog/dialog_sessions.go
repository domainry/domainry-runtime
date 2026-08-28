package agentdialog

import (
	"net/http"
	"strconv"
	"strings"

	agent "github.com/domainry/domainry-runtime/runtime/application/agent"
)

func (h *AgentDialogHandler) agentDialogListSessions(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	result, err := h.sessions.ListSessions(r.Context(), agent.AgentSessionQuery{Search: query.Get("q"), Surface: strings.TrimSpace(query.Get("surface")), ObjectKey: strings.TrimSpace(query.Get("object_key")), RecordID: strings.TrimSpace(query.Get("record_id")), IncludeArchived: query.Get("archived") == "true", Limit: agentDialogSessionLimit(query.Get("limit"))}, h.principal(r))
	if err != nil {
		h.writeError(w, r, http.StatusInternalServerError, "agent_dialog.state_repository_failed")
		return
	}
	h.writeJSON(w, http.StatusOK, map[string]any{"sessions": result})
}

func (h *AgentDialogHandler) agentDialogUpsertSession(w http.ResponseWriter, r *http.Request) {
	var request agent.AgentSessionUpsertRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	result, err := h.sessions.UpsertSession(r.Context(), request, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *AgentDialogHandler) agentDialogArchiveSession(w http.ResponseWriter, r *http.Request) {
	h.agentDialogSetSessionArchived(w, r, true)
}
func (h *AgentDialogHandler) agentDialogRestoreSession(w http.ResponseWriter, r *http.Request) {
	h.agentDialogSetSessionArchived(w, r, false)
}

func (h *AgentDialogHandler) agentDialogSetSessionArchived(w http.ResponseWriter, r *http.Request, archived bool) {
	result, err := h.sessions.SetSessionArchived(r.Context(), r.PathValue("externalSessionID"), archived, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func agentDialogSessionLimit(raw string) int {
	limit, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || limit <= 0 {
		return 20
	}
	if limit > 100 {
		return 100
	}
	return limit
}
