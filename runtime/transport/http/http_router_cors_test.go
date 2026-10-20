package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCORSMiddlewareAllowsIdentityProductSurfaceHeader(t *testing.T) {
	request := httptest.NewRequest(http.MethodOptions, "/auth/refresh", nil)
	request.Header.Set("Origin", "https://aurora.example.com")
	request.Header.Set("Access-Control-Request-Method", http.MethodPost)
	request.Header.Set("Access-Control-Request-Headers", "content-type,x-domainry-product-surface")
	response := httptest.NewRecorder()

	CORSMiddleware([]string{"https://aurora.example.com"}, http.NotFoundHandler()).ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("status=%d, want %d", response.Code, http.StatusNoContent)
	}
	allowed := strings.ToLower(response.Header().Get("Access-Control-Allow-Headers"))
	for _, required := range []string{"content-type", "x-domainry-product-surface"} {
		if !strings.Contains(allowed, required) {
			t.Fatalf("allowed headers %q do not include %q", allowed, required)
		}
	}
}
