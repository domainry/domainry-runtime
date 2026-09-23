package transport

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestEntrypointMuxPublishesOnlyInitializedBusinessTransport(t *testing.T) {
	handler := &EntrypointMux{}
	request := httptest.NewRequest(http.MethodGet, "/anything", nil)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("uninitialized status=%d", response.Code)
	}

	handler.SetBusiness(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("initialized status=%d", response.Code)
	}

	provision := httptest.NewRecorder()
	handler.ServeHTTP(provision, httptest.NewRequest(http.MethodPost, "/provision/apply", nil))
	if provision.Code != http.StatusNoContent {
		t.Fatalf("provision path received a special transport: status=%d", provision.Code)
	}
}
