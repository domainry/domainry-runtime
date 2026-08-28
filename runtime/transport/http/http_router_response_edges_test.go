package http

import (
	"context"
	"encoding/json"
	operationscontract "github.com/domainry/domainry-runtime/runtime/domain/operations/contract"
	"net/http"
	"net/http/httptest"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/requestcontext"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestPrincipalContextIgnoresUntrustedScopeHeadersAndUsesRequestSources(t *testing.T) {
	base := httptest.NewRequest(http.MethodGet, "/workspaces/path-workspace/records?workspace_id=query-workspace", nil)
	base.SetPathValue("workspaceID", "path-workspace")
	base.Header.Set("X-Workspace-ID", " header-workspace ")
	base.Header.Set("X-Team-IDs", " team-1,team-2,team-1, ")
	base.Header.Set("X-Store-IDs", "store-1")
	base.Header.Set("X-Territory-IDs", "territory-1")
	base.Header.Set("X-Warehouse-IDs", "warehouse-1")
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "user-1", WorkspaceID: "workspace-original", OrganizationScopes: identitysdk.OrganizationScopes{TeamIDs: []string{"trusted-team"}, StoreIDs: []string{"trusted-store"},
		TerritoryIDs: []string{"trusted-territory"}, WarehouseIDs: []string{"trusted-warehouse"}}}}
	request := requestWithPrincipal(base, principal)
	fromContext, ok := principalFromContext(request)
	if !ok || fromContext.UserID != "user-1" || requestcontext.WorkspaceID(request.Context()) != "workspace-original" || requestcontext.ActorID(request.Context()) != "user-1" {
		t.Fatalf("context principal=%+v ok=%v", fromContext, ok)
	}
	router := &HTTPRouter{}
	resolved := router.principalFromRequest(request)
	if !resolved.Known || resolved.WorkspaceID != "header-workspace" ||
		len(resolved.OrganizationScopes.TeamIDs) != 1 || resolved.OrganizationScopes.TeamIDs[0] != "trusted-team" ||
		len(resolved.OrganizationScopes.StoreIDs) != 1 || resolved.OrganizationScopes.StoreIDs[0] != "trusted-store" ||
		len(resolved.OrganizationScopes.TerritoryIDs) != 1 || resolved.OrganizationScopes.TerritoryIDs[0] != "trusted-territory" ||
		len(resolved.OrganizationScopes.WarehouseIDs) != 1 || resolved.OrganizationScopes.WarehouseIDs[0] != "trusted-warehouse" {
		t.Fatalf("resolved principal=%+v", resolved)
	}
	if router.actorIDFromRequest(request) != "user-1" {
		t.Fatal("context actor was not used")
	}
	unknownRequest := requestWithPrincipal(base, principalmodel.Principal{Principal: identitysdk.Principal{Known: false}})
	if _, ok := principalFromContext(unknownRequest); ok {
		t.Fatal("unknown principal reported as authenticated")
	}

	if explicitWorkspaceIDFromRequest(base) != "header-workspace" || workspaceIDFromRequest(base) != "header-workspace" {
		t.Fatal("workspace header precedence failed")
	}
	base.Header.Del("X-Workspace-ID")
	if explicitWorkspaceIDFromRequest(base) != "query-workspace" {
		t.Fatal("workspace query fallback failed")
	}
	base.URL.RawQuery = ""
	if explicitWorkspaceIDFromRequest(base) != "path-workspace" {
		t.Fatal("workspace path fallback failed")
	}
	empty := httptest.NewRequest(http.MethodGet, "/", nil)
	if workspaceIDFromRequest(empty) != "default" || explicitWorkspaceIDFromRequest(empty) != "" {
		t.Fatal("default workspace projection failed")
	}
	if authenticatedWorkspaceMatchesRequest(principalmodel.Principal{}, empty) || !authenticatedWorkspaceMatchesRequest(principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-1"}}, empty) {
		t.Fatal("workspace authentication default mismatch")
	}
	empty.Header.Set("X-Workspace-ID", "workspace-2")
	if authenticatedWorkspaceMatchesRequest(principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-1"}}, empty) || !authenticatedWorkspaceMatchesRequest(principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-2"}}, empty) {
		t.Fatal("workspace authentication target mismatch")
	}
}

