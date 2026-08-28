package agentdialog

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/domainry/domainry-runtime/runtime/platform/ratelimit"
)

func (h *AgentDialogHandler) agentDialogRateLimited(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		allowed, retryAfter := h.agentDialogRateLimitDecision(r)
		if !allowed {
			w.Header().Set("Retry-After", retryAfterSeconds(retryAfter))
			h.writeError(w, r, http.StatusTooManyRequests, "agent_dialog.rate_limited")
			return
		}
		next(w, r)
	}
}

func (h *AgentDialogHandler) allowAgentDialogRequest(r *http.Request) bool {
	allowed, _ := h.agentDialogRateLimitDecision(r)
	return allowed
}

func (h *AgentDialogHandler) agentDialogRateLimitDecision(r *http.Request) (bool, time.Duration) {
	limit := h.config.RateLimitPerMinute
	if limit <= 0 {
		limit = 60
	}
	principal := h.principal(r)
	key := strings.Join([]string{
		workspaceIDFromRequest(r),
		principal.UserID,
		principal.RoleKey,
		strings.TrimSpace(r.Pattern),
	}, "|")
	if h.rateLimiter == nil {
		h.rateLimiter = ratelimit.NewMemoryLimiter(ratelimit.DefaultMemoryCapacity)
	}
	decision, err := h.rateLimiter.Allow(r.Context(), "agent:"+key, limit, time.Minute)
	if err != nil {
		return false, time.Second
	}
	if decision.Allowed {
		return true, 0
	}
	h.securityAuditForPrincipal(r, principal, "agent_dialog_rate_limited", "Agent dialog request rate limited", map[string]any{
		"path":                 r.URL.Path,
		"method":               r.Method,
		"limit_per_minute":     limit,
		"retry_after_seconds":  retryAfterSeconds(decision.RetryAfter),
		"rate_limit_scope":     "workspace_user_role_route",
		"rate_limited_request": strconv.Itoa(decision.Count),
	})
	return false, decision.RetryAfter
}

func retryAfterSeconds(value time.Duration) string {
	seconds := int64((value + time.Second - 1) / time.Second)
	if seconds < 1 {
		seconds = 1
	}
	return strconv.FormatInt(seconds, 10)
}
