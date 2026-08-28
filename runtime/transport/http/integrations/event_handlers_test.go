package integrations_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	integrationapplication "github.com/domainry/domainry-runtime/runtime/application/integration"
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	integrationpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/integration"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	integrationhttp "github.com/domainry/domainry-runtime/runtime/transport/http/integrations"
)

type integrationHTTPEventHandler struct{}

func (integrationHTTPEventHandler) ProcessIntegrationEvent(context.Context, integrationmodel.IntegrationEvent, principalmodel.Principal) (integrationapplication.EventProcessDecision, error) {
	return integrationapplication.EventProcessDecision{Status: "processed"}, nil
}

type integrationHTTPEventResponse struct {
	Event     integrationmodel.IntegrationEvent `json:"event"`
	Duplicate bool                              `json:"duplicate"`
}

func TestIntegrationEventHTTPStateRetryReplayAndWorker(t *testing.T) {
	store, application := newIntegrationEventHTTPApplication(t)
	defer store.Close()
	application.RegisterIntegrationEventHandler("probe", integrationHTTPEventHandler{})

	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-1", UserID: "operator"}}, accessfixture.Bundle{Permissions: []string{
		integrationapplication.PermissionInvoke,
		integrationapplication.PermissionRetry,
		integrationapplication.PermissionAuditView,
	}},
	)
	handler := integrationEventHTTPHandler(application, &principal)
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	integrationhttp.RegisterInternalWorkerRoutesForTest(handler, mux)
	call := func(method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(method, path, strings.NewReader(body))
		for key, value := range headers {
			request.Header.Set(key, value)
		}
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		return response
	}

	if response := call(http.MethodPost, "/integrations/events", `{`, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid json status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/integrations/events", `{}`, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("missing identity status=%d body=%s", response.Code, response.Body.String())
	}

	first := call(http.MethodPost, "/integrations/events", `{"provider":"probe","event_type":"updated","external_id":"external-1","payload":{"_integration_context":{"connection_key":"spoofed"},"value":1}}`, nil)
	if first.Code != http.StatusCreated {
		t.Fatalf("create status=%d body=%s", first.Code, first.Body.String())
	}
	var created integrationHTTPEventResponse
	if err := json.Unmarshal(first.Body.Bytes(), &created); err != nil || created.Event.ID == "" || created.Duplicate {
		t.Fatalf("created=%+v err=%v", created, err)
	}
	if _, leaked := created.Event.Payload["_integration_context"]; leaked {
		t.Fatalf("caller supplied runtime context leaked: %#v", created.Event.Payload)
	}

	duplicate := call(http.MethodPost, "/integrations/events", `{"provider":"probe","event_type":"updated","external_id":"external-1","payload":{"value":1}}`, nil)
	var replay integrationHTTPEventResponse
	if duplicate.Code != http.StatusOK || json.Unmarshal(duplicate.Body.Bytes(), &replay) != nil || !replay.Duplicate || replay.Event.ID != created.Event.ID {
		t.Fatalf("duplicate status=%d value=%+v body=%s", duplicate.Code, replay, duplicate.Body.String())
	}

	listed := call(http.MethodGet, "/integrations/events?provider=probe&status=received&limit=100", "", nil)
	var listResult struct {
		Events []integrationmodel.IntegrationEvent `json:"events"`
		Count  int                                 `json:"count"`
	}
	if listed.Code != http.StatusOK || json.Unmarshal(listed.Body.Bytes(), &listResult) != nil || listResult.Count != 1 || len(listResult.Events) != 1 {
		t.Fatalf("list status=%d result=%+v body=%s", listed.Code, listResult, listed.Body.String())
	}
	if invalid := call(http.MethodGet, "/integrations/events?limit=101", "", nil); invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "backend.integration.query_limit_invalid") {
		t.Fatalf("invalid event limit status=%d body=%s", invalid.Code, invalid.Body.String())
	}

	recovery := call(http.MethodPost, "/integrations/events/recover-offline", `{"events":[{"provider":"offline-adapter","event_type":"fact.created","external_id":"offline-1","payload":{"value":1}},{"provider":"offline-adapter","event_type":"fact.created","external_id":"offline-1","payload":{"value":1}},{"provider":"offline-adapter","event_type":"fact.created","external_id":"offline-1","payload":{"value":2}},{"provider":"offline-adapter","event_type":"fact.created","external_id":"offline-2","payload":{"value":3}}]}`, nil)
	var recoveryResult integrationmodel.IntegrationOfflineEventRecoveryResult
	if recovery.Code != http.StatusOK || json.Unmarshal(recovery.Body.Bytes(), &recoveryResult) != nil || recoveryResult.Accepted != 2 || recoveryResult.Duplicates != 1 || recoveryResult.ReconciliationRequired != 1 || len(recoveryResult.ConflictExternalIDs) != 1 || recoveryResult.ConflictExternalIDs[0] != "offline-1" {
		t.Fatalf("recovery status=%d result=%+v body=%s", recovery.Code, recoveryResult, recovery.Body.String())
	}
	if response := call(http.MethodPost, "/integrations/events/recover-offline", `{`, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid recovery status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/integrations/events/recover-offline", `{}`, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid recovery request status=%d body=%s", response.Code, response.Body.String())
	}
	if invalid := call(http.MethodGet, "/integrations/events?limit=invalid", "", nil); invalid.Code != http.StatusBadRequest {
		t.Fatalf("non-numeric event limit status=%d body=%s", invalid.Code, invalid.Body.String())
	}

	if response := call(http.MethodPost, "/integrations/events/"+created.Event.ID+"/status", `{"status":"unknown"}`, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/integrations/events/"+created.Event.ID+"/status", `{`, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid status json=%d body=%s", response.Code, response.Body.String())
	}
	failed := call(http.MethodPost, "/integrations/events/"+created.Event.ID+"/status", `{"status":"failed","error":"temporary"}`, nil)
	if failed.Code != http.StatusOK {
		t.Fatalf("failed status=%d body=%s", failed.Code, failed.Body.String())
	}

	replayed := call(http.MethodPost, "/integrations/events/"+created.Event.ID+"/replay", "", map[string]string{
		"Idempotency-Key": "event-replay-1", "X-Operation-Reason": "operator verified upstream recovery",
	})
	if replayed.Code != http.StatusOK {
		t.Fatalf("replay status=%d body=%s", replayed.Code, replayed.Body.String())
	}

	second := call(http.MethodPost, "/integrations/events", `{"provider":"probe","event_type":"updated","external_id":"external-2"}`, nil)
	var secondCreated integrationHTTPEventResponse
	if second.Code != http.StatusCreated || json.Unmarshal(second.Body.Bytes(), &secondCreated) != nil {
		t.Fatalf("second status=%d body=%s", second.Code, second.Body.String())
	}
	if response := call(http.MethodPost, "/integrations/events/"+secondCreated.Event.ID+"/status", `{"status":"failed"}`, nil); response.Code != http.StatusOK {
		t.Fatalf("second failed status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/integrations/events/"+secondCreated.Event.ID+"/retry", `{`, nil); response.Code != http.StatusBadRequest {
		t.Fatalf("invalid retry json=%d body=%s", response.Code, response.Body.String())
	}
	retried := call(http.MethodPost, "/integrations/events/"+secondCreated.Event.ID+"/retry", `{"delay_seconds":0,"error":"retry"}`, map[string]string{
		"Idempotency-Key": "event-retry-1", "X-Operation-Reference": "incident-1",
	})
	if retried.Code != http.StatusOK {
		t.Fatalf("retry status=%d body=%s", retried.Code, retried.Body.String())
	}

	processed := call(http.MethodPost, "/integrations/events/process-due?limit=999", "", nil)
	var batch integrationapplication.EventProcessBatchResult
	if processed.Code != http.StatusOK || json.Unmarshal(processed.Body.Bytes(), &batch) != nil {
		t.Fatalf("process status=%d body=%s", processed.Code, processed.Body.String())
	}

	if response := call(http.MethodPost, "/integrations/events/missing/retry", `{}`, map[string]string{"Idempotency-Key": "missing-retry"}); response.Code != http.StatusNotFound {
		t.Fatalf("missing retry status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/integrations/events/missing/replay", "", map[string]string{"Idempotency-Key": "missing-replay"}); response.Code != http.StatusNotFound {
		t.Fatalf("missing replay status=%d body=%s", response.Code, response.Body.String())
	}
	accessfixture.Set(&principal, accessfixture.Bundle{})
	if response := call(http.MethodGet, "/integrations/events", "", nil); response.Code != http.StatusForbidden {
		t.Fatalf("permission status=%d body=%s", response.Code, response.Body.String())
	}
	if response := call(http.MethodPost, "/integrations/events/process-due", "", nil); response.Code != http.StatusForbidden {
		t.Fatalf("process permission status=%d body=%s", response.Code, response.Body.String())
	}
}

func newIntegrationEventHTTPApplication(t *testing.T) (*database.RuntimeStore, *integrationapplication.IntegrationApplicationService) {
	t.Helper()
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "integration-events.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		store.Close()
		t.Fatal(err)
	}
	application := integrationapplication.NewIntegrationApplicationService(integrationapplication.ApplicationDependencies{
		ConfigRepository:   integrationpersistence.NewIntegrationConfigStore(store),
		EventRepository:    integrationpersistence.NewIntegrationEventStore(store),
		DeliveryRepository: integrationpersistence.NewIntegrationDeliveryStore(store),
		WorkerRepository:   integrationpersistence.NewIntegrationWorkerStore(store),
		Registry:           integrationapplication.NewConnectorRegistry(integrationmodel.IntegrationSchema{}),
	})
	return store, application
}

