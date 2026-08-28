package records

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	auditapplication "github.com/domainry/domainry-runtime/runtime/application/audit"
	auditmodel "github.com/domainry/domainry-runtime/runtime/domain/audit/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestBusinessRecordStreamPublishesContentFreeCursor(t *testing.T) {
	repository := &recordsAuditRepository{events: []auditmodel.AuditEvent{{
		ID: "audit-1", WorkspaceID: "workspace", ObjectKey: "candidate_process",
		RecordID: "candidate-secret", CreatedAt: "2026-08-08T00:00:00Z",
	}}}
	handler, _ := recordsHandlerForTest(recordsHTTPPrincipal())
	handler.audit = auditapplication.NewAuditApplicationService(repository)

	ctx, cancel := context.WithCancel(t.Context())
	request := httptest.NewRequest(http.MethodGet, "/business/records/stream", nil).WithContext(ctx)
	response := &cancelAfterWriteRecorder{ResponseRecorder: httptest.NewRecorder(), cancel: cancel}
	handler.streamBusinessRecords(response, request)

	body := response.Body.String()
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/event-stream" ||
		!strings.Contains(body, "event: business.sync") || strings.Contains(body, "candidate-secret") {
		t.Fatalf("unexpected business sync response: status=%d headers=%v body=%s", response.Code, response.Header(), body)
	}
	if repository.eventQuery.Limit != 1 {
		t.Fatalf("sync query limit = %d, want 1", repository.eventQuery.Limit)
	}
}

func TestBusinessRecordStreamDoesNotRequireAuditPermission(t *testing.T) {
	repository := &recordsAuditRepository{events: []auditmodel.AuditEvent{{
		ID: "audit-1", WorkspaceID: "workspace", ObjectKey: "candidate_process",
		RecordID: "candidate-secret", CreatedAt: "2026-08-08T00:00:00Z",
	}}}
	handler, _ := recordsHandlerForTest(recordsHTTPPrincipal())
	handler.audit = auditapplication.NewAuditApplicationService(repository)
	handler.principal = func(*http.Request) principalmodel.Principal {
		return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace", UserID: "hiring-manager"}}, accessfixture.Bundle{Key: "hiring_manager", RecordScope: "owned_records"})
	}

	ctx, cancel := context.WithCancel(t.Context())
	request := httptest.NewRequest(http.MethodGet, "/business/records/stream", nil).WithContext(ctx)
	response := &cancelAfterWriteRecorder{ResponseRecorder: httptest.NewRecorder(), cancel: cancel}
	handler.streamBusinessRecords(response, request)

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "event: business.sync") {
		t.Fatalf("business actor stream status=%d body=%s", response.Code, response.Body.String())
	}
}

func TestBusinessRecordStreamHonorsResumeCursor(t *testing.T) {
	repository := &recordsAuditRepository{}
	handler, _ := recordsHandlerForTest(recordsHTTPPrincipal())
	handler.audit = auditapplication.NewAuditApplicationService(repository)
	identity := []byte(`{"id":"","updated_at":""}`)
	cursor := base64.RawURLEncoding.EncodeToString(identity)

	ctx, cancel := context.WithCancel(t.Context())
	request := httptest.NewRequest(http.MethodGet, "/business/records/stream?cursor="+cursor, nil).WithContext(ctx)
	response := &cancelAfterWriteRecorder{ResponseRecorder: httptest.NewRecorder(), cancel: cancel}
	handler.streamBusinessRecords(response, request)
	if body := response.Body.String(); !strings.Contains(body, "event: business.ready") || strings.Contains(body, "event: business.sync") {
		t.Fatalf("unexpected resumed stream body: %s", body)
	}
}

func TestWriteBusinessRecordSSEStopsOnWriteFailure(t *testing.T) {
	writer := &notificationSSEErrorWriterForRecords{writeErr: context.Canceled}
	if writeBusinessRecordSSE(writer, "business.sync", "cursor", businessRecordSync{Cursor: "cursor", UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano)}) {
		t.Fatal("writeBusinessRecordSSE succeeded after write failure")
	}
}

