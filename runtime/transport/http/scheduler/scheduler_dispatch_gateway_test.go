package scheduler

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
	"github.com/domainry/domainry-scheduler-sdk/dispatchgateway"
)

type dispatchGatewayStub struct{ calls int }

func (d *dispatchGatewayStub) Dispatch(_ context.Context, trigger schedulersdk.Trigger) (schedulersdk.DownstreamReceipt, error) {
	d.calls++
	return schedulersdk.DownstreamReceipt{ID: "receipt-1", Owner: trigger.Target.Owner, Status: "accepted"}, nil
}

func TestSchedulerDispatchGatewayAuthenticatesScopesAndReturnsReceipt(t *testing.T) {
	dispatcher := &dispatchGatewayStub{}
	handler := NewSchedulerHandler(SchedulerDependencies{
		Dispatcher: dispatcher, RuntimeID: "runtime-a",
		AuthenticateService: func(_ context.Context, credential string) error { return nil },
		Authenticated:       func(next http.HandlerFunc) http.HandlerFunc { return next },
		WriteJSON: func(w http.ResponseWriter, status int, value any) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		},
		WriteServiceError: func(w http.ResponseWriter, _ *http.Request, _ error) { http.Error(w, "failed", http.StatusBadGateway) },
	})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	target := schedulersdk.TargetRef{Type: "runtime_operation", Owner: "workflow", Operation: "start", Payload: json.RawMessage(`{}`)}
	body, _ := json.Marshal(dispatchgateway.Request{RuntimeID: "runtime-a", Trigger: schedulersdk.Trigger{RunID: "run-1", DefinitionKey: "definition-1", IdempotencyKey: "run-1", Target: target}})

	unauthorized := httptest.NewRequest(http.MethodPost, dispatchgateway.AcceptPath, bytes.NewReader(body))
	unauthorized.Header.Set("X-Domainry-Runtime-ID", "runtime-a")
	unauthorizedResponse := httptest.NewRecorder()
	mux.ServeHTTP(unauthorizedResponse, unauthorized)
	if unauthorizedResponse.Code != http.StatusUnauthorized || dispatcher.calls != 0 {
		t.Fatalf("unauthorized status=%d calls=%d", unauthorizedResponse.Code, dispatcher.calls)
	}

	accepted := httptest.NewRequest(http.MethodPost, dispatchgateway.AcceptPath, bytes.NewReader(body))
	accepted.Header.Set("X-Domainry-Runtime-ID", "runtime-a")
	accepted.Header.Set("X-Domainry-Service-Credential", "credential")
	acceptedResponse := httptest.NewRecorder()
	mux.ServeHTTP(acceptedResponse, accepted)
	var receipt dispatchgateway.Receipt
	_ = json.NewDecoder(acceptedResponse.Body).Decode(&receipt)
	if acceptedResponse.Code != http.StatusOK || receipt.RunID != "run-1" || receipt.ID != "receipt-1" || dispatcher.calls != 1 {
		t.Fatalf("status=%d receipt=%#v calls=%d", acceptedResponse.Code, receipt, dispatcher.calls)
	}
}
