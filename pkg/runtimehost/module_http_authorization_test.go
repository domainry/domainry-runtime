package runtimehost

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	actioncontract "github.com/domainry/domainry-foundation/action"
	"github.com/domainry/domainry-foundation/modulehttp"
	"github.com/domainry/domainry-foundation/requestcontext"
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
	services  identitysdk.ApplicationServiceTokenVerifier
	principal identitysdk.Principal
}

func (moduleServiceBindingStub) Descriptor() identitysdk.Descriptor {
	return identitysdk.Descriptor{Mode: identitysdk.DeploymentModeSaaS, Audience: "domainry-runtime"}
}

func (stub moduleServiceBindingStub) ApplicationServiceVerifier() identitysdk.ApplicationServiceTokenVerifier {
	return stub.services
}

func (stub moduleServiceBindingStub) PrincipalAuthenticator() identitysdk.PrincipalAuthenticator {
	return modulePrincipalAuthenticatorStub{principal: stub.principal}
}

type modulePrincipalAuthenticatorStub struct{ principal identitysdk.Principal }

func (stub modulePrincipalAuthenticatorStub) Authenticate(context.Context, string) (identitysdk.Principal, error) {
	return stub.principal, nil
}

type moduleAuditRecorderStub struct {
	events []modulehttp.AuditEvent
	err    error
}

func (stub *moduleAuditRecorderStub) Record(_ context.Context, event modulehttp.AuditEvent) error {
	stub.events = append(stub.events, event)
	return stub.err
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
	action := runtimeHostTestAction("runtime.action.permission_usages.query", "POST /action/permission-usages/query", []actioncontract.Exposure{actioncontract.ExposureManagement}, actioncontract.AuthorizationSigned)
	action.Authorization = actioncontract.Authorization{
		Strategy: actioncontract.AuthorizationSigned, PolicyKey: "runtime.action.permission_usages.query", Audiences: []string{"domainry-runtime"},
	}
	executed := 0
	handler, err := guard(modulehttp.Route{Action: action}, http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		executed++
		writer.WriteHeader(http.StatusNoContent)
	}), nil)
	if err != nil {
		t.Fatal(err)
	}
	call := func(token string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/action/permission-usages/query", nil)
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
	wantGrant := identitysdk.ApplicationServiceGrant{Resource: "runtime.action.permission_usages", Action: "query"}
	if verifier.request.Audience != "domainry-runtime" || verifier.request.Grant != wantGrant {
		t.Fatalf("verification request=%+v", verifier.request)
	}
	action.Authorization.Audiences = []string{"other-runtime"}
	if _, err := guard(modulehttp.Route{Action: action}, http.NotFoundHandler(), nil); err == nil {
		t.Fatal("service Action with another audience was mounted")
	}
}

var _ identitysdk.ApplicationServiceVerificationBinding = moduleServiceBindingStub{}

func TestModuleHTTPPermissionDenialUsesSourceOwnedAuditRecorder(t *testing.T) {
	principal := identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "coach-1", RoleKey: "coach"}
	guard, err := newModuleHTTPRouteGuard(moduleServiceBindingStub{principal: principal})
	if err != nil {
		t.Fatal(err)
	}
	action := runtimeHostTestAction("identity.user_role_assignments.assign", "POST /identity/users/{userID}/role-assignments", []actioncontract.Exposure{actioncontract.ExposureManagement}, actioncontract.AuthorizationAuthenticated)
	recorder := &moduleAuditRecorderStub{}
	handler, err := guard(modulehttp.Route{Action: action}, http.NotFoundHandler(), recorder)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/identity/users/target-1/role-assignments", strings.NewReader(`{"role_id":"private-role"}`))
	request.Header.Set("Authorization", "Bearer known-token")
	request.SetPathValue("userID", "target-1")
	request = request.WithContext(requestcontext.WithCorrelationID(requestcontext.WithRequestID(request.Context(), "request-1"), "correlation-1"))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden || len(recorder.events) != 1 {
		t.Fatalf("status=%d events=%#v body=%s", response.Code, recorder.events, response.Body.String())
	}
	event := recorder.events[0]
	if event.Event != "auth_api_denied" || event.ObjectKey != "identity.user_role_assignments" || event.RecordID != "target-1" || event.WorkspaceID != "workspace-a" || event.ActorID != "coach-1" || event.RoleKey != "coach" {
		t.Fatalf("denial audit=%#v", event)
	}
	if event.Metadata["result"] != "denied" || event.Metadata["reason"] != "permission_denied" || event.Metadata["error_code"] != "auth.permission_denied" || event.Metadata["request_id"] != "request-1" || event.Metadata["correlation_id"] != "correlation-1" {
		t.Fatalf("denial metadata=%#v", event.Metadata)
	}
	encoded, _ := json.Marshal(event)
	if strings.Contains(string(encoded), "private-role") || strings.Contains(string(encoded), "known-token") {
		t.Fatalf("denial audit leaked request credentials or body: %s", encoded)
	}

	recorder.err = errors.New("audit unavailable")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), "backend.audit.append_failed") {
		t.Fatalf("audit failure status=%d body=%s", response.Code, response.Body.String())
	}
}
