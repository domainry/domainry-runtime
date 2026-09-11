package http

import (
	operationscontract "github.com/domainry/domainry-runtime/runtime/domain/operations/contract"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	actioncontract "github.com/domainry/domainry-foundation/action"

	apperror "github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/logging"
	"github.com/domainry/domainry-foundation/requestcontext"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	localization "github.com/domainry/domainry-runtime/runtime/platform/localization"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

const (
	requestIDHeader     = "X-Request-ID"
	correlationIDHeader = "X-Correlation-ID"
)

var httpTracePropagator = propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{})

type statusRecorder struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (r *statusRecorder) WriteHeader(status int) {
	if r.wroteHeader {
		return
	}
	r.status = status
	r.wroteHeader = true
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Write(payload []byte) (int, error) {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
	return r.ResponseWriter.Write(payload)
}

// Unwrap preserves optional ResponseWriter capabilities for
// http.ResponseController (flush, deadlines, hijacking, and full duplex).
func (r *statusRecorder) Unwrap() http.ResponseWriter {
	return r.ResponseWriter
}

func (s *HTTPRouter) withRecovery(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if recovered := recover(); recovered != nil {
				if recovered == http.ErrAbortHandler {
					panic(recovered)
				}
				logging.FromContext(r.Context()).Error("http panic recovered", zap.Any("panic", recovered), zap.ByteString("stack", debug.Stack()))
				if recorder, ok := w.(*statusRecorder); !ok || !recorder.wroteHeader {
					writeError(w, r, http.StatusInternalServerError, "backend.internal")
				}
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *HTTPRouter) withMetrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		if s.httpMetrics != nil {
			s.httpMetrics.Begin()
		}
		requestID := requestIDFromRequest(r)
		correlationID := correlationIDFromRequest(r, requestID)
		r.Header.Set(requestIDHeader, requestID)
		r.Header.Set(correlationIDHeader, correlationID)
		ctx := httpTracePropagator.Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		ctx = requestcontext.WithRequestID(ctx, requestID)
		ctx = requestcontext.WithCorrelationID(ctx, correlationID)
		ctx = requestcontext.WithWorkspaceID(ctx, explicitWorkspaceIDFromRequest(r))
		ctx = requestcontext.WithActorID(ctx, strings.TrimSpace(r.Header.Get("X-User-ID")))
		if s.allowDevAuthHeaders {
			// Acceptance failure injection is a development-only control that
			// shares the same gate as header-based development identities.
			ctx = actionmodel.WithAcceptanceFailurePoint(ctx, r.Header.Get(actionmodel.AcceptanceFailureHeader))
		}
		ctx, span := otel.Tracer("domainry.runtime.http").Start(ctx, "HTTP "+r.Method, trace.WithSpanKind(trace.SpanKindServer), trace.WithAttributes(
			attribute.String("http.request.method", r.Method),
		))
		defer span.End()
		r = r.WithContext(ctx)
		w.Header().Set(requestIDHeader, requestID)
		w.Header().Set(correlationIDHeader, correlationID)
		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r)
		path := r.Pattern
		if strings.TrimSpace(path) == "" {
			path = "unmatched"
		}
		span.SetName(r.Method + " " + path)
		span.SetAttributes(attribute.String("http.route", path), attribute.Int("http.response.status_code", recorder.status))
		if recorder.status >= http.StatusInternalServerError {
			span.SetStatus(codes.Error, http.StatusText(recorder.status))
		}
		s.observeHTTPRequest(r, path, recorder.status, time.Since(start))
	})
}

func requestIDFromRequest(r *http.Request) string {
	if value := normalizedCorrelationValue(r.Header.Get(requestIDHeader)); value != "" {
		return value
	}
	return requestcontext.NewRequestID()
}

func correlationIDFromRequest(r *http.Request, requestID string) string {
	if value := normalizedCorrelationValue(r.Header.Get(correlationIDHeader)); value != "" {
		return value
	}
	return requestID
}

func normalizedCorrelationValue(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" || len(value) > 128 {
		return ""
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || strings.ContainsRune("._:-", character) {
			continue
		}
		return ""
	}
	return value
}

