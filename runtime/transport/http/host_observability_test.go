package http

import (
	"context"
	"fmt"
	"io"
	stdhttp "net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/domainry/domainry-foundation/requestcontext"
	"github.com/domainry/domainry-foundation/telemetry"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestHostObservabilityCoversRuntimeAndProjectRoutesOnce(t *testing.T) {
	core, observed := observer.New(zapcore.InfoLevel)
	previousLogger := zap.L()
	zap.ReplaceGlobals(zap.New(core))
	t.Cleanup(func() { zap.ReplaceGlobals(previousLogger) })
	provider := sdktrace.NewTracerProvider()
	previousProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(previousProvider)
	})

	project := stdhttp.NewServeMux()
	project.HandleFunc("POST /api/crm/opportunities/{id}/win", func(writer stdhttp.ResponseWriter, _ *stdhttp.Request) {
		writer.WriteHeader(stdhttp.StatusNoContent)
	})
	routes := stdhttp.NewServeMux()
	routes.HandleFunc("GET /runtime/items/{id}", func(writer stdhttp.ResponseWriter, _ *stdhttp.Request) {
		writer.WriteHeader(stdhttp.StatusNoContent)
	})
	routes.HandleFunc("GET /live", func(writer stdhttp.ResponseWriter, _ *stdhttp.Request) {
		writer.WriteHeader(stdhttp.StatusNoContent)
	})
	routes.Handle("POST /api/", project)
	handler := (&HTTPRouter{}).HostObservabilityMiddleware(ListenerRouteGroupPublic, routes)

	for _, test := range []struct {
		method, target string
	}{
		{stdhttp.MethodGet, "/runtime/items/one?secret=hidden"},
		{stdhttp.MethodPost, "/api/crm/opportunities/one/win?secret=hidden"},
	} {
		request := httptest.NewRequest(test.method, test.target, nil)
		handler.ServeHTTP(httptest.NewRecorder(), request)
	}
	handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(stdhttp.MethodGet, "/live?token=hidden", nil))

	entries := observed.FilterMessage("http_request").All()
	if len(entries) != 2 {
		t.Fatalf("terminal events=%d entries=%#v", len(entries), observed.All())
	}
	wantRoutes := []string{"/runtime/items/{id}", "/api/crm/opportunities/{id}/win"}
	for index, entry := range entries {
		fields := entry.ContextMap()
		if fields["route"] != wantRoutes[index] || fields["listener_group"] != "public" || fields["request_id"] != nil || fields["correlation_id"] != nil || fields["trace_id"] == "" || fields["span_id"] == "" {
			t.Fatalf("entry %d fields=%#v", index, fields)
		}
		if text := fmt.Sprint(fields); strings.Contains(text, "hidden") || strings.Contains(text, "secret") {
			t.Fatalf("entry %d leaked raw URL data: %s", index, text)
		}
	}
}

