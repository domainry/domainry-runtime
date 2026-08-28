package record

import (
	"context"
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

var errRecordBatchJobTest = errors.New("record batch job test failure")

func TestRecordBatchGetCancelAndDownloadContracts(t *testing.T) {
	principal := recordBatchPrincipal()
	base := &recordBatchStoreProbe{jobs: map[string]recordmodel.RecordBatchJob{
		"queued": {ID: "queued", WorkspaceID: principal.WorkspaceID, Kind: "import", Status: "queued"},
		"export": {ID: "export", WorkspaceID: principal.WorkspaceID, Kind: "export", Status: "completed"},
	}, chunks: map[string][]recordmodel.RecordBatchJobChunk{"export": {{JobID: "export", Sequence: 0, Content: "data"}}}}
	store := &recordBatchFaultStore{recordBatchStoreProbe: base}
	service := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{Store: store})
	job, err := service.Get(t.Context(), " queued ", principal)
	if err != nil || job.ID != "queued" {
		t.Fatalf("job=%#v err=%v", job, err)
	}
	if _, err := service.Get(t.Context(), "missing", principal); apperror.CodeOf(err) != "backend.record_batch.not_found" {
		t.Fatalf("missing: %v", err)
	}
	store.getErr = errRecordBatchJobTest
	if _, err := service.Get(t.Context(), "queued", principal); !errors.Is(err, errRecordBatchJobTest) {
		t.Fatalf("get error: %v", err)
	}
	store.getErr = nil
	job, err = service.Cancel(t.Context(), "queued", principal)
	if err != nil || job.Status != "cancelled" || service.cancelled.Load() != 1 {
		t.Fatalf("cancel=%#v metric=%d err=%v", job, service.cancelled.Load(), err)
	}
	missing := false
	store.cancelFound = &missing
	if _, err := service.Cancel(t.Context(), "queued", principal); apperror.CodeOf(err) != "backend.record_batch.not_found" {
		t.Fatalf("cancel missing: %v", err)
	}
	store.cancelFound, store.cancelErr = nil, errRecordBatchJobTest
	if _, err := service.Cancel(t.Context(), "queued", principal); !errors.Is(err, errRecordBatchJobTest) {
		t.Fatalf("cancel error: %v", err)
	}
	store.cancelErr = nil
	completed, chunks, err := service.Download(t.Context(), "export", principal)
	if err != nil || completed.ID != "export" || len(chunks) != 1 {
		t.Fatalf("completed=%#v chunks=%#v err=%v", completed, chunks, err)
	}
	if _, _, err := service.Download(t.Context(), "queued", principal); apperror.CodeOf(err) != "backend.record_batch.result_not_ready" {
		t.Fatalf("not ready: %v", err)
	}
	store.chunksErr = errRecordBatchJobTest
	if _, _, err := service.Download(t.Context(), "export", principal); !errors.Is(err, errRecordBatchJobTest) {
		t.Fatalf("chunks error: %v", err)
	}
	if _, _, err := service.Download(t.Context(), "missing", principal); apperror.CodeOf(err) != "backend.record_batch.not_found" {
		t.Fatalf("download missing: %v", err)
	}
}

