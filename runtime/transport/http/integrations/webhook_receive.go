package integrations

import (
	"io"
	"net/http"
	"strings"
)

const WebhookBodyLimit = 1 << 20

func (h *IntegrationsHandler) receiveIntegrationWebhook(w http.ResponseWriter, r *http.Request) {
	limited := io.LimitReader(r.Body, WebhookBodyLimit+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		h.writeError(w, r, http.StatusBadRequest, "backend.integration.webhook.body_invalid")
		return
	}
	if len(body) > WebhookBodyLimit {
		h.writeError(w, r, http.StatusRequestEntityTooLarge, "backend.integration.webhook.body_too_large")
		return
	}
	headers := cloneHTTPValues(r.Header)
	query := cloneHTTPValues(r.URL.Query())
	result, err := h.webhooks.ReceiveIntegrationWebhookValuesForWorkspace(r.Context(), strings.TrimSpace(r.PathValue("workspaceID")), strings.TrimSpace(r.PathValue("connectionKey")), headers, query, body)
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if result.Challenge != "" {
		if result.ChallengeFormat == "text/plain" {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(result.Challenge))
			return
		}
		h.writeJSON(w, http.StatusOK, map[string]any{"challenge": result.Challenge})
		return
	}
	status := http.StatusCreated
	if result.Duplicate {
		status = http.StatusOK
	}
	h.writeJSON(w, status, result)
}

func cloneHTTPValues(source map[string][]string) map[string][]string {
	valuesByKey := make(map[string][]string, len(source))
	for key, values := range source {
		valuesByKey[key] = append([]string(nil), values...)
	}
	return valuesByKey
}
