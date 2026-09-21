package dispatch

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	dispatchapplication "github.com/domainry/domainry-runtime/runtime/application/dispatch"
	dispatchmodel "github.com/domainry/domainry-runtime/runtime/domain/dispatch/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	dispatchpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/dispatch"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	schedulergateway "github.com/domainry/domainry-scheduler-sdk/dispatchgateway"
)

type dispatchGatewayStub struct {
	calls   int
	err     error
	request dispatchapplication.ExecutionRequest
}

type dispatchCallbackReceiptStub struct{}

func (dispatchCallbackReceiptStub) TryBeginCallback(_ context.Context, request dispatchmodel.CallbackClaimRequest) (dispatchmodel.CallbackClaimResult, error) {
	receipt := request.Receipt
	receipt.ID, receipt.LeaseOwner, receipt.FencingToken = "callback-receipt-1", request.LeaseOwner, 1
	return dispatchmodel.CallbackClaimResult{Decision: idempotency.DecisionAcquired, Receipt: receipt}, nil
}

func (dispatchCallbackReceiptStub) HeartbeatCallback(context.Context, dispatchmodel.CallbackHeartbeat) (bool, error) {
	return true, nil
}

func (dispatchCallbackReceiptStub) CompleteCallback(context.Context, dispatchmodel.CallbackCompletion) error {
	return nil
}

func (dispatchCallbackReceiptStub) FailCallbackRetryable(context.Context, dispatchmodel.CallbackFailure) error {
	return nil
}

func (d *dispatchGatewayStub) Execute(_ context.Context, request dispatchapplication.ExecutionRequest) (dispatchapplication.ExecutionReceipt, error) {
	d.calls++
	d.request = request
	if d.err != nil {
		return dispatchapplication.ExecutionReceipt{}, d.err
	}
	return dispatchapplication.ExecutionReceipt{ID: "receipt-1", Owner: request.Target.Owner, Status: "accepted"}, nil
}

func TestTargetExecutionGatewayPreservesBusinessActionAuthorizationFields(t *testing.T) {
	dispatcher := &dispatchGatewayStub{}
	now := time.Date(2026, time.September, 20, 1, 2, 3, 0, time.UTC)
	secret := []byte("runtime-signing-secret")
	handler := NewExecutionHandler(TargetExecutionDependencies{Executor: dispatcher, Receipts: dispatchCallbackReceiptStub{}, RuntimeID: "runtime-a", SigningSecret: secret, Now: func() time.Time { return now }})
	body, _ := json.Marshal(executionRequest{
		RuntimeID: "runtime-a", ExecutionID: "run-1", DefinitionKey: "expire-orders", IdempotencyKey: "window-1",
		Target: executionTarget{Type: "runtime_operation", Owner: "business_action", Operation: "order.expire", ObjectKey: "order", RunAsRole: "order_automation", Payload: json.RawMessage(`{"status":"expired"}`)},
	})
	request := httptest.NewRequest(http.MethodPost, RuntimeExecutionPath, bytes.NewReader(body))
	request.Header.Set(schedulergateway.RuntimeIDHeader, "runtime-a")
	signSchedulerRequest(t, request, body, "window-1", secret, now)
	response := httptest.NewRecorder()
	handler.acceptExecution(response, request)
	if response.Code != http.StatusOK || dispatcher.calls != 1 || dispatcher.request.DefinitionKey != "expire-orders" || dispatcher.request.Target.ObjectKey != "order" || dispatcher.request.Target.RunAsRole != "order_automation" || string(dispatcher.request.Target.Payload) != `{"status":"expired"}` {
		t.Fatalf("status=%d calls=%d request=%+v", response.Code, dispatcher.calls, dispatcher.request)
	}
}

