package changeplans

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestChangePlanRoutesBindMethodsAndPaths(t *testing.T) {
	fixture := newChangePlansFixture()
	mux := http.NewServeMux()
	fixture.handler.RegisterRoutes(mux)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPatch, "/tenant-admin/change-plans/plan", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method route status=%d", response.Code)
	}
}