func requestLocale(r *http.Request) string {
	if r == nil {
		return localization.DefaultLocale
	}
	if value := strings.TrimSpace(r.URL.Query().Get("locale")); value != "" {
		return supportedRequestLocale(value)
	}
	if value := strings.TrimSpace(r.Header.Get("X-Locale")); value != "" {
		return supportedRequestLocale(value)
	}
	acceptLanguage := strings.TrimSpace(r.Header.Get("Accept-Language"))
	if acceptLanguage != "" {
		first := strings.TrimSpace(strings.Split(acceptLanguage, ",")[0])
		first = strings.TrimSpace(strings.Split(first, ";")[0])
		if first != "" {
			return supportedRequestLocale(first)
		}
	}
	return localization.DefaultLocale
}

func supportedRequestLocale(locale string) string {
	normalized := localization.NormalizeLocale(locale)
	for _, supported := range localization.SupportedLocales() {
		if supported == normalized {
			return normalized
		}
	}
	return localization.DefaultLocale
}

func (s *HTTPRouter) observeHTTPRequest(r *http.Request, path string, status int, duration time.Duration) {
	if s.httpMetrics != nil {
		s.httpMetrics.Observe(r.Method, path, status, duration)
	}
	logging.FromContext(r.Context()).Info("http_request", logging.Fields(map[string]any{
		"event":        "http_request",
		"workspace_id": workspaceIDFromRequest(r),
		"actor_id":     s.actorIDFromRequest(r),
		"role":         valueOrDefault(strings.TrimSpace(r.Header.Get("X-Preview-Role")), valueOrDefault(strings.TrimSpace(r.Header.Get("X-Role")), strings.TrimSpace(r.Header.Get("X-User-Role")))),
		"method":       r.Method,
		"path":         path,
		"status":       status,
		"duration_ms":  duration.Milliseconds(),
		"object_key":   strings.TrimSpace(r.PathValue("objectKey")),
		"action_key":   strings.TrimSpace(r.PathValue("actionKey")),
		"workflow_key": strings.TrimSpace(r.PathValue("workflowKey")),
		"execution_id": strings.TrimSpace(r.PathValue("executionID")),
	})...)
}

func (s *HTTPRouter) httpMetricsSnapshot() []HTTPRequestMetric {
	if s.httpMetrics == nil {
		return []HTTPRequestMetric{}
	}
	return s.httpMetrics.Snapshot()
}

func (s *HTTPRouter) httpMetricsSummary() map[string]int64 {
	if s.httpMetrics == nil {
		return map[string]int64{"request_count": 0, "error_count": 0, "series_count": 0, "dropped_series_count": 0}
	}
	return s.httpMetrics.Summary()
}

func (s *HTTPRouter) withCORS(next http.Handler) http.Handler {
	return CORSMiddleware(s.corsAllowedOrigins, next)
}