func TestBusinessRecordStreamPollsChangesAndHeartbeats(t *testing.T) {
	repository := &recordsAuditRepository{events: []auditmodel.AuditEvent{{ID: "first", CreatedAt: "2026-08-08T00:00:00Z"}}}
	handler, _ := recordsHandlerForTest(recordsHTTPPrincipal())
	handler.audit = auditapplication.NewAuditApplicationService(repository)
	handler.streamPollInterval = time.Millisecond
	handler.streamHeartbeat = time.Hour
	ctx, cancel := context.WithCancel(t.Context())
	response := &cancelAfterNWriteRecorder{ResponseRecorder: httptest.NewRecorder(), cancel: cancel, remaining: 2, beforeWrite: func(write int) {
		if write == 2 {
			repository.events = []auditmodel.AuditEvent{{ID: "second", CreatedAt: "2026-08-08T00:01:00Z"}}
		}
	}}
	handler.streamBusinessRecords(response, httptest.NewRequest(http.MethodGet, "/business/records/stream", nil).WithContext(ctx))
	if body := response.Body.String(); strings.Count(body, "event: business.sync") != 2 {
		t.Fatalf("poll stream body=%s", body)
	}

	handler.streamPollInterval = time.Hour
	handler.streamHeartbeat = time.Millisecond
	ctx, cancel = context.WithCancel(t.Context())
	heartbeat := &cancelAfterNWriteRecorder{ResponseRecorder: httptest.NewRecorder(), cancel: cancel, remaining: 2}
	handler.streamBusinessRecords(heartbeat, httptest.NewRequest(http.MethodGet, "/business/records/stream", nil).WithContext(ctx))
	if !strings.Contains(heartbeat.Body.String(), ": keepalive") {
		t.Fatalf("heartbeat body=%s", heartbeat.Body.String())
	}
}

func TestBusinessRecordStreamStateAndDefaultIntervalFailures(t *testing.T) {
	handler, _ := recordsHandlerForTest(recordsHTTPPrincipal())
	handler.audit = auditapplication.NewAuditApplicationService(&recordsAuditRepository{err: errors.New("audit unavailable")})
	response := httptest.NewRecorder()
	handler.streamBusinessRecords(response, httptest.NewRequest(http.MethodGet, "/business/records/stream", nil))
	if response.Code == http.StatusOK {
		t.Fatalf("audit failure status=%d body=%s", response.Code, response.Body.String())
	}

	handler, _ = recordsHandlerForTest(principalmodel.Principal{})
	if _, err := handler.businessRecordSyncState(httptest.NewRequest(http.MethodGet, "/", nil)); err == nil {
		t.Fatal("missing workspace scope accepted")
	}

	handler, _ = recordsHandlerForTest(recordsHTTPPrincipal())
	handler.audit = auditapplication.NewAuditApplicationService(&recordsAuditRepository{})
	handler.streamPollInterval = 0
	handler.streamHeartbeat = 0
	ctx, cancel := context.WithCancel(t.Context())
	responseWithCancel := &cancelAfterWriteRecorder{ResponseRecorder: httptest.NewRecorder(), cancel: cancel}
	handler.streamBusinessRecords(responseWithCancel, httptest.NewRequest(http.MethodGet, "/business/records/stream", nil).WithContext(ctx))

	handler, _ = recordsHandlerForTest(principalmodel.Principal{Principal: identitysdk.Principal{Known: true}, SystemScope: principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "stream system principal")})
	if _, err := handler.businessRecordSyncState(httptest.NewRequest(http.MethodGet, "/", nil)); err == nil {
		t.Fatal("system principal without workspace accepted")
	}
}

