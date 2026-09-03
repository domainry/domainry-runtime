package runtimehost

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	actioncontract "github.com/domainry/domainry-foundation/action"
	"github.com/domainry/domainry-foundation/modulehttp"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	runtimetestkit "github.com/domainry/domainry-runtime/runtime/bootstrap/testkit"
)

type moduleServiceVerifierStub struct {
	calls   int
	request identitysdk.VerifyApplicationServiceTokenRequest
}

func (stub *moduleServiceVerifierStub) Verify(_ context.Context, request identitysdk.VerifyApplicationServiceTokenRequest) (identitysdk.ApplicationServicePrincipal, error) {
	stub.calls++
	stub.request = request
	if request.AccessToken != "valid-service-token" {
		return identitysdk.ApplicationServicePrincipal{}, &identitysdk.Error{StatusCode: http.StatusUnauthorized, Code: "invalid_service_token"}
	}
	return identitysdk.ApplicationServicePrincipal{SubjectID: "service:identity", Audience: request.Audience}, nil
}

type moduleServiceBindingStub struct {
	runtimetestkit.IdentityBindingStub
	services identitysdk.ApplicationServiceTokenVerifier
}

func (moduleServiceBindingStub) Descriptor() identitysdk.Descriptor {
	return identitysdk.Descriptor{Mode: identitysdk.DeploymentModeSaaS, Audience: "domainry-runtime"}
}

func (stub moduleServiceBindingStub) ApplicationServiceVerifier() identitysdk.ApplicationServiceTokenVerifier {
	return stub.services
}

func TestModuleHTTPGovernanceEnforcesIdempotencyReasonAndConfirmation(t *testing.T) {
	action := runtimeHostTestAction("test.governed.execute", "POST /governed", []actioncontract.Exposure{actioncontract.ExposureOps}, actioncontract.AuthorizationAuthenticated)
	action.EffectClass = actioncontract.EffectWrite
	action.IdempotencyDecision = "caller_key_required"
	action.ApprovalPolicies = []actioncontract.ApprovalPolicy{actioncontract.ApprovalConfirmation}
	route := modulehttp.Route{Action: action}
	handler := governModuleHTTPRoute(route, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { writer.WriteHeader(http.StatusNoContent) }))
	call := func(headers map[string]string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/governed", nil)
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if response := call(nil); response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "backend.idempotency.key_required") {
		t.Fatalf("missing key status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(map[string]string{"Idempotency-Key": "key-1"}); response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "operations.reason_required") {
		t.Fatalf("missing reason status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(map[string]string{"Idempotency-Key": "key-1", "X-Operation-Reason": "reviewed"}); response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "operations.confirmation_required") {
		t.Fatalf("missing confirmation status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(map[string]string{"Idempotency-Key": "key-1", "X-Operation-Reason": "reviewed", "X-Operation-Confirmation": "confirmed"}); response.Code != http.StatusNoContent {
		t.Fatalf("governed status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestModuleHTTPSignedRequestVerifiesExactAudienceAndGrant(t *testing.T) {
	verifier := &moduleServiceVerifierStub{}
	guard, err := newModuleHTTPRouteGuard(moduleServiceBindingStub{services: verifier})
	if err != nil {
		t.Fatal(err)
	}
	action := runtimeHostTestAction("runtime.authorization.action_usages.query", "POST /operations/authorization/action-usages/query", []actioncontract.Exposure{actioncontract.ExposureTenantAdmin}, actioncontract.AuthorizationSigned)
	action.Authorization = actioncontract.Authorization{
		Strategy: actioncontract.AuthorizationSigned, PolicyKey: "runtime.authorization.action_usages.query", Audiences: []string{"domainry-runtime"},
	}
	executed := 0
	handler, err := guard(modulehttp.Route{Action: action}, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		executed++
		writer.WriteHeader(http.StatusNoContent)
	}))
	if err != nil {
		t.Fatal(err)
	}
	call := func(token string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/operations/authorization/action-usages/query", nil)
		if token != "" {
			request.Header.Set("Authorization", "Bearer "+token)
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		return response
	}
	if response := call(""); response.Code != http.StatusUnauthorized || executed != 0 || verifier.calls != 0 {
		t.Fatalf("missing service token status=%d executed=%d calls=%d", response.Code, executed, verifier.calls)
	}
	if response := call("wrong"); response.Code != http.StatusUnauthorized || executed != 0 || verifier.calls != 1 {
		t.Fatalf("invalid service token status=%d executed=%d calls=%d", response.Code, executed, verifier.calls)
	}
	if response := call("valid-service-token"); response.Code != http.StatusNoContent || executed != 1 || verifier.calls != 2 {
		t.Fatalf("valid service token status=%d executed=%d calls=%d", response.Code, executed, verifier.calls)
	}
	wantGrant := identitysdk.ApplicationServiceGrant{Resource: "runtime.authorization.action_usages", Action: "query"}
	if verifier.request.Audience != "domainry-runtime" || verifier.request.Grant != wantGrant {
		t.Fatalf("verification request=%+v", verifier.request)
	}
	action.Authorization.Audiences = []string{"other-runtime"}
	if _, err := guard(modulehttp.Route{Action: action}, http.NotFoundHandler()); err == nil {
		t.Fatal("service Action with another audience was mounted")
	}
}

var _ identitysdk.ApplicationServiceVerificationBinding = moduleServiceBindingStub{}
