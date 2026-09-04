package dispatch

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	dispatchapplication "github.com/domainry/domainry-runtime/runtime/application/dispatch"
	schedulergateway "github.com/domainry/domainry-scheduler-sdk/dispatchgateway"
)

type dispatchGatewayStub struct{ calls int }

func (d *dispatchGatewayStub) Execute(_ context.Context, request dispatchapplication.ExecutionRequest) (dispatchapplication.ExecutionReceipt, error) {
	d.calls++
	return dispatchapplication.ExecutionReceipt{ID: "receipt-1", Owner: request.Target.Owner, Status: "accepted"}, nil
}

func TestTargetExecutionGatewayAuthenticatesScopesAndReturnsReceipt(t *testing.T) {
	dispatcher := &dispatchGatewayStub{}
	now := time.Date(2026, time.September, 4, 1, 2, 3, 0, time.UTC)
	secret := []byte("runtime-signing-secret")
	handler := NewExecutionHandler(TargetExecutionDependencies{
		Executor: dispatcher, RuntimeID: "runtime-a",
		SigningSecret: secret, Now: func() time.Time { return now },
		WriteJSON: func(w http.ResponseWriter, status int, value any) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		},
	})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	target := executionTarget{Type: "runtime_operation", Owner: "workflow", Operation: "start", Payload: json.RawMessage(`{}`)}
	body, _ := json.Marshal(executionRequest{RuntimeID: "runtime-a", ExecutionID: "run-1", IdempotencyKey: "run-1", Target: target})

	unauthorized := httptest.NewRequest(http.MethodPost, RuntimeExecutionPath, bytes.NewReader(body))
	unauthorized.Header.Set("X-Domainry-Runtime-ID", "runtime-a")
	unauthorizedResponse := httptest.NewRecorder()
	mux.ServeHTTP(unauthorizedResponse, unauthorized)
	if unauthorizedResponse.Code != http.StatusUnauthorized || dispatcher.calls != 0 {
		t.Fatalf("unauthorized status=%d calls=%d", unauthorizedResponse.Code, dispatcher.calls)
	}

	accepted := httptest.NewRequest(http.MethodPost, RuntimeExecutionPath, bytes.NewReader(body))
	accepted.Header.Set("X-Domainry-Runtime-ID", "runtime-a")
	signSchedulerRequest(accepted, body, "run-1", secret, now)
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
	body, _ := json.Marshal(executionRequest{RuntimeID: "runtime-a", ExecutionID: "run-1", IdempotencyKey: "run-1", Target: target})

	tamperedBody := bytes.Replace(body, []byte("run-1"), []byte("run-2"), 1)
	tampered := httptest.NewRequest(http.MethodPost, RuntimeExecutionPath, bytes.NewReader(tamperedBody))
	tampered.Header.Set("X-Domainry-Runtime-ID", "runtime-a")
	signSchedulerRequest(tampered, body, "run-1", secret, now)
	tamperedResponse := httptest.NewRecorder()
	mux.ServeHTTP(tamperedResponse, tampered)
	if tamperedResponse.Code != http.StatusUnauthorized {
		t.Fatalf("tampered status=%d", tamperedResponse.Code)
	}

	stale := httptest.NewRequest(http.MethodPost, RuntimeExecutionPath, bytes.NewReader(body))
	stale.Header.Set("X-Domainry-Runtime-ID", "runtime-a")
	signSchedulerRequest(stale, body, "run-1", secret, now.Add(-schedulergateway.MaxClockSkew-time.Second))
	staleResponse := httptest.NewRecorder()
	mux.ServeHTTP(staleResponse, stale)
	if staleResponse.Code != http.StatusUnauthorized || dispatcher.calls != 0 {
		t.Fatalf("stale status=%d calls=%d", staleResponse.Code, dispatcher.calls)
	}
}

func signSchedulerRequest(request *http.Request, body []byte, executionID string, secret []byte, signedAt time.Time) {
	timestamp := strconv.FormatInt(signedAt.Unix(), 10)
	request.Header.Set(schedulergateway.ClientIDHeader, schedulergateway.SchedulerClientID)
	request.Header.Set(schedulergateway.TimestampHeader, timestamp)
	request.Header.Set(schedulergateway.SignatureHeader, schedulergateway.Sign(body, executionID, schedulergateway.SchedulerClientID, timestamp, secret))
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
