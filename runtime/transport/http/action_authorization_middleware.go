package http

import (
	"net/http"
	"strings"

	actioncontract "github.com/domainry/domainry-foundation/action"
	operationscontract "github.com/domainry/domainry-runtime/runtime/domain/operations/contract"
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
	case "GET /objects/{objectKey}/records/export", "POST /objects/{objectKey}/records/export/jobs":
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
		switch action.Authorization.Strategy {
		case actioncontract.AuthorizationAnonymousProtocol:
			next.ServeHTTP(w, r)
			return
		case actioncontract.AuthorizationDelegatedCredential:
			next.ServeHTTP(w, r)
			return
		case actioncontract.AuthorizationAuthenticatedPrincipal:
			if principal.Known {
				next.ServeHTTP(w, r)
				return
			}
			writeError(w, r, http.StatusUnauthorized, "auth.session_expired")
		case actioncontract.AuthorizationExactRolePermission:
			if principal.Known && action.Permission != nil && principal.HasExactPermission(action.Permission.Key) {
				next.ServeHTTP(w, r)
				return
			}
			writeError(w, r, http.StatusForbidden, "auth.permission_denied")
		case actioncontract.AuthorizationServiceIdentity:
			if principal.Known && strings.TrimSpace(apiKeyTokenFromRequest(r)) != "" {
				next.ServeHTTP(w, r)
				return
			}
			writeError(w, r, http.StatusUnauthorized, "auth.service_identity_required")
		case actioncontract.AuthorizationOperationsIdentity:
			if operationscontract.BuilderTaskID(r.Context()) != "" {
				next.ServeHTTP(w, r)
				return
			}
			writeError(w, r, http.StatusForbidden, "auth.operations_identity_required")
		default:
			writeError(w, r, http.StatusForbidden, "auth.permission_denied")
		}
	})
}
