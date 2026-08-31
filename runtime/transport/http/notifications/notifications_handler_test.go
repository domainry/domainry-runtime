package notifications

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestNotificationTemplateRoutesAreOwnedByModuleSurface(t *testing.T) {
	handler, capture, _ := newNotificationHTTPHandler(&notificationHTTPRepository{})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/notifications/templates", nil))
	if response.Code != http.StatusNotFound || capture.status != 0 {
		t.Fatalf("legacy template route = (%d, %d, %v), want module-owned 404", response.Code, capture.status, capture.err)
	}
	response = httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPatch, "/notifications/templates/order-ready", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("method route status=%d", response.Code)
	}
}
