package http

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	capacityplatform "github.com/domainry/domainry-runtime/runtime/platform/capacity"
)

const productSurfaceHeader = "X-Domainry-Product-Surface"

func (s *HTTPRouter) withSurfaceRouteGroupPolicy(group SurfaceRouteGroup, next http.Handler) http.Handler {
	policy := s.surfaceGroupPolicies[group]
	controller := s.surfaceGroupCapacity[group]
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		if s.httpMetrics != nil {
			defer func() {
				s.httpMetrics.ObserveSurfaceGroup(string(group), recorder.status, surfaceMutationRequest(r))
			}()
		}
		w = recorder
		if origin := strings.TrimSpace(r.Header.Get("Origin")); origin != "" && len(policy.AllowedOrigins) > 0 && !corsOriginAllowed(policy.AllowedOrigins, origin) {
			writeError(w, r, http.StatusForbidden, "cors.origin_denied")
			return
		}
		if policy.MaxJSONBodyBytes > 0 && r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, policy.MaxJSONBodyBytes)
		}
		if s.rateLimiter != nil && policy.RateLimitPerMinute > 0 && !capacityProbePath(r.URL.Path) {
			decision, err := s.rateLimiter.Allow(r.Context(), "http_surface:"+string(group), policy.RateLimitPerMinute, time.Minute)
			if err != nil {
				w.Header().Set("Retry-After", "1")
				s.appendSecurityAudit(r, "surface_listener_rate_limit_unavailable", "Runtime listener rate-limit backend unavailable", map[string]any{
					"group": group, "audit_class": policy.AuditClass,
				})
				writeError(w, r, http.StatusServiceUnavailable, "capacity.surface_rate_limit_unavailable")
				return
			}
			if !decision.Allowed {
				retrySeconds := int64((decision.RetryAfter + time.Second - 1) / time.Second)
				if retrySeconds < 1 {
					retrySeconds = 1
				}
				w.Header().Set("Retry-After", strconv.FormatInt(retrySeconds, 10))
				s.appendSecurityAudit(r, "surface_listener_rate_limited", "Runtime listener rate limit denied", map[string]any{
					"group": group, "audit_class": policy.AuditClass,
				})
				writeError(w, r, http.StatusTooManyRequests, "capacity.surface_rate_limited")
				return
			}
		}
		if controller != nil && !capacityProbePath(r.URL.Path) {
			principal := s.principalFromRequest(r)
			workspaceID := strings.TrimSpace(principal.WorkspaceID)
			if workspaceID == "" {
				workspaceID = workspaceIDFromRequest(r)
			}
			lease, decision := controller.Acquire(r.Context(), capacityplatform.Request{
				WorkspaceID: workspaceID,
				UseCase:     string(group) + ":" + capacityUseCase(r, r.URL.Path),
				Essential:   capacityEssentialRequest(r.Method, r.URL.Path),
				Retry:       capacityRetryRequest(r.URL.Path),
			})
			if !decision.Allowed {
				retrySeconds := int64((decision.RetryAfter + time.Second - 1) / time.Second)
				if retrySeconds < 1 {
					retrySeconds = 1
				}
				w.Header().Set("Retry-After", strconv.FormatInt(retrySeconds, 10))
				s.appendSecurityAudit(r, "surface_listener_rate_limited", "Runtime listener rate limit denied", map[string]any{
					"group":       group,
					"audit_class": policy.AuditClass,
				})
				writeError(w, r, http.StatusTooManyRequests, "capacity.surface_rate_limited")
				return
			}
			defer lease.Release()
		}
		ctx := r.Context()
		if policy.RequestTimeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, policy.RequestTimeout)
			defer cancel()
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func corsOriginAllowed(allowedOrigins []string, origin string) bool {
	for _, allowed := range allowedOrigins {
		allowed = strings.TrimSpace(allowed)
		if allowed == "*" || strings.EqualFold(allowed, origin) {
			return true
		}
	}
	return false
}

func surfaceMutationRequest(r *http.Request) bool {
	if r == nil {
		return false
	}
	switch r.Method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}