func TestTargetExecutionGatewayAuthenticatesScopesAndReturnsReceipt(t *testing.T) {
	dispatcher := &dispatchGatewayStub{}
	now := time.Date(2026, time.September, 4, 1, 2, 3, 0, time.UTC)
	secret := []byte("runtime-signing-secret")
	handler := NewExecutionHandler(TargetExecutionDependencies{
		Executor: dispatcher, RuntimeID: "runtime-a",
		Receipts: dispatchCallbackReceiptStub{}, SigningSecret: secret, Now: func() time.Time { return now },
		WriteJSON: func(w http.ResponseWriter, status int, value any) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		},
	})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	target := executionTarget{Type: "runtime_operation", Owner: "workflow", Operation: "start", Payload: json.RawMessage(`{}`)}
	body, _ := json.Marshal(executionRequest{RuntimeID: "runtime-a", ExecutionID: "run-1", DefinitionKey: "daily", IdempotencyKey: "run-1", Target: target})

	unauthorized := httptest.NewRequest(http.MethodPost, RuntimeExecutionPath, bytes.NewReader(body))
	unauthorized.Header.Set("X-Domainry-Runtime-ID", "runtime-a")
	unauthorizedResponse := httptest.NewRecorder()
	mux.ServeHTTP(unauthorizedResponse, unauthorized)
	if unauthorizedResponse.Code != http.StatusUnauthorized || dispatcher.calls != 0 {
		t.Fatalf("unauthorized status=%d calls=%d", unauthorizedResponse.Code, dispatcher.calls)
	}

	accepted := httptest.NewRequest(http.MethodPost, RuntimeExecutionPath, bytes.NewReader(body))
	accepted.Header.Set("X-Domainry-Runtime-ID", "runtime-a")
	signSchedulerRequest(t, accepted, body, "run-1", secret, now)
	acceptedResponse := httptest.NewRecorder()
	mux.ServeHTTP(acceptedResponse, accepted)
	var receipt executionReceipt
	_ = json.NewDecoder(acceptedResponse.Body).Decode(&receipt)
	if acceptedResponse.Code != http.StatusOK || receipt.ExecutionID != "run-1" || receipt.ID != "receipt-1" || dispatcher.calls != 1 {
		t.Fatalf("status=%d receipt=%#v calls=%d", acceptedResponse.Code, receipt, dispatcher.calls)
	}
}

func TestTargetExecutionGatewayRejectsTamperedAndStaleSignatures(t *testing.T) {
	dispatcher := &dispatchGatewayStub{}
	now := time.Date(2026, time.September, 4, 1, 2, 3, 0, time.UTC)
	secret := []byte("runtime-signing-secret")
	handler := NewExecutionHandler(TargetExecutionDependencies{
		Executor: dispatcher, RuntimeID: "runtime-a", SigningSecret: secret, Now: func() time.Time { return now },
	})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	target := executionTarget{Type: "runtime_operation", Owner: "workflow", Operation: "start", Payload: json.RawMessage(`{}`)}
	body, _ := json.Marshal(executionRequest{RuntimeID: "runtime-a", ExecutionID: "run-1", DefinitionKey: "daily", IdempotencyKey: "run-1", Target: target})

	tamperedBody := bytes.Replace(body, []byte("run-1"), []byte("run-2"), 1)
	tampered := httptest.NewRequest(http.MethodPost, RuntimeExecutionPath, bytes.NewReader(tamperedBody))
	tampered.Header.Set("X-Domainry-Runtime-ID", "runtime-a")
	signSchedulerRequest(t, tampered, body, "run-1", secret, now)
	tamperedResponse := httptest.NewRecorder()
	mux.ServeHTTP(tamperedResponse, tampered)
	if tamperedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("tampered status=%d", tamperedResponse.Code)
	}

	stale := httptest.NewRequest(http.MethodPost, RuntimeExecutionPath, bytes.NewReader(body))
	stale.Header.Set("X-Domainry-Runtime-ID", "runtime-a")
	signSchedulerRequest(t, stale, body, "run-1", secret, now.Add(-schedulergateway.MaxClockSkew-time.Second))
	staleResponse := httptest.NewRecorder()
	mux.ServeHTTP(staleResponse, stale)
	if staleResponse.Code != http.StatusUnauthorized || dispatcher.calls != 0 {
		t.Fatalf("stale status=%d calls=%d", staleResponse.Code, dispatcher.calls)
	}
}

func signSchedulerRequest(t *testing.T, request *http.Request, body []byte, idempotencyKey string, secret []byte, signedAt time.Time) {
	t.Helper()
	timestamp := strconv.FormatInt(signedAt.Unix(), 10)
	runtimeID := request.Header.Get(schedulergateway.RuntimeIDHeader)
	value, err := schedulergateway.SignRequest(body, schedulergateway.SignedRequest{
		Method: request.Method, Path: request.URL.EscapedPath(), RuntimeID: runtimeID, IdempotencyKey: idempotencyKey,
	}, schedulergateway.SchedulerClientID, timestamp, secret)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set(schedulergateway.SignatureVersionHeader, schedulergateway.CallbackSignatureContractVersion)
	request.Header.Set(schedulergateway.ClientIDHeader, schedulergateway.SchedulerClientID)
	request.Header.Set(schedulergateway.TimestampHeader, timestamp)
	request.Header.Set(schedulergateway.SignatureHeader, value)
}

