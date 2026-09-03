package http

import (
	"net/http"
	"strings"

	actioncontract "github.com/domainry/domainry-foundation/action"
	runtimeactioncontract "github.com/domainry/domainry-runtime/runtime/domain/action/contract"
	workflowcontract "github.com/domainry/domainry-runtime/runtime/domain/workflow/contract"
)

type resolvedRequestAction struct {
	definition actioncontract.ActionDefinition
	found      bool
	dynamic    bool
}

func (s *HTTPRouter) resolveRequestAction(routes *http.ServeMux, r *http.Request) resolvedRequestAction {
	if s == nil || r == nil || s.authorizationActions == nil {
		return resolvedRequestAction{}
	}
	registry := s.authorizationActions()
	if registry == nil || !registry.Frozen() {
		return resolvedRequestAction{}
	}
	policy := routePolicyFor(routes, r)
	if policy.fallback {
		return resolvedRequestAction{}
	}
	objectKey := matchedRouteValue(policy.path, r.URL.Path, "objectKey")
	if operation, dynamic := defaultObjectOperation(r.Method, policy.path); dynamic {
		definition, found := registry.ResolveNonHTTP("runtime_object_action", objectKey+"."+operation)
		return resolvedRequestAction{definition: definition, found: found && actionBelongsToObject(definition, objectKey), dynamic: true}
	}
	if authoredActionRoute(r.Method, policy.path) {
		definition, found := registry.ResolveNonHTTP("runtime_action", matchedRouteValue(policy.path, r.URL.Path, "actionKey"))
		return resolvedRequestAction{definition: definition, found: found && actionBelongsToObject(definition, objectKey), dynamic: true}
	}
	if workflowRunRoute(r.Method, policy.path) {
		workflowKey := matchedRouteValue(policy.path, r.URL.Path, "workflowKey")
		definition, found := registry.ResolveNonHTTP(workflowcontract.RunActionBindingKind, workflowKey)
		return resolvedRequestAction{definition: definition, found: found, dynamic: true}
	}
	definition, found := registry.ResolveHTTP(r.Method, policy.path)
	return resolvedRequestAction{definition: definition, found: found}
}

// matchedRouteValue reads a standard ServeMux path variable before the mux
// dispatches the handler. Request.PathValue is populated only during dispatch,
// while the authorization middleware must resolve the Action first.
func matchedRouteValue(routeTemplate, requestPath, name string) string {
	templateParts := strings.Split(strings.Trim(strings.TrimSpace(routeTemplate), "/"), "/")
	requestParts := strings.Split(strings.Trim(strings.TrimSpace(requestPath), "/"), "/")
	if len(templateParts) != len(requestParts) {
		return ""
	}
	wanted := "{" + strings.TrimSpace(name) + "}"
	for index := range templateParts {
		if templateParts[index] == wanted {
			return strings.TrimSpace(requestParts[index])
		}
	}
	return ""
}

func defaultObjectOperation(method, routeTemplate string) (string, bool) {
	identity := strings.ToUpper(strings.TrimSpace(method)) + " " + strings.TrimSpace(routeTemplate)
	switch identity {
	case "GET /objects/{objectKey}/records",
		"GET /objects/{objectKey}/records/{recordID}",
		"GET /objects/{objectKey}/records/{recordID}/references",
		"GET /objects/{objectKey}/records/{recordID}/related/{relatedObjectKey}":
		return "read", true
	case "POST /objects/{objectKey}/records":
		return "create", true
	case "PATCH /objects/{objectKey}/records/{recordID}":
		return "update", true
	case "DELETE /objects/{objectKey}/records/{recordID}":
		return "delete", true
	case "POST /objects/{objectKey}/records/export":
		return "export", true
	default:
		return "", false
	}
}

func authoredActionRoute(method, routeTemplate string) bool {
	if strings.ToUpper(strings.TrimSpace(method)) != http.MethodPost {
		return false
	}
	switch strings.TrimSpace(routeTemplate) {
	case "/objects/{objectKey}/actions/{actionKey}/run",
		"/objects/{objectKey}/actions/{actionKey}/bulk",
		"/objects/{objectKey}/records/{recordID}/actions/{actionKey}":
		return true
	default:
		return false
	}
}

func workflowRunRoute(method, routeTemplate string) bool {
	if strings.ToUpper(strings.TrimSpace(method)) != http.MethodPost {
		return false
	}
	switch strings.TrimSpace(routeTemplate) {
	case "/business/workflows/{workflowKey}/run", "/portal/workflows/{workflowKey}/run":
		return true
	default:
		return false
	}
}

func actionBelongsToObject(definition actioncontract.ActionDefinition, objectKey string) bool {
	objectKey = strings.TrimSpace(objectKey)
	return objectKey != "" && definition.Permission != nil && strings.TrimSpace(definition.Permission.ResourceKey) == objectKey
}

func (s *HTTPRouter) withActionAuthorization(routes *http.ServeMux, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		policy := routePolicyFor(routes, r)
		if r.Method == http.MethodOptions || policy.fallback {
			next.ServeHTTP(w, r)
			return
		}
		resolved := s.resolveRequestAction(routes, r)
		if !resolved.found {
			s.appendSecurityAudit(r, "runtime_action_unresolved", "Runtime request did not resolve to an active Action", map[string]any{
				"route": policy.path, "dynamic": resolved.dynamic,
			})
			if resolved.dynamic {
				writeError(w, r, http.StatusForbidden, "auth.permission_denied")
			} else {
				writeError(w, r, http.StatusServiceUnavailable, "identity.authorization_unavailable")
			}
			return
		}
		action := resolved.definition
		principal := s.principalFromRequest(r)
		serveAuthorized := func() {
			ctx := runtimeactioncontract.WithAuthorizedAction(r.Context(), action)
			next.ServeHTTP(w, r.WithContext(ctx))
		}
		switch action.Authorization.Strategy {
		case actioncontract.AuthorizationAnonymous:
			serveAuthorized()
			return
		case actioncontract.AuthorizationSigned:
			// Signed Actions are routed to their registered signature middleware.
			// The outer user-authentication layer intentionally does not resolve a
			// login Principal for machine requests.
			serveAuthorized()
			return
		case actioncontract.AuthorizationAuthenticated:
			if !principal.Known {
				writeError(w, r, http.StatusUnauthorized, "auth.session_expired")
				return
			}
			if action.Permission == nil || strings.TrimSpace(action.Authorization.PolicyKey) != "" || principal.HasExactPermission(action.Permission.Key) {
				serveAuthorized()
				return
			}
			writeError(w, r, http.StatusForbidden, "auth.permission_denied")
		default:
			writeError(w, r, http.StatusForbidden, "auth.permission_denied")
		}
	})
}
