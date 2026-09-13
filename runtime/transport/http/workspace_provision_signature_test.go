package http

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	application "github.com/domainry/domainry-runtime/runtime/application/workspaceprovision"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	model "github.com/domainry/domainry-runtime/runtime/domain/workspaceprovision/model"
	transport "github.com/domainry/domainry-runtime/runtime/transport/http/workspaceprovision"
	signature "github.com/domainry/domainry-scheduler-sdk/dispatchgateway"
)

const provisionSigningTestSecret = "provisioning-test-secret-32-bytes-long"
const provisionSigningTestBody = `{"request_id":"create-1","workspace_code":"shop-a","workspace_name":"Shop A","first_store_code":"main","first_store_name":"Main","admin_login_id":"owner@example.test","admin_name":"Owner","commercial_configuration":{"plan":"standard","included_user_limit":1,"max_user_limit":10,"included_customer_limit":0,"max_customer_limit":100,"included_store_limit":1,"max_stores":2,"contract_date":"2026-09-13","billing_day":1}}`

type signedProvisionRepository struct{ calls int }

func (p *signedProvisionRepository) Provision(_ context.Context, request model.Request) (model.Result, error) {
	p.calls++
	return model.Result{CanonicalCode: request.WorkspaceCode}, nil
}

func signedProvisionTestRequest(t *testing.T, signedAt time.Time) *http.Request {
	t.Helper()
	r := httptest.NewRequest("POST", "/workspaces", strings.NewReader(provisionSigningTestBody))
	timestamp := strconv.FormatInt(signedAt.Unix(), 10)
	value, err := signature.SignRequest([]byte(provisionSigningTestBody), signature.SignedRequest{Method: "POST", Path: "/workspaces", RuntimeID: "runtime-a", IdempotencyKey: "create-1"}, "superk", timestamp, []byte(provisionSigningTestSecret))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set(signature.SignatureHeader, value)
	r.Header.Set(signature.SignatureVersionHeader, signature.CallbackSignatureContractVersion)
	r.Header.Set(signature.ClientIDHeader, "superk")
	r.Header.Set(signature.TimestampHeader, timestamp)
	r.Header.Set(signature.RuntimeIDHeader, "runtime-a")
	r.Header.Set("Idempotency-Key", "create-1")
	return r
}

func provisionSignatureTestRouter(t *testing.T) (*HTTPRouter, *signedProvisionRepository) {
	t.Helper()
	router := completeRouterForListenerGroupTests(HTTPRouterConfig{WorkspaceProvisionClientID: "superk", WorkspaceProvisionSigningSecret: provisionSigningTestSecret, MaxJSONBodyBytes: 2 << 20})
	router.runtimeInstanceID = "runtime-a"
	router.identityAuthentication = routerIdentityMiddlewareStub{invalid: true}
	router.identityPrincipal = principalmodel.NewPrincipalFromIdentity
	repository := &signedProvisionRepository{}
	callbacks := router.HandlerCallbacks()
	router.workspaceProvisionHTTP = transport.NewWorkspaceProvisionHandler(transport.WorkspaceProvisionDependencies{
		UseCases: application.NewWorkspaceProvisionApplicationService(repository), Principal: callbacks.Principal,
		DecodeJSON: callbacks.DecodeJSON, WriteJSON: callbacks.WriteJSON, WriteServiceError: callbacks.WriteServiceError,
		SecurityAudit: callbacks.SecurityAudit,
	})
	return router, repository
}

func TestWorkspaceProvisionExistingRouteAcceptsSignatureAndRetainsAdministratorAuthorization(t *testing.T) {
	router, repository := provisionSignatureTestRouter(t)
	for _, group := range []ListenerRouteGroup{ListenerRouteGroupManagement, ListenerRouteGroupOps} {
		response := httptest.NewRecorder()
		router.RoutesForListenerGroup(group).ServeHTTP(response, signedProvisionTestRequest(t, time.Now()))
		if response.Code != 201 {
			t.Fatalf("listener=%s status=%d body=%s", group, response.Code, response.Body.String())
		}
	}
	if repository.calls != 2 {
		t.Fatalf("calls=%d", repository.calls)
	}
	response := httptest.NewRecorder()
	router.RoutesForListenerGroup(ListenerRouteGroupPublic).ServeHTTP(response, signedProvisionTestRequest(t, time.Now()))
	if response.Code != 404 || repository.calls != 2 {
		t.Fatalf("public status=%d calls=%d", response.Code, repository.calls)
	}

	previous := principalmodel.InstallationWorkspaceID
	principalmodel.InstallationWorkspaceID = "installation"
	t.Cleanup(func() { principalmodel.InstallationWorkspaceID = previous })
	principal := routerTestPrincipal(identitysdk.Principal{Known: true, WorkspaceID: "installation", UserID: "admin", RoleKey: "tenant_admin"}, application.ProvisionActionKey)
	principal.AccessBundle = &identitysdk.AccessBundle{
		ContractVersion: identitysdk.CurrentPolicyBundleVersion,
		FunctionGrants:  []identitysdk.FunctionGrant{{Resource: "runtime.workspaceprovision", Action: "provision_workspace", Effect: identitysdk.EffectAllow}},
		DataPolicies:    []identitysdk.DataPolicy{{Key: "provision", Resource: "runtime.workspaceprovision", Action: "provision_workspace", Effect: identitysdk.EffectAllow}},
	}
	router.identityAuthentication = routerIdentityMiddlewareStub{principal: principal.Principal}
	router.identityPrincipal = principalmodel.NewPrincipalFromIdentity
	response = httptest.NewRecorder()
	adminRequest := httptest.NewRequest("POST", "/workspaces", strings.NewReader(provisionSigningTestBody))
	adminRequest.Header.Set("Authorization", "Bearer administrator")
	router.Routes().ServeHTTP(response, adminRequest)
	if response.Code != 201 || repository.calls != 3 {
		t.Fatalf("administrator status=%d calls=%d body=%s", response.Code, repository.calls, response.Body.String())
	}
}

