package integrations_test

import (
	"context"
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

func TestIntegrationSurfaceHandlersExerciseAdminAndOpsContracts(t *testing.T) {
	store, application := newIntegrationManagementHTTPApplication(t)
	defer store.Close()
	application.RegisterIntegrationEventHandler("probe", integrationHTTPEventHandler{})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1", UserID: "operator"}, RequestID: "request-1"}, accessfixture.Bundle{Permissions: []string{
		integrationapplication.PermissionCatalogView,
		integrationapplication.PermissionSecretManage,
		integrationapplication.PermissionAuditView,
		integrationapplication.PermissionInvoke,
		integrationapplication.PermissionRetry,
	}},
	)
	handler := integrationEventHTTPHandler(application, &principal)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	integrationhttp.RegisterInternalWorkerRoutesForTest(handler, mux)
	call := func(method, path, body string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		return response
	}

	for _, path := range []string{
		"/tenant-admin/integrations/catalog",
		"/tenant-admin/integrations/secrets",
		"/operations/integrations/activity?kind=events&limit=5",
	} {
		if response := call(http.MethodGet, path, ""); response.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}
	if response := call(http.MethodGet, "/operations/integrations/activity?kind=unknown", ""); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid activity status=%d body=%s", response.Code, response.Body.String())
	}

	for _, path := range []string{
		"/operations/integrations/events/event-1/retry",
		"/operations/integrations/outbox/message-1/retry",
	} {
		if response := call(http.MethodPost, path, `{`); response.Code != http.StatusBadRequest {
			t.Fatalf("%s invalid JSON status=%d body=%s", path, response.Code, response.Body.String())
		}
	}

	created := call(http.MethodPost, "/integrations/events", `{"provider":"probe","event_type":"updated","external_id":"surface-event"}`)
	var eventResponse integrationHTTPEventResponse
	if created.Code != http.StatusCreated || json.Unmarshal(created.Body.Bytes(), &eventResponse) != nil {
		t.Fatalf("create event status=%d body=%s", created.Code, created.Body.String())
	}
	if response := call(http.MethodPost, "/integrations/events/"+eventResponse.Event.ID+"/status", `{"status":"failed","error":"temporary"}`); response.Code != http.StatusOK {
		t.Fatalf("fail event status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/operations/integrations/events/"+eventResponse.Event.ID+"/retry", `{"delay_seconds":0}`); response.Code != http.StatusOK {
		t.Fatalf("retry event status=%d body=%s", response.Code, response.Body.String())
	}

	replayCreated := call(http.MethodPost, "/integrations/events", `{"provider":"probe","event_type":"updated","external_id":"surface-replay"}`)
	var replayEvent integrationHTTPEventResponse
	if replayCreated.Code != http.StatusCreated || json.Unmarshal(replayCreated.Body.Bytes(), &replayEvent) != nil {
		t.Fatalf("create replay event status=%d body=%s", replayCreated.Code, replayCreated.Body.String())
	}
	if response := call(http.MethodPost, "/integrations/events/"+replayEvent.Event.ID+"/status", `{"status":"failed"}`); response.Code != http.StatusOK {
		t.Fatalf("fail replay event status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/operations/integrations/events/"+replayEvent.Event.ID+"/replay", ""); response.Code != http.StatusOK {
		t.Fatalf("replay event status=%d body=%s", response.Code, response.Body.String())
	}

	message, err := application.EnqueueIntegrationOutboxMessage(t.Context(), integrationmodel.IntegrationOutboxEnqueueRequest{
		ConnectorKey: "webhook", ConnectionKey: "webhook-connection", Operation: "ping", RequestRef: "surface-outbox",
	}, principal)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := application.UpdateIntegrationOutboxStatus(t.Context(), message.ID, integrationmodel.IntegrationOutboxStatusRequest{Status: "failed", Error: "temporary"}, principal); err != nil {
		t.Fatal(err)
	}
	if response := call(http.MethodPost, "/operations/integrations/outbox/"+message.ID+"/retry", `{"delay_seconds":0}`); response.Code != http.StatusOK {
		t.Fatalf("retry outbox status=%d body=%s", response.Code, response.Body.String())
	}

	for _, path := range []string{
		"/operations/integrations/events/process-due?limit=1",
		"/operations/integrations/outbox/process-due?limit=1",
	} {
		if response := call(http.MethodPost, path, ""); response.Code != http.StatusOK {
			t.Fatalf("%s status=%d body=%s", path, response.Code, response.Body.String())
		}
	}

	for _, request := range []struct {
		path string
		body string
	}{
		{"/operations/integrations/events/missing/retry", `{}`},
		{"/operations/integrations/events/missing/replay", ""},
		{"/operations/integrations/outbox/missing/retry", `{}`},
	} {
		if response := call(http.MethodPost, request.path, request.body); response.Code == http.StatusOK {
			t.Fatalf("%s unexpectedly succeeded: %s", request.path, response.Body.String())
		}
	}
	for _, path := range []string{
		"/operations/integrations/events/process-due",
		"/operations/integrations/outbox/process-due",
	} {
		request := httptest.NewRequest(http.MethodPost, path, nil)
		cancelled, cancel := context.WithCancel(request.Context())
		cancel()
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request.WithContext(cancelled))
		if response.Code == http.StatusOK {
			t.Fatalf("%s cancelled request unexpectedly succeeded: %s", path, response.Body.String())
		}
	}

	accessfixture.Set(&principal, accessfixture.Bundle{})
	for _, request := range []struct {
		method string
		path   string
		body   string
	}{
		{http.MethodGet, "/tenant-admin/integrations/catalog", ""},
		{http.MethodGet, "/tenant-admin/integrations/secrets", ""},
		{http.MethodGet, "/operations/integrations/activity", ""},
		{http.MethodPost, "/operations/integrations/events/event-1/retry", `{}`},
		{http.MethodPost, "/operations/integrations/events/event-1/replay", ""},
		{http.MethodPost, "/operations/integrations/outbox/message-1/retry", `{}`},
		{http.MethodPost, "/operations/integrations/events/process-due", ""},
		{http.MethodPost, "/operations/integrations/outbox/process-due", ""},
	} {
		if response := call(request.method, request.path, request.body); response.Code != http.StatusForbidden {
			t.Fatalf("%s permission status=%d body=%s", request.path, response.Code, response.Body.String())
		}
	}
}
