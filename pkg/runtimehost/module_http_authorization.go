package runtimehost

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

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
		switch route.Authentication {
		case modulehttp.AuthenticationAuthenticated:
			if len(route.AnyPermissions) != 0 {
				protected = middleware.RequireAnyPermission(route.AnyPermissions, governed)
			} else if permission := strings.TrimSpace(route.Permission); permission != "" {
				protected = middleware.RequirePermission(permission, governed)
			} else {
				protected = middleware.RequireAuthenticated(governed)
			}
		case modulehttp.AuthenticationService:
			return nil, fmt.Errorf("service-authenticated module routes require an explicit service principal verifier")
		default:
			return nil, fmt.Errorf("unsupported guarded authentication %q", route.Authentication)
		}
		return middleware.Authenticate(protected), nil
	}, nil
}

func governModuleHTTPRoute(route modulehttp.Route, next http.Handler) http.Handler {
	if route.Governance == nil {
		return next
	}
	governance := *route.Governance
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if governance.IdempotencyDecision == "caller_key_required" && strings.TrimSpace(request.Header.Get("Idempotency-Key")) == "" {
			writeModuleGovernanceError(writer, http.StatusBadRequest, "backend.idempotency.key_required")
			return
		}
		if governance.HighRiskPolicy != modulehttp.HighRiskNone {
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
		switch governance.HighRiskPolicy {
		case modulehttp.HighRiskConfirmationRequired:
			if strings.TrimSpace(request.Header.Get("X-Operation-Confirmation")) != "confirmed" {
				writeModuleGovernanceError(writer, http.StatusBadRequest, "operations.confirmation_required")
				return
			}
		case modulehttp.HighRiskBreakGlassRequired:
			if strings.TrimSpace(request.Header.Get("X-Operation-Confirmation")) != "break-glass" {
				writeModuleGovernanceError(writer, http.StatusBadRequest, "operations.break_glass_confirmation_required")
				return
			}
		}
		next.ServeHTTP(writer, request)
	})
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
