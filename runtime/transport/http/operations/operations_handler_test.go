package operations

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestIdempotencyOperationRoutesAreAdminProtected(t *testing.T) {
	handler := NewOperationsHandler(OperationsDependencies{Admin: func(http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusForbidden) }
	}})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/operations/idempotency/receipts", nil),
		httptest.NewRequest(http.MethodPost, "/operations/idempotency/receipts/record/receipt-1/retry", nil),
		httptest.NewRequest(http.MethodPost, "/operations/idempotency/receipts/record/receipt-1/reset", nil),
	} {
		response := httptest.NewRecorder()
		mux.ServeHTTP(response, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("%s %s status=%d", request.Method, request.URL.Path, response.Code)
		}
	}
}
