package integrations

import (
	"fmt"
	"net/http"
	"strings"

	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

func (h *IntegrationsHandler) startGoogleOAuth(w http.ResponseWriter, r *http.Request) {
	var request integrationmodel.IntegrationGoogleOAuthStartRequest
	if !h.decodeJSON(w, r, &request) {
		return
	}
	result, err := h.connections.StartGoogleOAuthAuthorization(r.Context(), strings.TrimSpace(r.PathValue("connectionKey")), request, h.principal(r))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}

func (h *IntegrationsHandler) completeGoogleOAuth(w http.ResponseWriter, r *http.Request) {
	if providerError := strings.TrimSpace(r.URL.Query().Get("error")); providerError != "" {
		h.writeError(w, r, http.StatusBadRequest, "backend.integration.google_oauth.provider_rejected", "provider_error", providerError)
		return
	}
	result, err := h.connections.CompleteGoogleOAuthAuthorization(r.Context(), strings.TrimSpace(r.URL.Query().Get("code")), strings.TrimSpace(r.URL.Query().Get("state")))
	if err != nil {
		h.writeServiceError(w, r, err)
		return
	}
	if strings.Contains(strings.ToLower(r.Header.Get("Accept")), "text/html") {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprint(w, `<!doctype html><html lang="zh-CN"><meta charset="utf-8"><title>Google 授权完成</title><style>body{font:16px system-ui;display:grid;place-items:center;min-height:80vh;color:#173f37}main{text-align:center}p{color:#667}</style><main><h1>Google 授权完成</h1><p>Gmail 定时轮询已启用。此窗口会自动关闭。</p></main><script>setTimeout(()=>window.close(),900)</script></html>`)
		return
	}
	h.writeJSON(w, http.StatusOK, result)
}