// CORSMiddleware applies the Runtime's browser cross-origin policy to any
// handler. The process host serves module-owned routes (Identity /auth,
// Report /report) ahead of the Runtime router, so it applies the same policy
// there; a browser reads a module response only when it carries these headers.
func CORSMiddleware(allowedOrigins []string, next http.Handler) http.Handler {
	origins := normalizeCORSOrigins(allowedOrigins)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if origin, ok := allowedCORSOriginFor(origins, r.Header.Get("Origin")); ok {
			w.Header().Set("Access-Control-Allow-Origin", origin)
			w.Header().Set("Vary", "Origin")
		}
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type, If-Match, Last-Event-ID, Traceparent, Tracestate, Baggage, X-API-Key, X-User-ID, X-Role, X-User-Role, X-Preview-Role, X-Preview-User-ID, X-Workspace-ID, X-Request-ID, X-Correlation-ID, X-Operation-Reason, X-Operation-Confirmation, Builder-Task-ID, Idempotency-Key, Expected-Schema-Hash, Runtime-Authoring-Evidence-Step-Token")
		w.Header().Set("Access-Control-Expose-Headers", "X-Request-ID, X-Correlation-ID, X-Resource-Hash, X-Operation-ID, X-Operation-Status, X-Operation-Replayed, Operation-ID, Operation-Location, Idempotency-Replayed, Runtime-Authoring-Step-Receipt, Runtime-Authoring-Evidence-Error")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func normalizeCORSOrigins(origins []string) []string {
	out := make([]string, 0, len(origins))
	for _, origin := range origins {
		origin = strings.TrimSpace(origin)
		if origin != "" {
			out = append(out, origin)
		}
	}
	return out
}

func (s *HTTPRouter) allowedCORSOrigin(origin string) (string, bool) {
	return allowedCORSOriginFor(s.corsAllowedOrigins, origin)
}

func allowedCORSOriginFor(allowedOrigins []string, origin string) (string, bool) {
	origin = strings.TrimSpace(origin)
	if origin == "" {
		return "", false
	}
	for _, allowed := range allowedOrigins {
		if allowed == "*" {
			return "*", true
		}
		if strings.EqualFold(allowed, origin) {
			return origin, true
		}
	}
	return "", false
}

func (s *HTTPRouter) withAuth(routes *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		policy := routePolicyFor(routes, r)
		resolved := s.resolveRequestAction(routes, r)
		if r.Method == http.MethodOptions || resolved.found && (resolved.definition.Authorization.Strategy == actioncontract.AuthorizationAnonymous || resolved.definition.Authorization.Strategy == actioncontract.AuthorizationSigned) || policy.fallback {
			next.ServeHTTP(w, r)
			return
		}
		if token := apiKeyTokenFromRequest(r); token != "" {
			principal, _, err := s.integrationAuth.PrincipalFromIntegrationAPIKey(r.Context(), token, workspaceIDFromRequest(r), requestIDFromRequest(r))
			if err != nil {
				if apperror.CodeOf(err) == "backend.integration.api_key.rate_limited" {
					s.appendSecurityAudit(r, "auth_api_rate_limited", "Business API key rate limited", map[string]any{"path": r.URL.Path, "method": r.Method, "reason": "rate_limited"})
					writeError(w, r, http.StatusTooManyRequests, "backend.integration.api_key.rate_limited")
					return
				}
				s.appendSecurityAudit(r, "auth_api_denied", "Business API key rejected", map[string]any{"path": r.URL.Path, "method": r.Method, "reason": "invalid_api_key"})
				writeError(w, r, http.StatusUnauthorized, "auth.session_expired")
				return
			}
			if !principal.Known {
				s.appendSecurityAudit(r, "auth_api_denied", "Business API key resolved an inactive Principal", map[string]any{"path": r.URL.Path, "method": r.Method, "reason": "principal_inactive"})
				writeError(w, r, http.StatusUnauthorized, "auth.session_expired")
				return
			}
			if !s.admitAuthenticatedWorkspace(w, r, principal) {
				return
			}
			if !authenticatedWorkspaceMatchesRequest(principal, r) {
				s.appendSecurityAuditForPrincipal(r, principal, "auth_workspace_denied", "Authenticated workspace does not match request target", map[string]any{"target_workspace_id": explicitWorkspaceIDFromRequest(r)})
				writeError(w, r, http.StatusForbidden, "backend.workspace_scope_mismatch")
				return
			}
			authenticatedRequest := requestWithPrincipal(r, principal)
			if principal.Known && principal.AccessBundle != nil {
				sdkPrincipal := principal.Principal
				authenticatedRequest = authenticatedRequest.WithContext(identitysdk.WithRequestIdentity(
					authenticatedRequest.Context(), identitysdk.RequestIdentity{Principal: sdkPrincipal},
				))
			}
			next.ServeHTTP(w, authenticatedRequest)
			return
		}
		if s.allowDevAuthHeaders && devAuthHeadersPresent(r) {
			next.ServeHTTP(w, r)
			return
		}
		if operationscontract.BuilderTaskID(r.Context()) != "" {
			next.ServeHTTP(w, r)
			return
		}
		if s.identityAuthentication == nil || s.identityPrincipal == nil {
			s.appendSecurityAudit(r, "auth_api_denied", "Identity SDK middleware is unavailable", map[string]any{"path": r.URL.Path, "method": r.Method, "reason": "identity_binding_unavailable"})
			writeError(w, r, http.StatusServiceUnavailable, "identity.authentication_unavailable")
			return
		}
		authenticatedBusinessRequest := s.identityAuthentication.RequirePasswordChanged(http.HandlerFunc(func(w http.ResponseWriter, authenticatedRequest *http.Request) {
			requestIdentity, ok := identitysdk.RequestIdentityFromContext(authenticatedRequest.Context())
			if !ok {
				writeError(w, authenticatedRequest, http.StatusUnauthorized, "auth.token_invalid")
				return
			}
			principal := s.identityPrincipal(requestIdentity.Principal, requestIDFromRequest(authenticatedRequest))
			if !principal.Known {
				s.appendSecurityAudit(authenticatedRequest, "auth_api_denied", "Session resolved an inactive Principal", map[string]any{"path": authenticatedRequest.URL.Path, "method": authenticatedRequest.Method, "reason": "principal_inactive"})
				writeError(w, authenticatedRequest, http.StatusUnauthorized, "auth.session_expired")
				return
			}
			if !s.admitAuthenticatedWorkspace(w, authenticatedRequest, principal) {
				return
			}
			if !authenticatedWorkspaceMatchesRequest(principal, authenticatedRequest) {
				s.appendSecurityAuditForPrincipal(authenticatedRequest, principal, "auth_workspace_denied", "Authenticated workspace does not match request target", map[string]any{"target_workspace_id": explicitWorkspaceIDFromRequest(authenticatedRequest)})
				writeError(w, authenticatedRequest, http.StatusForbidden, "backend.workspace_scope_mismatch")
				return
			}
			next.ServeHTTP(w, requestWithPrincipal(authenticatedRequest, principal))
		}))
		s.identityAuthentication.Authenticate(authenticatedBusinessRequest).ServeHTTP(w, r)
	})
}