func TestProjectRequestPropagatesParentTraceToIndependentSaaS(t *testing.T) {
	core, observed := observer.New(zapcore.InfoLevel)
	previousLogger := zap.L()
	zap.ReplaceGlobals(zap.New(core))
	t.Cleanup(func() { zap.ReplaceGlobals(previousLogger) })

	provider := sdktrace.NewTracerProvider()
	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	otel.SetTracerProvider(provider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))
	t.Cleanup(func() {
		_ = provider.Shutdown(context.Background())
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
	})

	type identity struct{ requestID, traceID, spanID string }
	var runtimeIdentity, saasIdentity identity
	saasRoutes := stdhttp.NewServeMux()
	saasRoutes.HandleFunc("GET /todo/v1/discovery", func(writer stdhttp.ResponseWriter, request *stdhttp.Request) {
		saasIdentity = identity{requestID: requestcontext.RequestID(request.Context()), traceID: requestcontext.TraceID(request.Context()), spanID: requestcontext.SpanID(request.Context())}
		writer.WriteHeader(stdhttp.StatusNoContent)
	})
	saas := httptest.NewServer(telemetry.HTTPServerMiddleware(telemetry.HTTPServerConfig{
		ListenerGroup: "public", TracerName: "test.independent-todo-saas",
	}, saasRoutes))
	t.Cleanup(saas.Close)
	client := telemetry.InstrumentHTTPClient(saas.Client(), "test.generated-project-client")

	project := stdhttp.NewServeMux()
	project.HandleFunc("POST /api/project/sync", func(writer stdhttp.ResponseWriter, request *stdhttp.Request) {
		runtimeIdentity = identity{requestID: requestcontext.RequestID(request.Context()), traceID: requestcontext.TraceID(request.Context()), spanID: requestcontext.SpanID(request.Context())}
		ctx := telemetry.WithHTTPClientOperation(request.Context(), "todo.discovery")
		outbound, err := stdhttp.NewRequestWithContext(ctx, stdhttp.MethodGet, saas.URL+"/todo/v1/discovery?token=must-not-leak", nil)
		if err != nil {
			t.Errorf("create SaaS request: %v", err)
			writer.WriteHeader(stdhttp.StatusInternalServerError)
			return
		}
		response, err := client.Do(outbound)
		if err != nil {
			t.Errorf("call SaaS: %v", err)
			writer.WriteHeader(stdhttp.StatusBadGateway)
			return
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		writer.WriteHeader(response.StatusCode)
	})
	routes := stdhttp.NewServeMux()
	routes.Handle("POST /api/", project)
	runtimeHandler := (&HTTPRouter{}).HostObservabilityMiddleware(ListenerRouteGroupPublic, routes)

	request := httptest.NewRequest(stdhttp.MethodPost, "/api/project/sync?authorization=must-not-leak", nil)
	request.Header.Set("X-Correlation-ID", "untrusted-business-correlation")
	request.Header.Set(telemetry.RequestIDHeader, "4bf92f3577b34da6a3ce929d0e0e4736")
	response := httptest.NewRecorder()
	runtimeHandler.ServeHTTP(response, request)
	if response.Code != stdhttp.StatusNoContent {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	if runtimeIdentity.traceID == "" || runtimeIdentity.traceID != saasIdentity.traceID {
		t.Fatalf("trace runtime=%+v SaaS=%+v", runtimeIdentity, saasIdentity)
	}
	if runtimeIdentity.spanID == "" || saasIdentity.spanID == "" || runtimeIdentity.spanID == saasIdentity.spanID {
		t.Fatalf("span runtime=%+v SaaS=%+v", runtimeIdentity, saasIdentity)
	}
	if runtimeIdentity.requestID != runtimeIdentity.traceID || saasIdentity.requestID != runtimeIdentity.traceID || response.Header().Get(telemetry.RequestIDHeader) != runtimeIdentity.traceID {
		t.Fatalf("request identity runtime=%+v SaaS=%+v", runtimeIdentity, saasIdentity)
	}

	entries := observed.FilterMessage("http_request").All()
	if len(entries) != 2 {
		t.Fatalf("terminal events=%d entries=%#v", len(entries), observed.All())
	}
	wantRoutes := map[string]bool{"/api/project/sync": false, "/todo/v1/discovery": false}
	for _, entry := range entries {
		fields := entry.ContextMap()
		route, ok := fields["route"].(string)
		if !ok {
			t.Fatalf("access event route=%#v", fields)
		}
		if _, expected := wantRoutes[route]; !expected {
			t.Fatalf("unexpected access route=%q fields=%#v", route, fields)
		}
		wantRoutes[route] = true
		if fields["trace_id"] != runtimeIdentity.traceID || fields["correlation_id"] != nil {
			t.Fatalf("access correlation fields=%#v", fields)
		}
		if text := fmt.Sprint(fields); strings.Contains(text, "must-not-leak") || strings.Contains(text, "authorization") || strings.Contains(text, "token") {
			t.Fatalf("access event leaked raw URL data: %s", text)
		}
	}
	for route, seen := range wantRoutes {
		if !seen {
			t.Fatalf("missing access event for %s: %#v", route, entries)
		}
	}
}
