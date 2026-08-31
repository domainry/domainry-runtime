package runtimehost

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/modulehttp"
)

func TestModuleHTTPGovernanceEnforcesIdempotencyReasonAndConfirmation(t *testing.T) {
	route := modulehttp.Route{Governance: &modulehttp.Governance{
		EffectClass: modulehttp.EffectWrite, HighRiskPolicy: modulehttp.HighRiskConfirmationRequired,
		IdempotencyDecision: "caller_key_required", AuditClass: "mutation_audit_required",
	}}
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
