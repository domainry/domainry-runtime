package agentdialog

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	agentruntime "github.com/domainry/domainry-runtime/runtime/application/agent/runtime"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
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
	if h.interactive != nil {
		if h.contextResolver != nil {
			if payload.ResponseMode == "blocking" {
				h.runTypedInteractiveAgent(w, r, payload)
				return
			}
		}
	}
	h.proxyAgentDialogJSON(w, r, "/api/v1/agent-runs", payload)
}

func (h *AgentDialogHandler) agentDialogRunStream(w http.ResponseWriter, r *http.Request) {
	// Streaming has its own lifecycle: the request/upstream contexts govern it,
	// while the ordinary server WriteTimeout must not truncate an active SSE run.
	_ = http.NewResponseController(w).SetWriteDeadline(time.Time{})
	var payload agentDialogRunRequest
	if !h.decodeJSON(w, r, &payload) {
		return
	}
	if h.interactive != nil {
		if h.contextResolver != nil {
			h.runTypedInteractiveAgentStream(w, r, payload)
			return
		}
	}
	upstreamPayload, resolveErr := h.agentDialogResolvedUpstreamPayload(r, payload)
	if resolveErr != nil {
		h.writeServiceError(w, r, resolveErr)
		return
	}
	body, err := json.Marshal(upstreamPayload)
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "backend.invalid_json")
		return
	}
	upstreamReq, ok := h.newAgentDialogUpstreamRequest(w, r, "/api/v1/agent-runs/stream", body)
	if !ok {
		return
	}
	resp, err := http.DefaultClient.Do(upstreamReq)
	if err != nil {
		h.securityAudit(r, "agent_dialog_stream_failed", "Agent dialog stream request failed", map[string]any{"error_code": apperror.CodeOf(err)})
		h.writeError(w, r, http.StatusBadGateway, "agent_dialog.upstream_failed")
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode/100 != 2 {
		h.writeAgentDialogUpstreamError(w, r, resp)
		return
	}
	for key, values := range resp.Header {
		if !agentDialogRelayHeaderAllowed(key) {
			continue
		}
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	_, _ = io.Copy(agentDialogFlushWriter{w: w}, resp.Body)
}

func (h *AgentDialogHandler) agentDialogRunStatus(w http.ResponseWriter, r *http.Request) {
	runID := strings.TrimSpace(r.PathValue("runID"))
	if runID == "" {
		h.writeError(w, r, http.StatusBadRequest, "agent_dialog.run_id_required")
		return
	}
	if h.interactiveRuns != nil {
		if strings.HasPrefix(runID, "interactive_run_") {
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
			return
		}
	}
	upstreamReq, ok := h.newAgentDialogUpstreamRequest(w, r, "/api/v1/agent-runs/"+agentDialogPathEscape(runID), nil)
	if !ok {
		return
	}
	resp, err := http.DefaultClient.Do(upstreamReq)
	if err != nil {
		h.securityAudit(r, "agent_dialog_poll_failed", "Agent dialog poll request failed", map[string]any{"run_id": runID, "error_code": apperror.CodeOf(err)})
		h.writeError(w, r, http.StatusBadGateway, "agent_dialog.upstream_failed")
		return
	}
	defer resp.Body.Close()
	relayAgentDialogJSONResponse(w, r, resp)
}

func (h *AgentDialogHandler) proxyAgentDialogJSON(w http.ResponseWriter, r *http.Request, path string, payload agentDialogRunRequest) {
	upstreamPayload, resolveErr := h.agentDialogResolvedUpstreamPayload(r, payload)
	if resolveErr != nil {
		h.writeServiceError(w, r, resolveErr)
		return
	}
	body, err := json.Marshal(upstreamPayload)
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "backend.invalid_json")
		return
	}
	upstreamReq, ok := h.newAgentDialogUpstreamRequest(w, r, path, body)
	if !ok {
		return
	}
	resp, err := http.DefaultClient.Do(upstreamReq)
	if err != nil {
		h.securityAudit(r, "agent_dialog_run_failed", "Agent dialog run request failed", map[string]any{"error_code": apperror.CodeOf(err)})
		h.writeError(w, r, http.StatusBadGateway, "agent_dialog.upstream_failed")
		return
	}
	defer resp.Body.Close()
	relayAgentDialogJSONResponse(w, r, resp)
}

