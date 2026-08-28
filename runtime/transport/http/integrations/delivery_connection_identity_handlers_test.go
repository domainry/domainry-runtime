package integrations_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	integrationapplication "github.com/domainry/domainry-runtime/runtime/application/integration"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	integrationhttp "github.com/domainry/domainry-runtime/runtime/transport/http/integrations"
)

func TestIntegrationConnectionHTTP(t *testing.T) {
	store, application := newIntegrationManagementHTTPApplication(t)
	defer store.Close()
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1", UserID: "admin"}}, accessfixture.Bundle{Key: "admin", Permissions: []string{"workspace.admin", "*"}})
	call := integrationManagementHTTPCall(application, &principal)

	catalog := call(http.MethodGet, "/tenant-admin/integrations/connectors", "", nil)
	if catalog.Code != http.StatusOK || catalog.Header().Get("ETag") == "" || catalog.Header().Get("X-Connector-Contract-Version") == "" || responseCount(t, catalog, "count") != 1 {
		t.Fatalf("catalog status=%d headers=%v body=%s", catalog.Code, catalog.Header(), catalog.Body.String())
	}
	if response := call(http.MethodGet, "/tenant-admin/integrations/connections", "", nil); response.Code != http.StatusOK || responseCount(t, response, "count") != 1 {
		t.Fatalf("list status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodGet, "/tenant-admin/integrations/connections/webhook-connection", "", nil); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"key":"webhook-connection"`) || response.Header().Get("X-Resource-Hash") == "" {
		t.Fatalf("get status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodGet, "/tenant-admin/integrations/connections/webhook-connection/versions", "", nil); response.Code != http.StatusOK || responseCount(t, response, "count") != 1 {
		t.Fatalf("versions status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/tenant-admin/integrations/connections/webhook-connection/validate", `{"connector_key":"webhook","provider_key":"probe","status":"configured","config":{"url":"https://example.invalid"}}`, nil); response.Code != http.StatusOK {
		t.Fatalf("validate status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/tenant-admin/integrations/connections/webhook-connection/validate", `{`, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid validate status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/tenant-admin/integrations/connections/webhook-connection/validate", `{}`, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("missing validate fields status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPut, "/tenant-admin/integrations/connections/disposable", `{`, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid upsert status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPut, "/tenant-admin/integrations/connections/disposable", `{}`, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("missing connector status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPut, "/tenant-admin/integrations/connections/disposable", `{"connector_key":"webhook","provider_key":"probe","name":"Disposable","status":"configured","config":{"url":"https://example.invalid"}}`, nil); response.Code != http.StatusOK {
		t.Fatalf("upsert status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodDelete, "/tenant-admin/integrations/connections/disposable", "", nil); response.Code != http.StatusNoContent {
		t.Fatalf("delete status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/tenant-admin/integrations/connections/webhook-connection/test-operation", `{`, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid test status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/tenant-admin/integrations/connections/webhook-connection/test-operation", `{"operation":"ping"}`, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("unconfirmed test status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/tenant-admin/integrations/connections/webhook-connection/test-operation", `{"operation":"ping","confirm":true}`, nil); response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"ok":true`) {
		t.Fatalf("test status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/tenant-admin/integrations/connections/webhook-connection/rotate", `{`, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid rotate status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/tenant-admin/integrations/connections/webhook-connection/rotate", `{"connector_key":"webhook","provider_key":"probe","name":"Rotated","secret_refs":{"token":"env:ROTATED_TOKEN"}}`, nil); response.Code != http.StatusOK {
		t.Fatalf("rotate status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/tenant-admin/integrations/connections/webhook-connection/disable", "", nil); response.Code != http.StatusOK {
		t.Fatalf("disable status=%d body=%s", response.Code, response.Body.String())
	}
	for _, request := range []struct{ method, path, body string }{
		{http.MethodGet, "/tenant-admin/integrations/connections/missing", ""},
		{http.MethodGet, "/tenant-admin/integrations/connections/missing/versions", ""},
		{http.MethodPost, "/tenant-admin/integrations/connections/missing/disable", ""},
		{http.MethodDelete, "/tenant-admin/integrations/connections/missing", ""},
		{http.MethodPost, "/tenant-admin/integrations/connections/missing/rotate", `{}`},
	} {
		if response := call(request.method, request.path, request.body, nil); response.Code != http.StatusNotFound {
			t.Fatalf("%s status=%d body=%s", request.path, response.Code, response.Body.String())
		}
	}
	accessfixture.Set(&principal, accessfixture.Bundle{})
	for _, path := range []string{"/tenant-admin/integrations/connectors", "/tenant-admin/integrations/connections"} {
		if response := call(http.MethodGet, path, "", nil); response.Code != http.StatusForbidden {
			t.Fatalf("%s permission status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
}

func TestIntegrationInvocationAndOutboxHTTP(t *testing.T) {
	store, application := newIntegrationManagementHTTPApplication(t)
	defer store.Close()
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1", UserID: "operator"}, RequestID: "request-1"}, accessfixture.Bundle{Key: "operator", Permissions: []string{
		integrationapplication.PermissionInvoke, integrationapplication.PermissionRetry, integrationapplication.PermissionAuditView,
	}})
	call := integrationManagementHTTPCall(application, &principal)

	if response := call(http.MethodPost, "/integrations/invocations", `{`, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid invocation status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/integrations/invocations", `{}`, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("missing invocation status=%d body=%s", response.Code, response.Body.String())
	}
	created := call(http.MethodPost, "/integrations/invocations", `{"connector_key":"webhook","provider_key":"probe","connection_key":"webhook-connection","operation":"ping","status":"running","record_id":"record-1","metadata":{"provider":"probe","external_principal":"probe:user-1"}}`, nil)
	var invocation integrationmodel.IntegrationInvocation
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &invocation) != nil || invocation.ID == "" {
		t.Fatalf("create invocation status=%d value=%+v body=%s", created.Code, invocation, created.Body.String())
	}
	if response := call(http.MethodGet, "/integrations/invocations?connector_key=webhook&record_id=record-1&status=running&provider=probe&external_principal=probe:user-1&limit=200", "", nil); response.Code != http.StatusOK || responseCount(t, response, "count") != 1 {
		t.Fatalf("list invocation status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodGet, "/integrations/invocations?limit=201", "", nil); response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "backend.integration.query_limit_invalid") {
		t.Fatalf("invalid invocation limit status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/integrations/invocations/"+invocation.ID+"/status", `{`, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid invocation update status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/integrations/invocations/"+invocation.ID+"/status", `{"status":"unknown"}`, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("unknown invocation status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/integrations/invocations/"+invocation.ID+"/status", `{"status":"succeeded","duration_ms":12,"response_ref":"response-1"}`, nil); response.Code != http.StatusOK {
		t.Fatalf("update invocation status=%d body=%s", response.Code, response.Body.String())
	}

	if response := call(http.MethodPost, "/integrations/outbox", `{`, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid outbox status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/integrations/outbox", `{}`, map[string]string{"Idempotency-Key": "missing-identity"}); response.Code != http.StatusBadRequest {
		t.Fatalf("missing outbox identity status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/integrations/outbox", `{"connector_key":"webhook","operation":"ping","request_ref":"body-key"}`, map[string]string{"Idempotency-Key": "header-key"}); response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "backend.idempotency.key_mismatch") {
		t.Fatalf("mismatch status=%d body=%s", response.Code, response.Body.String())
	}
	outboxCreated := call(http.MethodPost, "/integrations/outbox", `{"connector_key":"webhook","connection_key":"webhook-connection","operation":"ping","payload":{"value":1}}`, map[string]string{"Idempotency-Key": "outbox-1"})
	var message integrationmodel.IntegrationOutboxMessage
	if outboxCreated.Code != http.StatusCreated || json.Unmarshal(outboxCreated.Body.Bytes(), &message) != nil || message.ID == "" || message.RequestRef != "outbox-1" || message.DedupKey != "outbox-1" {
		t.Fatalf("create outbox status=%d value=%+v body=%s", outboxCreated.Code, message, outboxCreated.Body.String())
	}
	if response := call(http.MethodGet, "/business/integration-intents/"+message.ID, "", nil); response.Code != http.StatusOK ||
		!strings.Contains(response.Body.String(), `"status":"queued"`) || strings.Contains(response.Body.String(), `"payload"`) {
		t.Fatalf("business intent status=%d body=%s", response.Code, response.Body.String())
	}
	principal.UserID = "different-user"
	if response := call(http.MethodGet, "/business/integration-intents/"+message.ID, "", nil); response.Code != http.StatusNotFound {
		t.Fatalf("foreign business intent status=%d body=%s", response.Code, response.Body.String())
	}
	principal.UserID = "operator"
	for _, request := range []struct {
		body    string
		headers map[string]string
	}{
		{body: `{"connector_key":"webhook","connection_key":"webhook-connection","operation":"ping","request_ref":"body-only","dedup_key":"dedup-only","payload":{"value":2}}`},
		{body: `{"connector_key":"webhook","connection_key":"webhook-connection","operation":"ping","request_ref":"same-key","dedup_key":"dedup-same","payload":{"value":3}}`, headers: map[string]string{"Idempotency-Key": "same-key"}},
	} {
		if response := call(http.MethodPost, "/integrations/outbox", request.body, request.headers); response.Code != http.StatusCreated {
			t.Fatalf("explicit outbox identity status=%d body=%s", response.Code, response.Body.String())
		}
	}
	if response := call(http.MethodGet, "/integrations/outbox?connector_key=webhook&status=queued&limit=200", "", nil); response.Code != http.StatusOK || responseCount(t, response, "count") != 3 {
		t.Fatalf("list outbox status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodGet, "/integrations/outbox?limit=201", "", nil); response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "backend.integration.query_limit_invalid") {
		t.Fatalf("invalid outbox limit status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/integrations/outbox/"+message.ID+"/status", `{`, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid outbox update status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/integrations/outbox/"+message.ID+"/status", `{"status":"unknown"}`, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("unknown outbox status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/integrations/outbox/"+message.ID+"/status", `{"status":"failed","error":"temporary"}`, nil); response.Code != http.StatusOK {
		t.Fatalf("fail outbox status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/integrations/outbox/"+message.ID+"/retry", `{`, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid retry status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/integrations/outbox/"+message.ID+"/retry", `{"error":"retry"}`, map[string]string{"Idempotency-Key": "retry-1"}); response.Code != http.StatusOK {
		t.Fatalf("retry status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/integrations/outbox/process-due?limit=999", "", nil); response.Code != http.StatusOK {
		t.Fatalf("process outbox status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/integrations/outbox/missing/retry", `{}`, map[string]string{"Idempotency-Key": "missing-retry"}); response.Code != http.StatusNotFound {
		t.Fatalf("missing retry status=%d body=%s", response.Code, response.Body.String())
	}

	accessfixture.Set(&principal, accessfixture.Bundle{})
	for _, request := range []struct{ method, path, body string }{
		{http.MethodGet, "/integrations/invocations", ""},
		{http.MethodGet, "/integrations/outbox", ""},
		{http.MethodPost, "/integrations/outbox/process-due", ""},
	} {
		if response := call(request.method, request.path, request.body, nil); response.Code != http.StatusForbidden {
			t.Fatalf("%s permission status=%d body=%s", request.path, response.Code, response.Body.String())
		}
	}
}

func TestIntegrationExternalIdentityHTTP(t *testing.T) {
	store, application := newIntegrationManagementHTTPApplication(t)
	defer store.Close()
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1", UserID: "admin"}}, accessfixture.Bundle{Key: "admin", Permissions: []string{"workspace.admin", "*"}})
	call := integrationManagementHTTPCall(application, &principal)

	if response := call(http.MethodPut, "/tenant-admin/integrations/external-identities/probe-user", `{`, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid upsert status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPut, "/tenant-admin/integrations/external-identities/probe-user", `{}`, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("missing subject status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPut, "/tenant-admin/integrations/external-identities/probe-user", `{"provider":"probe","external_subject":"user-1","actor_id":"service-user","role_key":"integration-client"}`, nil); response.Code != http.StatusOK {
		t.Fatalf("upsert status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodGet, "/tenant-admin/integrations/external-identities", "", nil); response.Code != http.StatusOK || responseCount(t, response, "count") != 1 {
		t.Fatalf("list status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/tenant-admin/integrations/external-identities/resolve", `{`, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid resolve status=%d body=%s", response.Code, response.Body.String())
	}
	resolved := call(http.MethodPost, "/tenant-admin/integrations/external-identities/resolve", `{"provider":"probe","external_subject":"user-1","external_name":"Probe User"}`, nil)
	var result integrationmodel.IntegrationExternalIdentityResolveResult
	if resolved.Code != http.StatusOK || json.Unmarshal(resolved.Body.Bytes(), &result) != nil || !result.Mapped || result.ActorID != "service-user" {
		t.Fatalf("resolve status=%d value=%+v body=%s", resolved.Code, result, resolved.Body.String())
	}
	if response := call(http.MethodPost, "/tenant-admin/integrations/external-identities/probe-user/disable", "", nil); response.Code != http.StatusOK {
		t.Fatalf("disable status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/tenant-admin/integrations/external-identities/resolve", `{"provider":"probe","external_subject":"user-1"}`, nil); response.Code != http.StatusForbidden {
		t.Fatalf("disabled resolve status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/tenant-admin/integrations/external-identities/missing/disable", "", nil); response.Code != http.StatusNotFound {
		t.Fatalf("missing disable status=%d body=%s", response.Code, response.Body.String())
	}
	accessfixture.Set(&principal, accessfixture.Bundle{})
	if response := call(http.MethodGet, "/tenant-admin/integrations/external-identities", "", nil); response.Code != http.StatusForbidden {
		t.Fatalf("permission status=%d body=%s", response.Code, response.Body.String())
	}
}

func integrationManagementHTTPCall(application *integrationapplication.IntegrationApplicationService, principal *principalmodel.Principal) func(string, string, string, map[string]string) *httptest.ResponseRecorder {
	handler := integrationEventHTTPHandler(application, principal)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	integrationhttp.RegisterInternalWorkerRoutesForTest(handler, mux)
	return func(method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		return response
	}
}