func TestWorkspaceProvisionSignatureCannotBypassOtherRoutesOrAcceptTampering(t *testing.T) {
	tests := []struct {
		name   string
		change func(*HTTPRouter, *http.Request)
	}{
		{"unsigned", func(_ *HTTPRouter, r *http.Request) { r.Header = http.Header{} }},
		{"missing signature", func(_ *HTTPRouter, r *http.Request) { r.Header.Del(signature.SignatureHeader) }},
		{"wrong key", func(s *HTTPRouter, _ *http.Request) {
			s.workspaceProvisionSigningSecret = []byte(strings.Repeat("x", 32))
		}},
		{"disabled", func(s *HTTPRouter, _ *http.Request) { s.workspaceProvisionSigningSecret = nil }},
		{"wrong runtime", func(_ *HTTPRouter, r *http.Request) { r.Header.Set(signature.RuntimeIDHeader, "runtime-b") }},
		{"wrong caller", func(_ *HTTPRouter, r *http.Request) { r.Header.Set(signature.ClientIDHeader, "scheduler") }},
		{"wrong key header", func(_ *HTTPRouter, r *http.Request) { r.Header.Set("Idempotency-Key", "other") }},
		{"body changed", func(_ *HTTPRouter, r *http.Request) {
			r.Body = io.NopCloser(strings.NewReader(strings.ReplaceAll(provisionSigningTestBody, "Shop A", "Shop B")))
		}},
		{"duplicate header", func(_ *HTTPRouter, r *http.Request) {
			r.Header.Add(signature.SignatureHeader, r.Header.Get(signature.SignatureHeader))
		}},
		{"query", func(_ *HTTPRouter, r *http.Request) { r.URL.RawQuery = "workspace_id=other" }},
		{"mixed authentication", func(_ *HTTPRouter, r *http.Request) { r.Header.Set("Authorization", "Bearer anything") }},
		{"expired", func(_ *HTTPRouter, r *http.Request) {
			*r = *signedProvisionTestRequest(t, time.Now().Add(-6*time.Minute))
		}},
		{"future", func(_ *HTTPRouter, r *http.Request) {
			*r = *signedProvisionTestRequest(t, time.Now().Add(6*time.Minute))
		}},
		{"catalog", func(_ *HTTPRouter, r *http.Request) { r.Method = "GET" }},
		{"suspend", func(_ *HTTPRouter, r *http.Request) { r.URL.Path = "/workspaces/shop-a/suspend" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router, repository := provisionSignatureTestRouter(t)
			r := signedProvisionTestRequest(t, time.Now())
			tt.change(router, r)
			response := httptest.NewRecorder()
			router.Routes().ServeHTTP(response, r)
			if response.Code < 400 || response.Code >= 500 || repository.calls != 0 {
				t.Fatalf("status=%d calls=%d body=%s", response.Code, repository.calls, response.Body.String())
			}
		})
	}
}

func TestSignedWorkspaceProvisionStillValidatesExistingRequest(t *testing.T) {
	router, repository := provisionSignatureTestRouter(t)
	r := signedProvisionTestRequest(t, time.Now())
	var value map[string]any
	_ = json.Unmarshal([]byte(provisionSigningTestBody), &value)
	value["workspace_code"] = "default"
	body, _ := json.Marshal(value)
	r.Body = io.NopCloser(strings.NewReader(string(body)))
	sig, err := signature.SignRequest(body, signature.SignedRequest{Method: "POST", Path: "/workspaces", RuntimeID: "runtime-a", IdempotencyKey: "create-1"}, "superk", r.Header.Get(signature.TimestampHeader), []byte(provisionSigningTestSecret))
	if err != nil {
		t.Fatal(err)
	}
	r.Header.Set(signature.SignatureHeader, sig)
	w := httptest.NewRecorder()
	router.Routes().ServeHTTP(w, r)
	if w.Code != 400 || repository.calls != 0 {
		t.Fatalf("status=%d calls=%d body=%s", w.Code, repository.calls, w.Body.String())
	}
}