func TestAuthHeaderTokenAndActorProjectionEdges(t *testing.T) {
	tests := []struct {
		authorization string
		bearer        string
		apiKey        string
	}{
		{authorization: "Bearer token", bearer: "token"},
		{authorization: " bearer   token ", bearer: "token"},
		{authorization: "Basic token"},
		{authorization: "Bearer"},
		{authorization: "Bearer vapi_token", bearer: "vapi_token", apiKey: "vapi_token"},
	}
	for _, test := range tests {
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.Header.Set("Authorization", test.authorization)
		if bearerTokenFromRequest(request) != test.bearer || apiKeyTokenFromRequest(request) != test.apiKey {
			t.Fatalf("authorization=%q bearer=%q api=%q", test.authorization, bearerTokenFromRequest(request), apiKeyTokenFromRequest(request))
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("X-API-Key", " explicit-key ")
	request.Header.Set("Authorization", "Bearer vapi_other")
	if apiKeyTokenFromRequest(request) != "explicit-key" || (&HTTPRouter{}).actorIDFromRequest(request) != "integration:api_key" {
		t.Fatal("explicit API key precedence failed")
	}
	request = httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("X-User-ID", " user-2 ")
	if !devAuthHeadersPresent(request) || (&HTTPRouter{}).actorIDFromRequest(request) != "user-2" {
		t.Fatal("development actor projection failed")
	}
	if devAuthHeadersPresent(httptest.NewRequest(http.MethodGet, "/", nil)) {
		t.Fatal("empty development auth header accepted")
	}
}

func TestCloneAndAuditPrincipalContext(t *testing.T) {
	original := map[string]any{"key": "value"}
	cloned := cloneStringAnyMap(original)
	cloned["key"] = "changed"
	if original["key"] != "value" || cloneStringAnyMap(nil) != nil {
		t.Fatal("metadata map clone was not isolated")
	}
	router := &HTTPRouter{}
	request := requestWithPrincipal(httptest.NewRequest(http.MethodGet, "/", nil), principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "auditor", WorkspaceID: "workspace-1"}})
	request.Header.Set(requestIDHeader, "request-1")
	principal := router.auditPrincipalFromRequest(request)
	if principal.UserID != "auditor" || principal.RequestID != "request-1" {
		t.Fatalf("audit principal=%+v", principal)
	}
	request = httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("X-User-ID", "anonymous-user")
	principal = router.auditPrincipalFromRequest(request)
	if principal.Known || principal.UserID != "anonymous-user" || principal.WorkspaceID != "default" || principal.RequestID == "" {
		t.Fatalf("anonymous audit principal=%+v", principal)
	}
}

func TestWriteServiceErrorKindAndResponseParameterMatrix(t *testing.T) {
	for _, test := range []struct {
		kind   apperror.ErrorKind
		status int
	}{
		{kind: apperror.KindBadRequest, status: http.StatusBadRequest},
		{kind: apperror.KindForbidden, status: http.StatusForbidden},
		{kind: apperror.KindNotFound, status: http.StatusNotFound},
		{kind: apperror.KindConflict, status: http.StatusConflict},
		{kind: apperror.KindRateLimited, status: http.StatusTooManyRequests},
		{kind: apperror.KindUnavailable, status: http.StatusServiceUnavailable},
		{kind: apperror.KindInternal, status: http.StatusInternalServerError},
	} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/", nil)
		request.Header.Set("X-Request-ID", "req-test")
		err := &apperror.AppError{Kind: test.kind, Code: "backend.test", Params: map[string]string{"field": "name"}}
		writeServiceError(response, request, err)
		var body map[string]any
		if json.Unmarshal(response.Body.Bytes(), &body) != nil || response.Code != test.status ||
			body["code"] != "backend.test" || body["message"] != "backend.test" ||
			body["request_id"] != "req-test" {
			t.Fatalf("kind=%s status=%d body=%v", test.kind, response.Code, body)
		}
	}
	response := httptest.NewRecorder()
	writeError(response, httptest.NewRequest(http.MethodGet, "/", nil), http.StatusBadRequest, " ", " field ", "name", "", "ignored", "dangling")
	var body map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || body["code"] != "backend.request_failed" {
		t.Fatalf("default error body=%v err=%v", body, err)
	}
	params := responseParams(" field ", "name", "", "ignored", "dangling")
	if len(params) != 1 || params["field"] != "name" || responseParams() != nil {
		t.Fatalf("params=%v", params)
	}
	response = httptest.NewRecorder()
	writeServiceError(response, httptest.NewRequest(http.MethodGet, "/", nil), context.Canceled)
	if response.Code != 499 {
		t.Fatalf("cancel status=%d", response.Code)
	}
	response = httptest.NewRecorder()
	writeServiceError(response, httptest.NewRequest(http.MethodGet, "/", nil), apperror.New(apperror.KindForbidden, "backend.permission.denied", nil, nil))
	body = map[string]any{}
	if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != http.StatusForbidden || body["code"] != "auth.permission_denied" {
		t.Fatalf("public permission alias status=%d body=%v err=%v", response.Code, body, err)
	}
}