func TestTargetExecutionGatewayV2SignatureBindsCompleteRequest(t *testing.T) {
	now := time.Date(2026, time.September, 4, 1, 2, 3, 0, time.UTC)
	secret := []byte("runtime-signing-secret")
	body, _ := json.Marshal(executionRequest{
		RuntimeID: "runtime-a", ExecutionID: "run-1", DefinitionKey: "daily", IdempotencyKey: "key-1",
		Target: executionTarget{Type: "runtime_operation", Owner: "workflow", Operation: "start", Payload: json.RawMessage(`{"value":1}`)},
	})
	tests := []struct {
		name   string
		mutate func(*http.Request)
		signAs string
		stale  bool
	}{
		{name: "method", mutate: func(request *http.Request) { request.Method = http.MethodPut }},
		{name: "escaped path", mutate: func(request *http.Request) { request.URL.Path = "/dispatch/other" }},
		{name: "runtime header", mutate: func(request *http.Request) { request.Header.Set(schedulergateway.RuntimeIDHeader, "runtime-b") }},
		{name: "runtime body", mutate: func(request *http.Request) {
			changed := bytes.Replace(body, []byte(`"runtime_id":"runtime-a"`), []byte(`"runtime_id":"runtime-b"`), 1)
			request.Body = io.NopCloser(bytes.NewReader(changed))
		}},
		{name: "idempotency identity", signAs: "other-key"},
		{name: "body", mutate: func(request *http.Request) {
			changed := bytes.Replace(body, []byte(`"value":1`), []byte(`"value":2`), 1)
			request.Body = io.NopCloser(bytes.NewReader(changed))
		}},
		{name: "version", mutate: func(request *http.Request) { request.Header.Set(schedulergateway.SignatureVersionHeader, "v1") }},
		{name: "timestamp", stale: true},
		{name: "signature", mutate: func(request *http.Request) {
			request.Header.Set(schedulergateway.SignatureHeader, strings.Repeat("0", 64))
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			dispatcher := &dispatchGatewayStub{}
			handler := NewExecutionHandler(TargetExecutionDependencies{Executor: dispatcher, RuntimeID: "runtime-a", SigningSecret: secret, Now: func() time.Time { return now }})
			request := httptest.NewRequest(http.MethodPost, RuntimeExecutionPath, bytes.NewReader(body))
			request.Header.Set(schedulergateway.RuntimeIDHeader, "runtime-a")
			key := "key-1"
			if test.signAs != "" {
				key = test.signAs
			}
			signedAt := now
			if test.stale {
				signedAt = now.Add(-schedulergateway.MaxClockSkew - time.Second)
			}
			signSchedulerRequest(t, request, body, key, secret, signedAt)
			if test.mutate != nil {
				test.mutate(request)
			}
			response := httptest.NewRecorder()
			handler.acceptExecution(response, request)
			if response.Code == http.StatusOK || dispatcher.calls != 0 {
				t.Fatalf("status=%d calls=%d", response.Code, dispatcher.calls)
			}
		})
	}
}

func TestTargetExecutionGatewayHandlesExecutorErrorsWithoutPanicking(t *testing.T) {
	now := time.Date(2026, time.September, 4, 1, 2, 3, 0, time.UTC)
	secret := []byte("runtime-signing-secret")
	body, _ := json.Marshal(executionRequest{
		RuntimeID: "runtime-a", ExecutionID: "run-1", DefinitionKey: "daily", IdempotencyKey: "key-1",
		Target: executionTarget{Type: "runtime_operation", Owner: "workflow", Operation: "start"},
	})
	executionErr := errors.New("downstream failed")

	t.Run("missing writer uses fail-safe response", func(t *testing.T) {
		dispatcher := &dispatchGatewayStub{err: executionErr}
		handler := NewExecutionHandler(TargetExecutionDependencies{Executor: dispatcher, Receipts: dispatchCallbackReceiptStub{}, RuntimeID: "runtime-a", SigningSecret: secret, Now: func() time.Time { return now }})
		request := httptest.NewRequest(http.MethodPost, RuntimeExecutionPath, bytes.NewReader(body))
		request.Header.Set(schedulergateway.RuntimeIDHeader, "runtime-a")
		signSchedulerRequest(t, request, body, "key-1", secret, now)
		response := httptest.NewRecorder()

		handler.acceptExecution(response, request)

		if response.Code != http.StatusInternalServerError || dispatcher.calls != 1 || !strings.Contains(response.Body.String(), `"code":"dispatch.execution_failed"`) {
			t.Fatalf("status=%d body=%s calls=%d", response.Code, response.Body.String(), dispatcher.calls)
		}
	})

	t.Run("configured writer preserves business error mapping", func(t *testing.T) {
		dispatcher := &dispatchGatewayStub{err: executionErr}
		var received error
		handler := NewExecutionHandler(TargetExecutionDependencies{
			Executor: dispatcher, Receipts: dispatchCallbackReceiptStub{}, RuntimeID: "runtime-a", SigningSecret: secret, Now: func() time.Time { return now },
			WriteServiceError: func(w http.ResponseWriter, _ *http.Request, err error) {
				received = err
				w.WriteHeader(http.StatusConflict)
			},
		})
		request := httptest.NewRequest(http.MethodPost, RuntimeExecutionPath, bytes.NewReader(body))
		request.Header.Set(schedulergateway.RuntimeIDHeader, "runtime-a")
		signSchedulerRequest(t, request, body, "key-1", secret, now)
		response := httptest.NewRecorder()

		handler.acceptExecution(response, request)

		if response.Code != http.StatusConflict || !errors.Is(received, executionErr) || dispatcher.calls != 1 {
			t.Fatalf("status=%d received=%v calls=%d", response.Code, received, dispatcher.calls)
		}
	})
}

