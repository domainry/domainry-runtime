package http

import (
	"net/http"
	"strings"

	actioncontract "github.com/domainry/domainry-foundation/action"
	"github.com/domainry/domainry-foundation/modulehttp"
)

type moduleHTTPRoute struct {
	route   modulehttp.Route
	handler http.Handler
	owner   string
}

func buildModuleHTTPRouteIndex(adapters []modulehttp.Adapter) map[string]moduleHTTPRoute {
	index := map[string]moduleHTTPRoute{}
	for _, adapter := range adapters {
		if err := modulehttp.ValidateAdapter(adapter); err != nil {
			panic("invalid module HTTP adapter: " + err.Error())
		}
		for _, route := range adapter.Routes() {
			identity := strings.TrimSpace(route.Pattern())
			if previous, exists := index[identity]; exists {
				panic("module HTTP route " + identity + " is declared by both " + previous.owner + " and " + adapter.Owner())
			}
			index[identity] = moduleHTTPRoute{route: route, handler: adapter.Handler(), owner: strings.TrimSpace(adapter.Owner())}
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
		if !s.verifyModuleHTTPGovernance(w, r, binding) {
			return
		}
		binding.handler.ServeHTTP(w, r)
	})
}

func (s *HTTPRouter) verifyModuleHTTPGovernance(w http.ResponseWriter, r *http.Request, binding moduleHTTPRoute) bool {
	action := binding.route.Action
	if action.IdempotencyDecision == "caller_key_required" && strings.TrimSpace(r.Header.Get("Idempotency-Key")) == "" {
		s.rejectModuleHTTPRequest(w, r, binding, "backend.idempotency.key_required")
		return false
	}
	reason, err := decodeOperationReasonHeader(r.Header.Get(operationReasonHeader))
	if err != nil {
		s.rejectModuleHTTPRequest(w, r, binding, "operations.reason_encoding_invalid")
		return false
	}
	requiresReason := len(action.ApprovalPolicies) != 0
	if requiresReason && reason == "" {
		s.rejectModuleHTTPRequest(w, r, binding, "operations.reason_required")
		return false
	}
	if reason != "" {
		r.Header.Set(operationReasonHeader, reason)
	}
	if moduleHTTPActionHasApprovalPolicy(action, actioncontract.ApprovalConfirmation) {
		if strings.TrimSpace(r.Header.Get(operationConfirmationHeader)) != operationConfirmedValue {
			s.rejectModuleHTTPRequest(w, r, binding, "operations.confirmation_required")
			return false
		}
	}
	if moduleHTTPActionHasApprovalPolicy(action, actioncontract.ApprovalBreakGlass) {
		if strings.TrimSpace(r.Header.Get(operationConfirmationHeader)) != operationBreakGlassValue {
			s.rejectModuleHTTPRequest(w, r, binding, "operations.break_glass_confirmation_required")
			return false
		}
	}
	return true
}

func moduleHTTPActionHasApprovalPolicy(action actioncontract.ActionDefinition, wanted actioncontract.ApprovalPolicy) bool {
	for _, policy := range action.ApprovalPolicies {
		if policy == wanted {
			return true
		}
	}
	return false
}

func (s *HTTPRouter) rejectModuleHTTPRequest(w http.ResponseWriter, r *http.Request, binding moduleHTTPRoute, code string) {
	s.appendSecurityAudit(r, "module_http_request_rejected", "Module HTTP request rejected by host policy", map[string]any{
		"owner": binding.owner, "route": binding.route.Pattern(), "code": code,
	})
	writeError(w, r, http.StatusBadRequest, code)
}

func moduleRouteVisibleOnListener(route modulehttp.Route, group ListenerRouteGroup) bool {
	want := modulehttp.Exposure("")
	switch group {
	case ListenerRouteGroupPublic:
		want = modulehttp.ExposurePublic
	case ListenerRouteGroupManagement:
		want = modulehttp.ExposureManagement
	case ListenerRouteGroupOps:
		want = modulehttp.ExposureOps
	default:
		return false
	}
	for _, exposure := range route.Action.Exposures {
		if exposure == want {
			return true
		}
	}
	return false
}
