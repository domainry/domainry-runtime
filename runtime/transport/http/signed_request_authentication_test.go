package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type signedRequestAuthenticatorStub struct {
	principal principalmodel.Principal
	presented bool
	err       error
}

func (stub signedRequestAuthenticatorStub) AuthenticateSignedRequest(*http.Request) (principalmodel.Principal, bool, error) {
	return stub.principal, stub.presented, stub.err
}

type signedRequestBusinessPrincipalProbe struct{ calls int }

func (probe *signedRequestBusinessPrincipalProbe) ResolveBusinessPrincipal(context.Context, principalmodel.Principal, string, string) (principalmodel.Principal, error) {
	probe.calls++
	return principalmodel.Principal{}, apperror.New(apperror.KindForbidden, "business_profile.must_not_run_for_signed_request", nil, nil)
}

func TestSignedRequestAuthenticationReachesExactRuntimeActionWithoutSession(t *testing.T) {
	probe := &signedRequestBusinessPrincipalProbe{}
	router := NewHTTPRouter(HTTPRouterConfig{}, HTTPRouterDependencies{BusinessPrincipal: probe})
	router.signedRequestAuth = signedRequestAuthenticatorStub{
		presented: true,
		principal: principalmodel.Principal{
			Principal:                     identitysdk.Principal{ContractVersion: identitysdk.PrincipalContextContractVersion, Known: true, WorkspaceID: "workspace-a", UserID: "service-a"},
			BusinessAuthorizationRevision: "auth-7",
		},
	}
	routes := http.NewServeMux()
	routes.HandleFunc("GET /uploads/{fileID}", func(response http.ResponseWriter, request *http.Request) {
		principal := router.principalFromRequest(request)
		if !principal.Known || principal.WorkspaceID != "workspace-a" || principal.UserID != "service-a" || principal.EffectiveAuthorizationRevision() != "auth-7" || !signedRequestPrincipal(request) {
			t.Fatalf("signed request principal was not preserved: %#v", principal)
		}
		response.WriteHeader(http.StatusNoContent)
	})
	handler := router.withAuth(routes, router.withActionAuthorization(routes, routes))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/uploads/art_opaque?download_ticket=signed", nil))
	if response.Code != http.StatusNoContent || probe.calls != 0 {
		t.Fatalf("status=%d body=%s business_profile_calls=%d", response.Code, response.Body.String(), probe.calls)
	}
}

func TestSignedRequestAuthenticationFailsBeforeRuntimeAction(t *testing.T) {
	router := NewHTTPRouter(HTTPRouterConfig{}, HTTPRouterDependencies{})
	router.signedRequestAuth = signedRequestAuthenticatorStub{
		presented: true,
		err:       apperror.New(apperror.KindForbidden, "backend.upload.download_ticket_invalid", nil, nil),
	}
	routes := http.NewServeMux()
	routes.HandleFunc("GET /uploads/{fileID}", func(http.ResponseWriter, *http.Request) {
		t.Fatal("invalid signed request reached the Runtime action")
	})
	response := httptest.NewRecorder()
	router.withAuth(routes, router.withActionAuthorization(routes, routes)).ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/uploads/art_opaque?download_ticket=forged", nil))
	if response.Code != http.StatusForbidden {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}