func TestTargetExecutionGatewayDurablyReplaysConflictsAndReclaims(t *testing.T) {
	previousWorkspaceID := principalmodel.InstallationWorkspaceID
	t.Cleanup(func() { principalmodel.InstallationWorkspaceID = previousWorkspaceID })
	if err := principalmodel.ConfigureInstallationWorkspaceID("workspace-primary"); err != nil {
		t.Fatal(err)
	}
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "dispatch.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	receipts := dispatchpersistence.NewCallbackReceiptStore(store)
	dispatcher := &dispatchGatewayStub{}
	now := time.Date(2026, time.September, 7, 3, 4, 5, 0, time.UTC)
	secret := []byte("runtime-signing-secret")
	handler := NewExecutionHandler(TargetExecutionDependencies{
		Executor: dispatcher, Receipts: receipts, RuntimeID: "runtime-a", SigningSecret: secret, Now: func() time.Time { return now },
		WriteServiceError: func(w http.ResponseWriter, _ *http.Request, err error) {
			status := http.StatusInternalServerError
			switch apperror.KindOf(err) {
			case apperror.KindConflict:
				status = http.StatusConflict
			case apperror.KindUnavailable:
				status = http.StatusServiceUnavailable
			}
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]string{"code": apperror.CodeOf(err)})
		},
	})
	body := callbackExecutionBody(t, "key-1", `{"value":1}`)

	first := invokeSignedCallback(t, handler, body, "key-1", secret, now)
	var firstReceipt executionReceipt
	_ = json.NewDecoder(first.Body).Decode(&firstReceipt)
	if first.Code != http.StatusOK || firstReceipt.Replay || firstReceipt.ID != "receipt-1" || dispatcher.calls != 1 {
		t.Fatalf("first status=%d receipt=%#v calls=%d", first.Code, firstReceipt, dispatcher.calls)
	}
	replayed := invokeSignedCallback(t, handler, body, "key-1", secret, now)
	var replayReceipt executionReceipt
	_ = json.NewDecoder(replayed.Body).Decode(&replayReceipt)
	if replayed.Code != http.StatusOK || !replayReceipt.Replay || replayReceipt.ExecutionID != firstReceipt.ExecutionID || replayReceipt.ID != firstReceipt.ID || replayReceipt.Owner != firstReceipt.Owner || replayReceipt.Status != firstReceipt.Status || dispatcher.calls != 1 {
		t.Fatalf("replay status=%d receipt=%#v calls=%d", replayed.Code, replayReceipt, dispatcher.calls)
	}
	conflictBody := callbackExecutionBody(t, "key-1", `{"value":2}`)
	conflict := invokeSignedCallback(t, handler, conflictBody, "key-1", secret, now)
	if conflict.Code != http.StatusConflict || dispatcher.calls != 1 {
		t.Fatalf("conflict status=%d body=%s calls=%d", conflict.Code, conflict.Body.String(), dispatcher.calls)
	}

	activeBody := callbackExecutionBody(t, "active", `{"active":true}`)
	activeIdentity := callbackIdentity(t, activeBody, "active")
	if _, err := receipts.TryBeginCallback(t.Context(), dispatchmodel.CallbackClaimRequest{
		Receipt: dispatchmodel.CallbackReceipt{
			WorkspaceID: "workspace-primary", RuntimeID: activeIdentity.RuntimeID, Method: activeIdentity.Method, Path: activeIdentity.Path,
			IdempotencyKey: activeIdentity.IdempotencyKey, BodySHA256: activeIdentity.BodySHA256, ExecutionID: "run-active",
		},
		LeaseOwner: "other-worker", LeaseTTL: time.Minute, Now: now,
	}); err != nil {
		t.Fatal(err)
	}
	active := invokeSignedCallback(t, handler, activeBody, "active", secret, now)
	if active.Code != http.StatusServiceUnavailable || dispatcher.calls != 1 {
		t.Fatalf("active status=%d body=%s calls=%d", active.Code, active.Body.String(), dispatcher.calls)
	}

	expiredBody := callbackExecutionBody(t, "expired", `{"expired":true}`)
	expiredIdentity := callbackIdentity(t, expiredBody, "expired")
	if _, err := receipts.TryBeginCallback(t.Context(), dispatchmodel.CallbackClaimRequest{
		Receipt: dispatchmodel.CallbackReceipt{
			WorkspaceID: "workspace-primary", RuntimeID: expiredIdentity.RuntimeID, Method: expiredIdentity.Method, Path: expiredIdentity.Path,
			IdempotencyKey: expiredIdentity.IdempotencyKey, BodySHA256: expiredIdentity.BodySHA256, ExecutionID: "run-expired",
		},
		LeaseOwner: "stale-worker", LeaseTTL: time.Minute, Now: now.Add(-2 * time.Minute),
	}); err != nil {
		t.Fatal(err)
	}
	expired := invokeSignedCallback(t, handler, expiredBody, "expired", secret, now)
	if expired.Code != http.StatusOK || dispatcher.calls != 2 {
		t.Fatalf("expired status=%d body=%s calls=%d", expired.Code, expired.Body.String(), dispatcher.calls)
	}

	retryBody := callbackExecutionBody(t, "retryable", `{"retryable":true}`)
	dispatcher.err = errors.New("transient downstream failure")
	failed := invokeSignedCallback(t, handler, retryBody, "retryable", secret, now)
	if failed.Code != http.StatusInternalServerError || dispatcher.calls != 3 {
		t.Fatalf("failed status=%d body=%s calls=%d", failed.Code, failed.Body.String(), dispatcher.calls)
	}
	dispatcher.err = nil
	retried := invokeSignedCallback(t, handler, retryBody, "retryable", secret, now)
	if retried.Code != http.StatusOK || dispatcher.calls != 4 {
		t.Fatalf("retried status=%d body=%s calls=%d", retried.Code, retried.Body.String(), dispatcher.calls)
	}
}

