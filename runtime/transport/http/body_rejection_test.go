package http

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeJSONBoundsBodiesAndRecordsStableRejectionReason(t *testing.T) {
	collector := NewMemoryHTTPMetricsCollector(8)
	router := &HTTPRouter{httpMetrics: collector, maxJSONBodyBytes: 8}
	request := httptest.NewRequest(http.MethodPost, "/records/objects/customer/records", strings.NewReader(`{"value":"payload"}`))
	response := httptest.NewRecorder()
	if router.decodeJSONBody(response, request, &map[string]any{}) {
		t.Fatal("oversized body was accepted")
	}
	if response.Code != http.StatusRequestEntityTooLarge || !strings.Contains(collector.Prometheus(), `reason="too_large"} 1`) {
		t.Fatalf("status=%d metrics=%s", response.Code, collector.Prometheus())
	}
}
