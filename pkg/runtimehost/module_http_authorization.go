package runtimehost

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	actioncontract "github.com/domainry/domainry-foundation/action"
	"github.com/domainry/domainry-foundation/modulehttp"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	identityprincipal "github.com/domainry/domainry-identity-sdk/authorization/principal"
	identityhttpmiddleware "github.com/domainry/domainry-identity-sdk/httpmiddleware"
)

type moduleRouteGuard func(modulehttp.Route, http.Handler) (http.Handler, error)

func newModuleHTTPRouteGuard(binding identitysdk.Binding) (moduleRouteGuard, error) {
	resolver, err := identityprincipal.NewResolver(binding, identityprincipal.Options{})
	if err != nil {
		return nil, fmt.Errorf("construct module HTTP principal resolver: %w", err)
	}
	middleware, err := identityhttpmiddleware.New(resolver, identityhttpmiddleware.WithAuthorization(binding.Authorization()))
	if err != nil {
		return nil, fmt.Errorf("construct module HTTP identity middleware: %w", err)
	}
	return func(route modulehttp.Route, next http.Handler) (http.Handler, error) {
		governed := governModuleHTTPRoute(route, next)
		var protected http.Handler
		switch route.Action.Authorization.Strategy {
		case actioncontract.AuthorizationExactRolePermission:
			if route.Action.Permission == nil {
				return nil, fmt.Errorf("module action %q has no exact permission", route.Action.Key)
			}
			protected = middleware.RequirePermission(route.Action.Permission.Key, governed)
		case actioncontract.AuthorizationAuthenticatedPrincipal, actioncontract.AuthorizationSelfOrPermission, actioncontract.AuthorizationOperationsIdentity:
			protected = middleware.RequireAuthenticated(governed)
		case actioncontract.AuthorizationServiceIdentity:
			servicesBinding, ok := binding.(identitysdk.ApplicationServiceVerificationBinding)
			if !ok || servicesBinding.ApplicationServiceVerifier() == nil {
				return nil, fmt.Errorf("service-authenticated module route %q requires an application service verifier", route.Action.Key)
			}
			grant, err := moduleServiceIdentityGrant(route.Action)
			if err != nil {
				return nil, err
			}
			audience := identitysdk.ApplicationKey(strings.TrimSpace(binding.Descriptor().Audience))
			if !audience.Valid() || !containsModuleServiceAudience(route.Action.Authorization.Audiences, string(audience)) {
				return nil, fmt.Errorf("service-authenticated module route %q does not allow the Runtime audience", route.Action.Key)
			}
			return requireModuleServiceIdentity(servicesBinding.ApplicationServiceVerifier(), audience, grant, governed), nil
		case actioncontract.AuthorizationDelegatedCredential:
			return governed, nil
		case actioncontract.AuthorizationAnonymousProtocol:
			return governed, nil
		default:
			return nil, fmt.Errorf("unsupported module action authorization %q", route.Action.Authorization.Strategy)
		}
		return middleware.Authenticate(protected), nil
	}, nil
}

func moduleServiceIdentityGrant(action actioncontract.ActionDefinition) (identitysdk.ApplicationServiceGrant, error) {
	policyKey := strings.TrimSpace(action.Authorization.PolicyKey)
	separator := strings.LastIndexByte(policyKey, '.')
	if separator <= 0 || separator == len(policyKey)-1 {
		return identitysdk.ApplicationServiceGrant{}, fmt.Errorf("service-authenticated module action %q has an invalid policy key", action.Key)
	}
	grant := identitysdk.ApplicationServiceGrant{Resource: identitysdk.ResourceType(policyKey[:separator]), Action: identitysdk.Action(policyKey[separator+1:])}
	if !grant.Valid() {
		return identitysdk.ApplicationServiceGrant{}, fmt.Errorf("service-authenticated module action %q has an invalid grant", action.Key)
	}
	return grant, nil
}

func containsModuleServiceAudience(values []string, audience string) bool {
	for _, value := range values {
		if strings.TrimSpace(value) == audience {
			return true
		}
	}
	return false
}

func requireModuleServiceIdentity(services identitysdk.ApplicationServiceTokenVerifier, audience identitysdk.ApplicationKey, grant identitysdk.ApplicationServiceGrant, next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		accessToken, ok := identityhttpmiddleware.BearerToken(request)
		if !ok {
			writeModuleGovernanceError(writer, http.StatusUnauthorized, "auth.service_identity_required")
			return
		}
		_, err := services.Verify(request.Context(), identitysdk.VerifyApplicationServiceTokenRequest{AccessToken: accessToken, Audience: audience, Grant: grant})
		if err != nil {
			status := http.StatusUnauthorized
			var sdkError *identitysdk.Error
			if errors.As(err, &sdkError) && sdkError.StatusCode >= http.StatusInternalServerError {
				status = http.StatusServiceUnavailable
			}
			writeModuleGovernanceError(writer, status, "auth.service_identity_invalid")
			return
		}
		next.ServeHTTP(writer, request)
	})
}

func governModuleHTTPRoute(route modulehttp.Route, next http.Handler) http.Handler {
	action := route.Action
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if action.IdempotencyDecision == "caller_key_required" && strings.TrimSpace(request.Header.Get("Idempotency-Key")) == "" {
			writeModuleGovernanceError(writer, http.StatusBadRequest, "backend.idempotency.key_required")
			return
		}
		if len(action.ApprovalPolicies) != 0 {
			reason, err := decodeModuleOperationReason(request.Header.Get("X-Operation-Reason"))
			if err != nil {
				writeModuleGovernanceError(writer, http.StatusBadRequest, "operations.reason_encoding_invalid")
				return
			}
			if reason == "" {
				writeModuleGovernanceError(writer, http.StatusBadRequest, "operations.reason_required")
				return
			}
			request.Header.Set("X-Operation-Reason", reason)
		}
		if moduleActionHasApprovalPolicy(action, actioncontract.ApprovalConfirmation) {
			if strings.TrimSpace(request.Header.Get("X-Operation-Confirmation")) != "confirmed" {
				writeModuleGovernanceError(writer, http.StatusBadRequest, "operations.confirmation_required")
				return
			}
		}
		if moduleActionHasApprovalPolicy(action, actioncontract.ApprovalBreakGlass) {
			if strings.TrimSpace(request.Header.Get("X-Operation-Confirmation")) != "break-glass" {
				writeModuleGovernanceError(writer, http.StatusBadRequest, "operations.break_glass_confirmation_required")
				return
			}
		}
		next.ServeHTTP(writer, request)
	})
}

func moduleActionHasApprovalPolicy(action actioncontract.ActionDefinition, wanted actioncontract.ApprovalPolicy) bool {
	for _, policy := range action.ApprovalPolicies {
		if policy == wanted {
			return true
		}
	}
	return false
}

func decodeModuleOperationReason(value string) (string, error) {
	const prefix = "UTF-8''"
	raw := strings.TrimSpace(value)
	if !strings.HasPrefix(raw, prefix) {
		return raw, nil
	}
	decoded, err := url.QueryUnescape(strings.TrimPrefix(raw, prefix))
	if err != nil || !utf8.ValidString(decoded) {
		return "", fmt.Errorf("invalid UTF-8 operation reason")
	}
	return strings.TrimSpace(decoded), nil
}

func writeModuleGovernanceError(writer http.ResponseWriter, status int, code string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.Header().Set("Cache-Control", "no-store")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]string{"code": code})
}
