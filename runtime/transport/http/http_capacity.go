package http

import (
	"context"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	capacityplatform "github.com/domainry/domainry-foundation/capacity"
)

func (s *HTTPRouter) withAdmission(routes *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		policy := routePolicyFor(routes, r)
		if policy.path == "/business-events/stream" {
			next.ServeHTTP(w, r)
			return
		}
		if s.runtimeReleaseAdmission != nil && !runtimeReleaseAdmissionProbePath(policy.path) {
			if err := s.runtimeReleaseAdmission(); err != nil {
				w.Header().Set("Retry-After", "5")
				writeError(w, r, http.StatusServiceUnavailable, "runtime.release_cohort_unavailable")
				return
			}
		}
		if s.capacityController == nil || capacityProbePath(policy.path) {
			next.ServeHTTP(w, r)
			return
		}
		principal := s.principalFromRequest(r)
		workspaceID := strings.TrimSpace(principal.WorkspaceID)
		if workspaceID == "" {
			workspaceID = workspaceIDFromRequest(r)
		}
		request := capacityplatform.Request{WorkspaceID: workspaceID, UseCase: capacityUseCase(r, policy.path), Retry: capacityRetryRequest(policy.path), Essential: capacityEssentialRequest(r.Method, policy.path)}
		if s.backpressure != nil && s.backpressure(r.Context()) && !request.Essential {
			w.Header().Set("Retry-After", "5")
			w.Header().Set("X-Capacity-State", string(capacityplatform.Degraded))
			w.Header().Set("X-Capacity-Dimension", "queue")
			writeError(w, r, http.StatusServiceUnavailable, "capacity.queue_backpressure", "dimension", "queue")
			return
		}
		lease, decision := s.capacityController.Acquire(r.Context(), request)
		if !decision.Allowed {
			retrySeconds := int64((decision.RetryAfter + time.Second - 1) / time.Second)
			if retrySeconds < 1 {
				retrySeconds = 1
			}
			w.Header().Set("Retry-After", strconv.FormatInt(retrySeconds, 10))
			w.Header().Set("X-Capacity-State", string(decision.State))
			w.Header().Set("X-Capacity-Dimension", string(decision.Dimension))
			status := http.StatusTooManyRequests
			if decision.Dimension == capacityplatform.DimensionProcess {
				status = http.StatusServiceUnavailable
			}
			writeError(w, r, status, decision.Code, "dimension", string(decision.Dimension), "current", strconv.Itoa(decision.Current), "limit", strconv.Itoa(decision.Limit))
			return
		}
		defer lease.Release()
		w.Header().Set("X-Capacity-State", string(decision.State))
		ctx := r.Context()
		if s.requestTimeout > 0 {
			var cancel context.CancelFunc
			ctx, cancel = context.WithTimeout(ctx, s.requestTimeout)
			defer cancel()
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func runtimeReleaseAdmissionProbePath(path string) bool {
	switch strings.TrimSpace(path) {
	case "/live", "/ready", "/startup", "/health", "/metrics":
		return true
	}
	return false
}

func capacityProbePath(path string) bool {
	switch strings.TrimSpace(path) {
	case "/live", "/ready", "/startup", "/metrics":
		return true
	}
	return false
}
func capacityRetryRequest(routePath string) bool {
	path := strings.ToLower(routePath)
	return strings.Contains(path, "/retry") || strings.Contains(path, "/replay")
}
func capacityEssentialRequest(method, routePath string) bool {
	path := strings.ToLower(routePath)
	for _, marker := range []string{"/report", "/export", "/preview", "/search", "/import"} {
		if strings.Contains(path, marker) {
			return false
		}
	}
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	}
	return false
}
func capacityUseCase(r *http.Request, routePath string) string {
	segment := strings.Trim(strings.Split(strings.Trim(routePath, "/"), "/")[0], " ")
	if segment == "" {
		segment = "root"
	}
	return strings.ToLower(r.Method) + ":" + segment
}

func capacityOpenMetrics(snapshot capacityplatform.Snapshot) string {
	state := map[capacityplatform.State]int{capacityplatform.Normal: 0, capacityplatform.Degraded: 1, capacityplatform.Overloaded: 2}[snapshot.State]
	return fmt.Sprintf("# HELP domainry_runtime_capacity_state Capacity state: 0 normal, 1 degraded, 2 overloaded.\n# TYPE domainry_runtime_capacity_state gauge\ndomainry_runtime_capacity_state %d\n# HELP domainry_runtime_capacity_in_flight Global admitted requests.\n# TYPE domainry_runtime_capacity_in_flight gauge\ndomainry_runtime_capacity_in_flight{dimension=\"process\"} %d\n# HELP domainry_runtime_capacity_limit Configured global in-flight limit.\n# TYPE domainry_runtime_capacity_limit gauge\ndomainry_runtime_capacity_limit{dimension=\"process\"} %d\n# HELP domainry_runtime_capacity_rate Requests admitted in the active fixed window.\n# TYPE domainry_runtime_capacity_rate gauge\ndomainry_runtime_capacity_rate{dimension=\"process\"} %d\n# HELP domainry_runtime_capacity_rate_limit Configured request limit per fixed window.\n# TYPE domainry_runtime_capacity_rate_limit gauge\ndomainry_runtime_capacity_rate_limit{dimension=\"process\"} %d\n# HELP domainry_runtime_capacity_rejected_total Rejected admission attempts.\n# TYPE domainry_runtime_capacity_rejected_total counter\ndomainry_runtime_capacity_rejected_total %d\n", state, snapshot.GlobalInFlight, snapshot.GlobalLimit, snapshot.GlobalRate, snapshot.GlobalRateLimit, snapshot.RejectedTotal)
}