func TestRuntimeAuthoringServiceErrorSemanticsMatrix(t *testing.T) {
	if got := runtimeAuthoringSemantics(http.StatusConflict, "backend.change_plan.concurrent_write"); got.Class != "conflict" || got.Retryable {
		t.Fatalf("conflict semantics=%#v", got)
	}
	if got := runtimeAuthoringSemantics(http.StatusTooManyRequests, "backend.rate_limited"); got.Class != "transient" || !got.Retryable {
		t.Fatalf("rate-limit semantics=%#v", got)
	}
	tests := []struct {
		name      string
		kind      apperror.ErrorKind
		code      string
		status    int
		class     string
		action    string
		retryable bool
	}{
		{name: "protocol", kind: apperror.KindBadRequest, code: "backend.idempotency.key_required", status: http.StatusBadRequest, class: "protocol", action: "correct_request_protocol"},
		{name: "repairable", kind: apperror.KindBadRequest, code: "backend.metadata.field_type_invalid", status: http.StatusUnprocessableEntity, class: "repairable", action: "repair_capability_payload", retryable: true},
		{name: "drift", kind: apperror.KindConflict, code: "backend.authoring.resource_hash_conflict", status: http.StatusConflict, class: "drift", action: "refresh_contract_and_snapshot", retryable: true},
		{name: "dependency", kind: apperror.KindUnavailable, code: "backend.integration.provider_unavailable", status: http.StatusFailedDependency, class: "dependency", action: "resolve_runtime_dependency"},
		{name: "permission", kind: apperror.KindForbidden, code: "auth.permission_denied", status: http.StatusForbidden, class: "permission", action: "resolve_authorization"},
		{name: "platform unavailable", kind: apperror.KindUnavailable, code: "backend.authoring.success_projection_unavailable", status: http.StatusServiceUnavailable, class: "transient", action: "retry_with_backoff", retryable: true},
		{name: "internal", kind: apperror.KindInternal, code: "backend.authoring.store_failed", status: http.StatusInternalServerError, class: "transient", action: "retry_with_backoff", retryable: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodPut, "/metadata/definitions/object/order", nil)
			request = request.WithContext(operationscontract.WithBuilderTaskID(request.Context(), "task-1"))
			writeServiceError(response, request, apperror.New(test.kind, test.code, nil, nil))
			body := map[string]any{}
			if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil || response.Code != test.status || body["error_class"] != test.class || body["repair_action"] != test.action || body["retryable"] != test.retryable {
				t.Fatalf("status=%d body=%#v err=%v", response.Code, body, err)
			}
		})
	}
}

func TestBuilderPrincipalShortcutRejectsEveryExplicitCredentialHeader(t *testing.T) {
	router := &HTTPRouter{}
	for _, header := range []struct{ key, value string }{
		{"Authorization", "Bearer token"},
		{"X-API-Key", "api-key"},
		{"X-Role", "viewer"},
		{"X-User-Role", "viewer"},
	} {
		request := requestWithPrincipal(httptest.NewRequest(http.MethodGet, "/", nil), principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "context-user"}})
		request = request.WithContext(operationscontract.WithBuilderTaskID(request.Context(), "task"))
		request.Header.Set(header.key, header.value)
		if principal := router.principalFromRequest(request); principal.UserID != "context-user" {
			t.Fatalf("header %s principal=%#v", header.key, principal)
		}
	}
}