func callbackExecutionBody(t *testing.T, key, payload string) []byte {
	t.Helper()
	body, err := json.Marshal(executionRequest{
		RuntimeID: "runtime-a", ExecutionID: "run-" + key, DefinitionKey: "daily", IdempotencyKey: key,
		Target: executionTarget{Type: "runtime_operation", Owner: "workflow", Operation: "start", Payload: json.RawMessage(payload)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func callbackIdentity(t *testing.T, body []byte, key string) schedulergateway.CallbackRequestIdentity {
	t.Helper()
	identity, err := schedulergateway.NewCallbackRequestIdentity(body, schedulergateway.SignedRequest{
		Method: http.MethodPost, Path: RuntimeExecutionPath, RuntimeID: "runtime-a", IdempotencyKey: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func invokeSignedCallback(t *testing.T, handler *ExecutionHandler, body []byte, key string, secret []byte, now time.Time) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, RuntimeExecutionPath, bytes.NewReader(body))
	request.Header.Set(schedulergateway.RuntimeIDHeader, "runtime-a")
	signSchedulerRequest(t, request, body, key, secret, now)
	response := httptest.NewRecorder()
	handler.acceptExecution(response, request)
	return response
}

func TestRuntimeTargetExecutionGatewayDoesNotMountSchedulerOwnerHTTP(t *testing.T) {
	handler := NewExecutionHandler(TargetExecutionDependencies{WriteJSON: func(http.ResponseWriter, int, any) {}})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	for _, path := range []string{
		"/scheduler/state",
		"/scheduler/definitions",
		"/scheduler/schedules/preview",
		"/scheduler/runs/run-1/retry",
	} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusNotFound {
			t.Errorf("Runtime still mounts Scheduler owner path %s: status=%d", path, response.Code)
		}
	}
}