func (s *HTTPRouter) admitAuthenticatedWorkspace(w http.ResponseWriter, r *http.Request, principal principalmodel.Principal) bool {
	if s.workspaceAdmission == nil {
		return true
	}
	active, err := s.workspaceAdmission.WorkspaceActive(r.Context(), principal.WorkspaceID)
	if err != nil {
		s.appendSecurityAuditForPrincipal(r, principal, "auth_workspace_denied", "Workspace admission failed closed", map[string]any{"reason": "workspace_admission_failed"})
		writeError(w, r, http.StatusServiceUnavailable, "workspace.administration_unavailable")
		return false
	}
	if !active {
		s.appendSecurityAuditForPrincipal(r, principal, "auth_workspace_denied", "Workspace is suspended", map[string]any{"reason": "workspace_suspended"})
		writeError(w, r, http.StatusUnauthorized, "auth.workspace_suspended")
		return false
	}
	return true
}

func fallbackRoute(next http.Handler, r *http.Request) bool {
	mux, ok := next.(*http.ServeMux)
	if !ok {
		return false
	}
	return routePolicyFor(mux, r).fallback
}

type httpRoutePolicy struct {
	path     string
	fallback bool
}

func routePolicyFor(routes *http.ServeMux, r *http.Request) httpRoutePolicy {
	path := strings.TrimSpace(r.URL.Path)
	if routes == nil {
		return httpRoutePolicy{path: path}
	}
	_, pattern := routes.Handler(r)
	pattern = strings.TrimSpace(pattern)
	if _, routePath, found := strings.Cut(pattern, " "); found {
		pattern = routePath
	}
	if pattern == "" {
		return httpRoutePolicy{path: path}
	}
	return httpRoutePolicy{
		path:     pattern,
		fallback: pattern == "/{path...}" || (pattern == "/" && path != "/"),
	}
}

func (p httpRoutePolicy) anonymous() bool {
	return !p.fallback && anonymousAuthPath(p.path)
}

func anonymousAuthPath(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	if path == "/" || path == "/live" || path == "/ready" || path == "/startup" || path == "/notification/deliveries/accept" || path == "/dispatch/executions" || strings.HasPrefix(path, "/discovery/i18n/") {
		return true
	}
	return false
}