func TestBusinessRecordStreamWriteAndPollFailureBranches(t *testing.T) {
	handler, _ := recordsHandlerForTest(recordsHTTPPrincipal())
	handler.audit = auditapplication.NewAuditApplicationService(&recordsAuditRepository{})
	handler.streamPollInterval, handler.streamHeartbeat = time.Hour, time.Hour
	initialFailure := &streamSequenceWriter{ResponseRecorder: httptest.NewRecorder(), failWriteAt: 1}
	handler.streamBusinessRecords(initialFailure, httptest.NewRequest(http.MethodGet, "/business/records/stream", nil))

	pollError := errors.New("poll audit failure")
	handler.audit = auditapplication.NewAuditApplicationService(&recordsAuditRepository{errors: []error{nil, pollError}})
	handler.streamPollInterval, handler.streamHeartbeat = time.Millisecond, time.Hour
	handler.streamBusinessRecords(&streamSequenceWriter{ResponseRecorder: httptest.NewRecorder()}, httptest.NewRequest(http.MethodGet, "/business/records/stream", nil))

	handler.audit = auditapplication.NewAuditApplicationService(&recordsAuditRepository{eventBatches: [][]auditmodel.AuditEvent{{{ID: "one"}}, {{ID: "two"}}}})
	pollWriteFailure := &streamSequenceWriter{ResponseRecorder: httptest.NewRecorder(), failWriteAt: 2}
	handler.streamBusinessRecords(pollWriteFailure, httptest.NewRequest(http.MethodGet, "/business/records/stream", nil))

	handler.audit = auditapplication.NewAuditApplicationService(&recordsAuditRepository{})
	ctx, cancel := context.WithCancel(t.Context())
	time.AfterFunc(5*time.Millisecond, cancel)
	handler.streamBusinessRecords(&streamSequenceWriter{ResponseRecorder: httptest.NewRecorder()}, httptest.NewRequest(http.MethodGet, "/business/records/stream", nil).WithContext(ctx))
}

func TestBusinessRecordStreamHeartbeatWriteAndFlushFailures(t *testing.T) {
	handler, _ := recordsHandlerForTest(recordsHTTPPrincipal())
	handler.audit = auditapplication.NewAuditApplicationService(&recordsAuditRepository{})
	handler.streamPollInterval, handler.streamHeartbeat = time.Hour, time.Millisecond
	for _, writer := range []*streamSequenceWriter{
		{ResponseRecorder: httptest.NewRecorder(), failWriteAt: 2},
		{ResponseRecorder: httptest.NewRecorder(), failFlushAt: 2},
	} {
		handler.streamBusinessRecords(writer, httptest.NewRequest(http.MethodGet, "/business/records/stream", nil))
	}
}

type notificationSSEErrorWriterForRecords struct {
	header   http.Header
	writeErr error
}

type streamSequenceWriter struct {
	*httptest.ResponseRecorder
	writes, flushes          int
	failWriteAt, failFlushAt int
}

func (w *streamSequenceWriter) Write(value []byte) (int, error) {
	w.writes++
	if w.writes == w.failWriteAt {
		return 0, errors.New("stream write failure")
	}
	return w.ResponseRecorder.Write(value)
}

func (w *streamSequenceWriter) FlushError() error {
	w.flushes++
	if w.flushes == w.failFlushAt {
		return errors.New("stream flush failure")
	}
	w.ResponseRecorder.Flush()
	return nil
}

type cancelAfterWriteRecorder struct {
	*httptest.ResponseRecorder
	cancel context.CancelFunc
}

type cancelAfterNWriteRecorder struct {
	*httptest.ResponseRecorder
	cancel      context.CancelFunc
	remaining   int
	beforeWrite func(int)
}

func (w *cancelAfterNWriteRecorder) Write(value []byte) (int, error) {
	write := w.remaining
	if w.beforeWrite != nil {
		w.beforeWrite(write)
	}
	written, err := w.ResponseRecorder.Write(value)
	w.remaining--
	if w.remaining == 0 {
		w.cancel()
	}
	return written, err
}

func (w *cancelAfterWriteRecorder) Write(value []byte) (int, error) {
	written, err := w.ResponseRecorder.Write(value)
	w.cancel()
	return written, err
}

func (w *notificationSSEErrorWriterForRecords) Header() http.Header {
	if w.header == nil {
		w.header = http.Header{}
	}
	return w.header
}
func (*notificationSSEErrorWriterForRecords) WriteHeader(int)             {}
func (w *notificationSSEErrorWriterForRecords) Write([]byte) (int, error) { return 0, w.writeErr }
