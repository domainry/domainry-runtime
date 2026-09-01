package http

import (
	"net/http"
	"strings"

	"github.com/domainry/domainry-foundation/modulehttp"
)

type moduleHTTPRoute struct {
	route   modulehttp.Route
	handler http.Handler
	owner   string
}

func buildModuleHTTPRouteIndex(surfaces []modulehttp.Surface) map[string]moduleHTTPRoute {
	index := map[string]moduleHTTPRoute{}
	for _, surface := range surfaces {
		if err := modulehttp.ValidateSurface(surface); err != nil {
			panic("invalid module HTTP surface: " + err.Error())
		}
		for _, route := range surface.Routes() {
			identity := strings.TrimSpace(route.Pattern)
			if previous, exists := index[identity]; exists {
				panic("module HTTP route " + identity + " is declared by both " + previous.owner + " and " + surface.Owner())
			}
			index[identity] = moduleHTTPRoute{route: route, handler: surface.Handler(), owner: strings.TrimSpace(surface.Owner())}
		}
	}
	return index
}

func (s *HTTPRouter) registerModuleHTTPRoutes(mux *http.ServeMux) {
	for identity, binding := range s.moduleHTTPRoutes {
		if _, retainedRuntimeRoute := runtimeEndpointContracts[identity]; retainedRuntimeRoute {
			continue
		}
		mux.Handle(identity, s.moduleHTTPRouteHandler(binding))
	}
}

func (s *HTTPRouter) moduleHTTPRouteHandler(binding moduleHTTPRoute) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authorizeModuleHTTPRequest(w, r, binding) || !s.verifyModuleHTTPGovernance(w, r, binding) {
			return
		}
		binding.handler.ServeHTTP(w, r)
	})
}

func (s *HTTPRouter) authorizeModuleHTTPRequest(w http.ResponseWriter, r *http.Request, binding moduleHTTPRoute) bool {
	switch binding.route.Authentication {
	case modulehttp.AuthenticationAnonymous:
		return true
	case modulehttp.AuthenticationService:
		if strings.TrimSpace(apiKeyTokenFromRequest(r)) == "" {
			writeError(w, r, http.StatusUnauthorized, "auth.service_identity_required")
			return false
		}
	}
	principal := s.principalFromRequest(r)
	if !principal.Known {
		writeError(w, r, http.StatusUnauthorized, "auth.session_expired")
		return false
	}
	if permission := strings.TrimSpace(binding.route.Permission); permission != "" && !principal.HasPermission(permission) {
		writeError(w, r, http.StatusForbidden, "auth.permission_denied")
		return false
	}
	if len(binding.route.AnyPermissions) > 0 {
		allowed := false
		for _, permission := range binding.route.AnyPermissions {
			if principal.HasPermission(permission) {
				allowed = true
				break
			}
		}
		if !allowed {
			writeError(w, r, http.StatusForbidden, "auth.permission_denied")
			return false
		}
	}
	return true
}

func (s *HTTPRouter) verifyModuleHTTPGovernance(w http.ResponseWriter, r *http.Request, binding moduleHTTPRoute) bool {
	governance := binding.route.Governance
	if governance == nil {
		return true
	}
	if governance.IdempotencyDecision == "caller_key_required" && strings.TrimSpace(r.Header.Get("Idempotency-Key")) == "" {
		s.rejectModuleHTTPRequest(w, r, binding, "backend.idempotency.key_required")
		return false
	}
	reason, err := decodeOperationReasonHeader(r.Header.Get(operationReasonHeader))
	if err != nil {
		s.rejectModuleHTTPRequest(w, r, binding, "operations.reason_encoding_invalid")
		return false
	}
	requiresReason := governance.HighRiskPolicy == modulehttp.HighRiskReasonRequired || governance.HighRiskPolicy == modulehttp.HighRiskConfirmationRequired || governance.HighRiskPolicy == modulehttp.HighRiskBreakGlassRequired
	if requiresReason && reason == "" {
		s.rejectModuleHTTPRequest(w, r, binding, "operations.reason_required")
		return false
	}
	if reason != "" {
		r.Header.Set(operationReasonHeader, reason)
	}
	switch governance.HighRiskPolicy {
	case modulehttp.HighRiskConfirmationRequired:
		if strings.TrimSpace(r.Header.Get(operationConfirmationHeader)) != operationConfirmedValue {
			s.rejectModuleHTTPRequest(w, r, binding, "operations.confirmation_required")
			return false
		}
	case modulehttp.HighRiskBreakGlassRequired:
		if strings.TrimSpace(r.Header.Get(operationConfirmationHeader)) != operationBreakGlassValue {
			s.rejectModuleHTTPRequest(w, r, binding, "operations.break_glass_confirmation_required")
			return false
		}
	}
	return true
}

func (s *HTTPRouter) rejectModuleHTTPRequest(w http.ResponseWriter, r *http.Request, binding moduleHTTPRoute, code string) {
	s.appendSecurityAudit(r, "module_http_request_rejected", "Module HTTP request rejected by host policy", map[string]any{
		"owner": binding.owner, "route": binding.route.Pattern, "code": code,
	})
	writeError(w, r, http.StatusBadRequest, code)
}

func moduleRouteVisibleOnListener(route modulehttp.Route, group ListenerRouteGroup) bool {
	want := modulehttp.Exposure("")
	switch group {
	case ListenerRouteGroupPublic:
		want = modulehttp.ExposurePublic
	case ListenerRouteGroupTenantAdmin:
		want = modulehttp.ExposureTenantAdmin
	case ListenerRouteGroupOps:
		want = modulehttp.ExposureOps
	default:
		return false
	}
	for _, exposure := range route.Exposures {
		if exposure == want {
			return true
		}
	}
	return false
}
