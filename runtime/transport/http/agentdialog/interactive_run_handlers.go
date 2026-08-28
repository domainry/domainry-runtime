package agentdialog

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	agentruntime "github.com/domainry/domainry-runtime/runtime/application/agent/runtime"
)

func (h *AgentDialogHandler) runTypedInteractiveAgent(w http.ResponseWriter, r *http.Request, payload agentDialogRunRequest) {
	principal := h.principal(r)
	trusted, err := h.contextResolver.ResolveGlobalContext(r.Context(), agentApplicationGlobalContextRequest(r, payload.Context, principal))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	idempotencyKey := valueOrDefault(r.Header.Get("Idempotency-Key"), payload.IdempotencyKey)
	if idempotencyKey == "" {
		h.writeError(w, r, http.StatusBadRequest, "agent.interactive.idempotency_required")
		return
	}
	result, err := h.interactive.Execute(r.Context(), agentruntime.AgentInteractiveExecutionRequest{
		SessionID: h.agentDialogExternalSessionID(r, payload.ExternalSessionID, principal.UserID), IdempotencyKey: idempotencyKey,
		Message: payload.Message, Context: trusted, Principal: principal,
	})
	if err != nil {
		if h.securityAuditForPrincipal != nil {
			h.securityAuditForPrincipal(r, principal, "agent_interactive_run_failed", "Interactive Agent run failed", map[string]any{"error_code": apperror.CodeOf(err), "entrypoint_key": trusted.EntrypointKey, "context_revision": trusted.ContextRevision})
		}
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *AgentDialogHandler) runTypedInteractiveAgentStream(w http.ResponseWriter, r *http.Request, payload agentDialogRunRequest) {
	principal := h.principal(r)
	trusted, err := h.contextResolver.ResolveGlobalContext(r.Context(), agentApplicationGlobalContextRequest(r, payload.Context, principal))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	idempotencyKey := valueOrDefault(r.Header.Get("Idempotency-Key"), payload.IdempotencyKey)
	if idempotencyKey == "" {
		h.writeError(w, r, http.StatusBadRequest, "agent.interactive.idempotency_required")
		return
	}
	acceptedEventID := agentInteractiveStreamEventID(principal.WorkspaceID, principal.UserID, idempotencyKey, "accepted")
	lastEventID := strings.TrimSpace(r.Header.Get("Last-Event-ID"))
	if lastEventID != "" && lastEventID != acceptedEventID {
		h.writeError(w, r, http.StatusConflict, "agent.interactive.stream_cursor_invalid")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	if lastEventID == "" {
		_, _ = w.Write([]byte("id: " + acceptedEventID + "\nevent: accepted\ndata: {\"status\":\"running\"}\n\n"))
		flushAgentDialogEvent(w)
	}
	result, runErr := h.interactive.Execute(r.Context(), agentruntime.AgentInteractiveExecutionRequest{
		SessionID: h.agentDialogExternalSessionID(r, payload.ExternalSessionID, principal.UserID), IdempotencyKey: idempotencyKey,
		Message: payload.Message, Context: trusted, Principal: principal,
	})
	event, body := "result", any(result)
	if runErr != nil {
		event, body = "error", map[string]any{"code": apperror.CodeOf(runErr)}
		if h.securityAuditForPrincipal != nil {
			h.securityAuditForPrincipal(r, principal, "agent_interactive_stream_failed", "Interactive Agent stream failed", map[string]any{"error_code": apperror.CodeOf(runErr), "entrypoint_key": trusted.EntrypointKey, "context_revision": trusted.ContextRevision})
		}
	}
	encoded, marshalErr := json.Marshal(body)
	if marshalErr != nil {
		encoded = []byte(`{"code":"agent.interactive.response_invalid"}`)
		event = "error"
	}
	eventID := agentInteractiveStreamEventID(principal.WorkspaceID, principal.UserID, idempotencyKey, event)
	_, _ = w.Write([]byte("id: " + eventID + "\nevent: " + event + "\ndata: " + string(encoded) + "\n\n"))
	flushAgentDialogEvent(w)
}

func agentInteractiveStreamEventID(workspaceID, userID, idempotencyKey, event string) string {
	digest := sha256.Sum256([]byte(strings.TrimSpace(workspaceID) + "\x00" + strings.TrimSpace(userID) + "\x00" + strings.TrimSpace(idempotencyKey)))
	sequence := "1"
	if event != "accepted" {
		sequence = "2"
	}
	return "agent_" + hex.EncodeToString(digest[:16]) + ":" + sequence
}

func flushAgentDialogEvent(w http.ResponseWriter) {
	if flusher, ok := w.(http.Flusher); ok {
		flusher.Flush()
	}
}