func integrationEventHTTPHandler(application *integrationapplication.IntegrationApplicationService, principal *principalmodel.Principal) *integrationhttp.IntegrationsHandler {
	passthrough := func(next http.HandlerFunc) http.HandlerFunc { return next }
	return integrationhttp.NewIntegrationsHandler(integrationhttp.IntegrationsDependencies{
		Connections: application, Bindings: application, RuntimeExecution: application, Webhooks: application,
		Principal: func(*http.Request) principalmodel.Principal { return *principal },
		DecodeJSON: func(w http.ResponseWriter, request *http.Request, value any) bool {
			if err := json.NewDecoder(request.Body).Decode(value); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return false
			}
			return true
		},
		WriteJSON: func(w http.ResponseWriter, status int, value any) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		},
		WriteError: func(w http.ResponseWriter, _ *http.Request, status int, code string, _ ...string) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": code})
		},
		WriteServiceError: func(w http.ResponseWriter, _ *http.Request, err error) {
			status := http.StatusInternalServerError
			switch apperror.KindOf(err) {
			case apperror.KindBadRequest:
				status = http.StatusBadRequest
			case apperror.KindForbidden:
				status = http.StatusForbidden
			case apperror.KindNotFound:
				status = http.StatusNotFound
			case apperror.KindConflict:
				status = http.StatusConflict
			}
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"error": apperror.CodeOf(err)})
		},
		Admin: passthrough, Authenticated: passthrough, Entrypoint: passthrough,
		Locale: func(*http.Request) string { return "en-US" },
	})
}
