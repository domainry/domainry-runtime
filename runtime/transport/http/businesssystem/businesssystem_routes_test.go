package businesssystem

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestBusinessSystemRoutesBindMethodsAndPaths(t *testing.T) {
	mux := http.NewServeMux()
	(&BusinessSystemHandler{}).RegisterRoutes(mux)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/business-system/snapshot", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method route status=%d", response.Code)
	}
}
