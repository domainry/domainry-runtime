package businessevents

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	businesseventapplication "github.com/domainry/domainry-runtime/runtime/application/businessevent"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	businesseventmemory "github.com/domainry/domainry-runtime/runtime/infrastructure/broadcast/memory"
	apperror "github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestBusinessEventHandlerStreamsFilteredSignalsHeartbeatAndCleansUp(t *testing.T) {
	service := businesseventapplication.NewBusinessEventApplicationService(businesseventmemory.NewBusinessEventBackplane(8, 4), businesseventapplication.Limits{})
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "user-a"}}
	var audits []string
	var auditMu sync.Mutex
	handler := NewBusinessEventsHandler(BusinessEventsDependencies{
		Service: service, Principal: func(*http.Request) principalmodel.Principal { return principal },
		WriteServiceError: testWriteServiceError, HeartbeatInterval: 10 * time.Millisecond, RetryInterval: time.Second,
		SecurityAuditForPrincipal: func(_ *http.Request, _ principalmodel.Principal, event, _ string, _ map[string]any) {
			auditMu.Lock()
			audits = append(audits, event)
			auditMu.Unlock()
		},
	})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	server := httptest.NewServer(mux)
	defer server.Close()

	ctx, cancel := context.WithCancel(t.Context())
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, server.URL+Route+"?objects=order", nil)
	request.Header.Set("Authorization", "Bearer test-session")
	response, err := server.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !strings.Contains(response.Header.Get("Content-Type"), "text/event-stream") || response.Header.Get("Cache-Control") != "no-store" || response.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("status=%d headers=%v", response.StatusCode, response.Header)
	}
	_, _ = service.Publish(t.Context(), "workspace-b", "order", "mutation")
	_, _ = service.Publish(t.Context(), "workspace-a", "customer", "mutation")
	_, _ = service.Publish(t.Context(), "workspace-a", "order", "mutation")

	scanner := bufio.NewScanner(response.Body)
	seenHeartbeat := false
	seenOrder := false
	deadline := time.After(time.Second)
	for !seenHeartbeat || !seenOrder {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for heartbeat and filtered event")
		default:
		}
		if !scanner.Scan() {
			t.Fatalf("stream ended: %v", scanner.Err())
		}
		line := scanner.Text()
		seenHeartbeat = seenHeartbeat || strings.HasPrefix(line, ": heartbeat")
		if strings.HasPrefix(line, "data: ") {
			if strings.Contains(line, "workspace-") || strings.Contains(line, "customer") {
				t.Fatalf("tenant or filtered data leaked: %s", line)
			}
			seenOrder = strings.Contains(line, `"object_key":"order"`)
		}
	}
	cancel()
	_ = response.Body.Close()
	for deadline := time.Now().Add(time.Second); time.Now().Before(deadline) && service.Snapshot(t.Context()).ActiveConnections != 0; {
		time.Sleep(time.Millisecond)
	}
	if service.Snapshot(t.Context()).ActiveConnections != 0 {
		t.Fatalf("connection was not cleaned up: %+v", service.Snapshot(t.Context()))
	}
	auditMu.Lock()
	defer auditMu.Unlock()
	if !contains(audits, "business_event_stream_connected") || !contains(audits, "business_event_stream_disconnected") {
		t.Fatalf("missing lifecycle audits: %v", audits)
	}
}

func TestBusinessEventHandlerEmitsResyncForUnknownCursorAndRejectsInvalidFilter(t *testing.T) {
	service := businesseventapplication.NewBusinessEventApplicationService(businesseventmemory.NewBusinessEventBackplane(2, 2), businesseventapplication.Limits{})
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "user-a"}}
	_, _ = service.Publish(t.Context(), "workspace-a", "order", "mutation")
	handler := NewBusinessEventsHandler(BusinessEventsDependencies{Service: service, Principal: func(*http.Request) principalmodel.Principal { return principal }, WriteServiceError: testWriteServiceError})

	invalid := httptest.NewRecorder()
	invalidRequest := httptest.NewRequest(http.MethodGet, Route+"?objects=bad/value", nil)
	invalidRequest.Header.Set("Authorization", "Bearer test-session")
	handler.stream(invalid, invalidRequest)
	if invalid.Code != http.StatusBadRequest || !strings.Contains(invalid.Body.String(), "backend.event_stream.filter_invalid") {
		t.Fatalf("invalid response=%d %s", invalid.Code, invalid.Body.String())
	}

	ctx, cancel := context.WithCancel(t.Context())
	request := httptest.NewRequest(http.MethodGet, Route, nil).WithContext(ctx)
	request.Header.Set("Authorization", "Bearer test-session")
	request.Header.Set("Last-Event-ID", "evicted")
	response := newFlushRecorder(cancel)
	handler.stream(response, request)
	if !strings.Contains(response.Body.String(), "event: business.resync") || !strings.Contains(response.Body.String(), "replay_unavailable") {
		t.Fatalf("missing resync event: %s", response.Body.String())
	}
}

func TestBusinessEventHandlerRequiresBearerSessionAndMapsConnectionCapacity(t *testing.T) {
	service := businesseventapplication.NewBusinessEventApplicationService(businesseventmemory.NewBusinessEventBackplane(2, 2), businesseventapplication.Limits{GlobalConnections: 1, WorkspaceConnections: 1, PrincipalConnections: 1})
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "user-a"}}
	handler := NewBusinessEventsHandler(BusinessEventsDependencies{Service: service, Principal: func(*http.Request) principalmodel.Principal { return principal }, WriteServiceError: testWriteServiceError})

	noSession := httptest.NewRecorder()
	handler.stream(noSession, httptest.NewRequest(http.MethodGet, Route, nil))
	if noSession.Code != http.StatusForbidden || !strings.Contains(noSession.Body.String(), "backend.event_stream.identity_required") {
		t.Fatalf("non-session response=%d %s", noSession.Code, noSession.Body.String())
	}

	active, err := service.Open(t.Context(), principal, "")
	if err != nil {
		t.Fatal(err)
	}
	defer active.Close()
	limitedRequest := httptest.NewRequest(http.MethodGet, Route, nil)
	limitedRequest.Header.Set("Authorization", "Bearer test-session")
	limited := httptest.NewRecorder()
	handler.stream(limited, limitedRequest)
	if limited.Code != http.StatusTooManyRequests || !strings.Contains(limited.Body.String(), "backend.event_stream.capacity_exceeded") {
		t.Fatalf("capacity response=%d %s", limited.Code, limited.Body.String())
	}
}

type flushRecorder struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
}

func newFlushRecorder(cancel context.CancelFunc) *flushRecorder {
	return &flushRecorder{ResponseRecorder: httptest.NewRecorder(), cancel: cancel}
}

func (r *flushRecorder) Flush() { r.cancel() }

func testWriteServiceError(w http.ResponseWriter, _ *http.Request, err error) {
	status := http.StatusBadRequest
	if apperror.CodeOf(err) == "backend.event_stream.identity_required" {
		status = http.StatusForbidden
	} else if apperror.CodeOf(err) == "backend.event_stream.capacity_exceeded" {
		status = http.StatusTooManyRequests
	}
	w.WriteHeader(status)
	_, _ = io.WriteString(w, apperror.CodeOf(err))
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