func (h *AgentDialogHandler) agentDialogUpstreamPayload(r *http.Request, payload agentDialogRunRequest) map[string]any {
	out, _ := h.agentDialogResolvedUpstreamPayload(r, payload)
	return out
}

func (h *AgentDialogHandler) agentDialogResolvedUpstreamPayload(r *http.Request, payload agentDialogRunRequest) (map[string]any, error) {
	principal := h.principal(r)
	metadata := cloneStringAnyMap(payload.Metadata)
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["workspace_id"] = principal.WorkspaceID
	metadata["user_id"] = principal.UserID
	agentDialogAttachExecutionIdentity(metadata, principal.UserID)
	metadata["role"] = principal.RoleKey
	metadata["source"] = "domainry-generated-app"
	metadata["output"] = "html_fragments"
	metadata["request_id"] = principal.RequestID
	runtimeContext := agentDialogRuntimeContext(payload.Context, principal)
	if h.contextResolver != nil {
		trusted, err := h.contextResolver.ResolveGlobalContext(r.Context(), agentApplicationGlobalContextRequest(r, payload.Context, principal))
		if err != nil {
			return nil, err
		}
		runtimeContext = agentDialogTrustedRuntimeContext(trusted)
	}
	metadata["runtime_context"] = runtimeContext

	out := map[string]any{
		"agent_id":            h.config.AgentID,
		"message":             agentDialogMessageWithRuntimeContext(payload.Message, runtimeContext),
		"new_session":         payload.NewSession,
		"external_session_id": h.agentDialogExternalSessionID(r, payload.ExternalSessionID, principal.UserID),
		"metadata":            metadata,
	}
	if mode := strings.TrimSpace(payload.ResponseMode); mode != "" {
		out["response_mode"] = mode
	}
	if payload.TimeoutSeconds > 0 {
		out["timeout_seconds"] = payload.TimeoutSeconds
	}
	return out, nil
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

func (h *AgentDialogHandler) newAgentDialogUpstreamRequest(w http.ResponseWriter, r *http.Request, path string, body []byte) (*http.Request, bool) {
	if strings.TrimSpace(h.config.APIKey) == "" || h.config.AgentID <= 0 {
		h.writeError(w, r, http.StatusServiceUnavailable, "agent_dialog.not_configured")
		return nil, false
	}
	ctx := r.Context()
	var cancel context.CancelFunc
	if h.config.Timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, h.config.Timeout)
	}
	if cancel != nil {
		go func() {
			<-ctx.Done()
			cancel()
		}()
	}
	var reader io.Reader
	method := http.MethodGet
	if body != nil {
		reader = bytes.NewReader(body)
		method = http.MethodPost
	}
	req, err := http.NewRequestWithContext(ctx, method, h.config.BaseURL+path, reader)
	if err != nil {
		h.writeError(w, r, http.StatusBadGateway, "agent_dialog.invalid_upstream")
		return nil, false
	}
	req.Header.Set("Authorization", "Bearer "+h.config.APIKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Accept", "text/event-stream, application/json")
	return req, true
}

func relayAgentDialogJSONResponse(w http.ResponseWriter, r *http.Request, resp *http.Response) {
	contentType := resp.Header.Get("Content-Type")
	if contentType == "" {
		contentType = "application/json"
	}
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func (h *AgentDialogHandler) writeAgentDialogUpstreamError(w http.ResponseWriter, r *http.Request, resp *http.Response) {
	payload, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if len(payload) > 0 && strings.Contains(resp.Header.Get("Content-Type"), "application/json") {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(resp.StatusCode)
		_, _ = w.Write(payload)
		return
	}
	h.writeError(w, r, http.StatusBadGateway, "agent_dialog.upstream_failed")
}

func agentDialogRelayHeaderAllowed(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "content-type", "cache-control":
		return true
	default:
		return false
	}
}

func agentDialogPathEscape(value string) string {
	return url.PathEscape(strings.TrimSpace(value))
}

type agentDialogFlushWriter struct {
	w http.ResponseWriter
}

func (writer agentDialogFlushWriter) Write(payload []byte) (int, error) {
	n, err := writer.w.Write(payload)
	_ = http.NewResponseController(writer.w).Flush()
	return n, err
}

func agentDialogSessionPart(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "default"
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
