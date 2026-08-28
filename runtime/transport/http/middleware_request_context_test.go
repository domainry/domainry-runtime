package http

// This test guards request context propagation in the shared middleware chain.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/domainry/domainry-foundation/requestcontext"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

type flushTrackingWriter struct {
	header  http.Header
	flushed bool
}

func (w *flushTrackingWriter) Header() http.Header               { return w.header }
func (w *flushTrackingWriter) WriteHeader(int)                   {}
func (w *flushTrackingWriter) Write(payload []byte) (int, error) { return len(payload), nil }
func (w *flushTrackingWriter) Flush()                            { w.flushed = true }

func TestMetricsMiddlewareInjectsStableRequestIDContext(t *testing.T) {
	router := &HTTPRouter{httpMetrics: NewMemoryHTTPMetricsCollector(16)}
	observed := ""
	handler := router.withMetrics(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		observed = requestcontext.RequestID(request.Context())
	}))
	request := httptest.NewRequest(http.MethodGet, "/probe", nil)
	request.Header.Set(requestIDHeader, "req-http-context")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if observed != "req-http-context" {
		t.Fatalf("handler observed request id %q", observed)
	}
	if response.Header().Get(requestIDHeader) != "req-http-context" {
		t.Fatalf("response request id = %q", response.Header().Get(requestIDHeader))
	}
}

func TestMetricsMiddlewarePropagatesW3CAndCorrelationContext(t *testing.T) {
	previous := otel.GetTracerProvider()
	provider := sdktrace.NewTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { _ = provider.Shutdown(t.Context()); otel.SetTracerProvider(previous) })
	router := &HTTPRouter{httpMetrics: NewMemoryHTTPMetricsCollector(16)}
	var traceID, correlationID, workspaceID, actorID string
	handler := router.withMetrics(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		traceID = requestcontext.TraceID(request.Context())
		correlationID = requestcontext.CorrelationID(request.Context())
		workspaceID = requestcontext.WorkspaceID(request.Context())
		actorID = requestcontext.ActorID(request.Context())
	}))
	request := httptest.NewRequest(http.MethodGet, "/probe", nil)
	request.Header.Set("traceparent", "00-4bf92f3577b34da6a3ce929d0e0e4736-00f067aa0ba902b7-01")
	request.Header.Set(correlationIDHeader, "corr-123")
	request.Header.Set("X-Workspace-ID", "workspace-a")
	request.Header.Set("X-User-ID", "user-a")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if traceID != "4bf92f3577b34da6a3ce929d0e0e4736" || correlationID != "corr-123" || workspaceID != "workspace-a" || actorID != "user-a" {
		t.Fatalf("context trace=%q correlation=%q workspace=%q actor=%q", traceID, correlationID, workspaceID, actorID)
	}
	if response.Header().Get(correlationIDHeader) != "corr-123" {
		t.Fatalf("response correlation id = %q", response.Header().Get(correlationIDHeader))
	}
}

func TestMetricsMiddlewareRebuildsInvalidTraceAndCorrelationHeaders(t *testing.T) {
	previous := otel.GetTracerProvider()
	provider := sdktrace.NewTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() { _ = provider.Shutdown(t.Context()); otel.SetTracerProvider(previous) })
	router := &HTTPRouter{httpMetrics: NewMemoryHTTPMetricsCollector(16)}
	var traceID, correlationID string
	handler := router.withMetrics(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		traceID = requestcontext.TraceID(request.Context())
		correlationID = requestcontext.CorrelationID(request.Context())
	}))
	request := httptest.NewRequest(http.MethodGet, "/probe", nil)
	request.Header.Set("traceparent", "invalid")
	request.Header.Set(correlationIDHeader, "secret\nvalue")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if len(traceID) != 32 || correlationID == "" || correlationID == "secret\nvalue" {
		t.Fatalf("rebuilt trace=%q correlation=%q", traceID, correlationID)
	}
}

func TestStatusRecorderPreservesResponseControllerFlush(t *testing.T) {
	underlying := &flushTrackingWriter{header: make(http.Header)}
	recorder := &statusRecorder{ResponseWriter: underlying, status: http.StatusOK}
	if err := http.NewResponseController(recorder).Flush(); err != nil {
		t.Fatal(err)
	}
	if !underlying.flushed {
		t.Fatal("wrapped ResponseWriter did not preserve flush capability")
	}
}

func TestRecoveryReturnsStableInternalErrorAndMetricsStatus(t *testing.T) {
	router := &HTTPRouter{httpMetrics: NewMemoryHTTPMetricsCollector(16)}
	handler := router.withMetrics(router.withRecovery(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("handler failed")
	})))
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/panic", nil))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	var payload map[string]any
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatal(err)
	}
	if payload["code"] != "backend.internal" || payload["request_id"] == "" {
		t.Fatalf("unexpected recovery payload: %#v", payload)
	}
	summary := router.httpMetrics.Summary()
	if summary["request_count"] != 1 || summary["error_count"] != 1 {
		t.Fatalf("unexpected metrics summary: %#v", summary)
	}
}
