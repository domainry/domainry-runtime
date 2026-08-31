package agentdialog

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	agentruntime "github.com/domainry/domainry-runtime/runtime/application/agent/runtime"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type agentDialogRunRequest struct {
	Message           string         `json:"message"`
	ResponseMode      string         `json:"response_mode,omitempty"`
	TimeoutSeconds    int            `json:"timeout_seconds,omitempty"`
	NewSession        bool           `json:"new_session,omitempty"`
	ExternalSessionID string         `json:"external_session_id,omitempty"`
	Context           map[string]any `json:"context,omitempty"`
	Metadata          map[string]any `json:"metadata,omitempty"`
	IdempotencyKey    string         `json:"idempotency_key,omitempty"`
}

func (h *AgentDialogHandler) agentDialogRun(w http.ResponseWriter, r *http.Request) {
	var payload agentDialogRunRequest
	if !h.decodeJSON(w, r, &payload) {
		return
	}
	if strings.TrimSpace(payload.ResponseMode) == "" {
		payload.ResponseMode = "blocking"
	}
	payload.ResponseMode = strings.TrimSpace(payload.ResponseMode)
	if h.interactive == nil || h.contextResolver == nil {
		h.writeError(w, r, http.StatusServiceUnavailable, "agent.interactive.unavailable")
		return
	}
	if payload.ResponseMode != "blocking" {
		h.writeError(w, r, http.StatusBadRequest, "agent.interactive.response_mode_unsupported")
		return
	}
	h.runTypedInteractiveAgent(w, r, payload)
}

func (h *AgentDialogHandler) agentDialogRunStream(w http.ResponseWriter, r *http.Request) {
	var payload agentDialogRunRequest
	if !h.decodeJSON(w, r, &payload) {
		return
	}
	if h.interactive == nil || h.contextResolver == nil {
		h.writeError(w, r, http.StatusServiceUnavailable, "agent.interactive.unavailable")
		return
	}
	h.runTypedInteractiveAgentStream(w, r, payload)
}

func (h *AgentDialogHandler) agentDialogRunStatus(w http.ResponseWriter, r *http.Request) {
	runID := strings.TrimSpace(r.PathValue("runID"))
	if runID == "" {
		h.writeError(w, r, http.StatusBadRequest, "agent_dialog.run_id_required")
		return
	}
	if h.interactiveRuns == nil {
		h.writeError(w, r, http.StatusServiceUnavailable, "agent.interactive.unavailable")
		return
	}
	run, found, err := h.interactiveRuns.Get(r.Context(), runID, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if !found {
		h.writeError(w, r, http.StatusNotFound, "agent.interactive.not_found")
		return
	}
	h.writeJSON(w, http.StatusOK, run)
}

func agentApplicationGlobalContextRequest(r *http.Request, hints map[string]any, principal principalmodel.Principal) agentruntime.GlobalAgentContextRequest {
	return agentruntime.GlobalAgentContextRequest{
		Principal:     principal,
		EntrypointKey: valueOrDefault(r.Header.Get("X-Agent-Entrypoint-Key"), agentDialogSafeString(hints["entrypoint_key"])),
		Surface:       valueOrDefault(r.Header.Get("X-Surface-Key"), agentDialogSafeString(hints["surface"])),
		RouteKey:      valueOrDefault(r.Header.Get("X-Route-Key"), agentDialogSafeString(hints["route_key"])),
		ObjectKey:     agentDialogSafeString(hints["object_key"]), RecordID: agentDialogSafeString(hints["record_id"]),
		SelectedRecordIDs:     agentDialogContextStringList(hints, "selected_record_ids", "selected_records"),
		Locale:                valueOrDefault(r.Header.Get("X-Locale"), agentDialogSafeString(hints["locale"])),
		Timezone:              valueOrDefault(r.Header.Get("X-Timezone"), agentDialogSafeString(hints["timezone"])),
		AvailableOperationIDs: agentDialogContextStringList(hints, "available_operation_ids", "available_operations"),
	}
}

func agentDialogContextStringList(hints map[string]any, keys ...string) []string {
	for _, key := range keys {
		if values := agentDialogSafeStringList(hints[key], 100); len(values) > 0 {
			return values
		}
	}
	return nil
}

func (h *AgentDialogHandler) agentDialogExternalSessionID(r *http.Request, requested string, userID string) string {
	if value := strings.TrimSpace(requested); value != "" {
		return value
	}
	workspaceID := workspaceIDFromRequest(r)
	if userID == "" {
		userID = "anonymous"
	}
	surface := strings.TrimSpace(r.Header.Get("X-Surface-Key"))
	if surface == "" {
		surface = "workspace"
	}
	return "workspace:" + agentDialogSessionPart(workspaceID) + ":user:" + agentDialogSessionPart(userID) + ":dialog:" + agentDialogSessionPart(surface)
}

func agentDialogSessionPart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unspecified"
	}
	var builder strings.Builder
	for _, ch := range value {
		if ch >= 'a' && ch <= 'z' || ch >= 'A' && ch <= 'Z' || ch >= '0' && ch <= '9' || ch == '_' || ch == '-' || ch == '.' {
			builder.WriteRune(ch)
		}
	}
	if builder.Len() == 0 {
		return strconv.FormatInt(int64(len(value)), 10)
	}
	return builder.String()
}

func timeNowUnixNano() int64 {
	return time.Now().UnixNano()
}