func TestRecordBatchTerminalLifecycle(t *testing.T) {
	principal := recordBatchPrincipal()
	base := &recordBatchStoreProbe{jobs: map[string]recordmodel.RecordBatchJob{
		"failed":  {ID: "failed", WorkspaceID: principal.WorkspaceID, Kind: "export", Status: "failed"},
		"healthy": {ID: "healthy", WorkspaceID: principal.WorkspaceID, Kind: "export", Status: "completed"},
	}}
	store := &recordBatchFaultStore{recordBatchStoreProbe: base}
	service := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{Store: store})
	if job, err := service.InspectTerminal(t.Context(), "failed", principal); err != nil || job.ID != "failed" {
		t.Fatalf("inspect=%#v err=%v", job, err)
	}
	if _, err := service.InspectTerminal(t.Context(), "healthy", principal); apperror.CodeOf(err) != "backend.record_batch.not_dead_letter" {
		t.Fatalf("healthy: %v", err)
	}
	if _, err := service.InspectTerminal(t.Context(), "missing", principal); apperror.CodeOf(err) != "backend.record_batch.not_found" {
		t.Fatalf("inspect missing: %v", err)
	}
	job, err := service.RetryTerminal(t.Context(), "failed", principal)
	if err != nil || job.Status != "queued" {
		t.Fatalf("retry=%#v err=%v", job, err)
	}
	if _, err := service.RetryTerminal(t.Context(), "healthy", principal); apperror.CodeOf(err) != "backend.record_batch.not_dead_letter" {
		t.Fatalf("retry healthy: %v", err)
	}
	base.jobs["failed"] = recordmodel.RecordBatchJob{ID: "failed", WorkspaceID: principal.WorkspaceID, Kind: "export", Status: "failed"}
	changed := false
	store.requeueChanged = &changed
	if _, err := service.RetryTerminal(t.Context(), "failed", principal); apperror.CodeOf(err) != "backend.record_batch.retry_conflict" {
		t.Fatalf("retry conflict: %v", err)
	}
	store.requeueChanged, store.requeueErr = nil, errRecordBatchJobTest
	base.jobs["failed"] = recordmodel.RecordBatchJob{ID: "failed", WorkspaceID: principal.WorkspaceID, Kind: "export", Status: "failed"}
	if _, err := service.RetryTerminal(t.Context(), "failed", principal); !errors.Is(err, errRecordBatchJobTest) {
		t.Fatalf("retry error: %v", err)
	}
	store.requeueErr = nil
	base.jobs["failed"] = recordmodel.RecordBatchJob{ID: "failed", WorkspaceID: principal.WorkspaceID, Status: "failed"}
	resolved, err := service.ResolveTerminal(t.Context(), "failed", principal)
	if err != nil || resolved.Status != "cancelled" {
		t.Fatalf("resolve=%#v err=%v", resolved, err)
	}
	if _, err := service.ResolveTerminal(t.Context(), "healthy", principal); apperror.CodeOf(err) != "backend.record_batch.not_dead_letter" {
		t.Fatalf("resolve healthy: %v", err)
	}
}

func TestRecordBatchUnavailableAndAuthorizationContracts(t *testing.T) {
	principal := recordBatchPrincipal()
	missingScope := principal
	missingScope.WorkspaceID = ""
	service := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{})
	checks := []func() error{
		func() error { _, err := service.Get(t.Context(), "x", missingScope); return err },
		func() error { _, err := service.Cancel(t.Context(), "x", missingScope); return err },
		func() error { _, err := service.RetryTerminal(t.Context(), "x", missingScope); return err },
		func() error {
			_, _, err := service.EnqueueExport(t.Context(), "x", "key", RecordExportOptions{}, missingScope)
			return err
		},
	}
	for index, check := range checks {
		if code := apperror.CodeOf(check()); code != "backend.workspace_scope_required" {
			t.Fatalf("check %d code=%q", index, code)
		}
	}
	if _, err := service.Get(t.Context(), "x", principal); apperror.CodeOf(err) != "backend.record_batch.unavailable" {
		t.Fatalf("get unavailable: %v", err)
	}
	if _, err := service.Cancel(t.Context(), "x", principal); apperror.CodeOf(err) != "backend.record_batch.unavailable" {
		t.Fatalf("cancel unavailable: %v", err)
	}
	if _, _, err := service.EnqueueExport(t.Context(), "x", "", RecordExportOptions{}, principal); apperror.CodeOf(err) != "backend.idempotency.key_required" {
		t.Fatalf("export missing key: %v", err)
	}
	if _, _, err := service.EnqueueExport(t.Context(), "x", "key", RecordExportOptions{}, principal); apperror.CodeOf(err) != "backend.record_batch.unavailable" {
		t.Fatalf("export unavailable: %v", err)
	}
}

func TestRecordBatchProcessDueStoreAndContextFailures(t *testing.T) {
	store := &recordBatchFaultStore{recordBatchStoreProbe: &recordBatchStoreProbe{}, claimErr: errRecordBatchJobTest}
	service := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{Store: store})
	if err := service.ProcessDue(t.Context(), 1); !errors.Is(err, errRecordBatchJobTest) {
		t.Fatalf("claim error: %v", err)
	}
	if err := (*RecordBatchJobApplicationService)(nil).ProcessDue(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	store.claimErr = nil
	store.jobs = map[string]recordmodel.RecordBatchJob{"job": {ID: "job", WorkspaceID: "workspace-a", Kind: "unsupported", Status: "queued"}}
	if err := service.ProcessDue(canceled, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("context error: %v", err)
	}
}
