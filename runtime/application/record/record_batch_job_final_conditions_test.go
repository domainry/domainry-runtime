package record

import (
	"context"
	"errors"
	"testing"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

func TestRecordBatchUnavailableConditionOrder(t *testing.T) {
	principal := recordBatchPrincipal()
	base := &recordBatchStoreProbe{}
	if _, _, err := (&RecordBatchJobApplicationService{}).EnqueueImport(t.Context(), "customer", nil, "key", principal); apperror.CodeOf(err) != "backend.record_batch.unavailable" {
		t.Fatalf("import store err=%v", err)
	}
	if _, _, err := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{Store: base}).EnqueueImport(t.Context(), "customer", nil, "key", principal); apperror.CodeOf(err) != "backend.record_batch.unavailable" {
		t.Fatalf("importer err=%v", err)
	}
	if _, _, err := (*RecordBatchJobApplicationService)(nil).EnqueueExport(t.Context(), "customer", "key", RecordExportOptions{}, principal); apperror.CodeOf(err) != "backend.record_batch.unavailable" {
		t.Fatalf("nil export err=%v", err)
	}
	if _, _, err := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{Store: base}).EnqueueExport(t.Context(), "customer", "key", RecordExportOptions{}, principal); apperror.CodeOf(err) != "backend.record_batch.unavailable" {
		t.Fatalf("exporter err=%v", err)
	}
	if _, err := (*RecordBatchJobApplicationService)(nil).Get(t.Context(), "job", principal); apperror.CodeOf(err) != "backend.record_batch.unavailable" {
		t.Fatalf("nil get err=%v", err)
	}
	if _, err := (*RecordBatchJobApplicationService)(nil).Cancel(t.Context(), "job", principal); apperror.CodeOf(err) != "backend.record_batch.unavailable" {
		t.Fatalf("nil cancel err=%v", err)
	}
}

func TestRecordBatchEnqueueGenericErrorsAndStoreReplay(t *testing.T) {
	principal := recordBatchPrincipal()
	failure := errors.New("enqueue failed")
	store := &recordBatchFaultStore{recordBatchStoreProbe: &recordBatchStoreProbe{}, enqueueErr: failure}
	service := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{Store: store, Importer: recordBatchImporter(), Exporter: recordBatchExporter()})
	if _, _, err := service.EnqueueImport(t.Context(), "customer", []byte("Name\nAcme\n"), "import-error", principal); !errors.Is(err, failure) {
		t.Fatalf("import enqueue err=%v", err)
	}
	store.enqueueErr, store.enqueueReplayed = nil, true
	if _, replayed, err := service.EnqueueImport(t.Context(), "customer", []byte("Name\nAcme\n"), "import-replay", principal); err != nil || !replayed {
		t.Fatalf("import replay=%v err=%v", replayed, err)
	}

	store.enqueueErr, store.enqueueReplayed = failure, false
	if _, _, err := service.EnqueueExport(t.Context(), "customer", "export-error", RecordExportOptions{}, principal); !errors.Is(err, failure) {
		t.Fatalf("export enqueue err=%v", err)
	}
	store.enqueueErr, store.enqueueReplayed = nil, true
	if _, replayed, err := service.EnqueueExport(t.Context(), "customer", "export-replay", RecordExportOptions{}, principal); err != nil || !replayed {
		t.Fatalf("export replay=%v err=%v", replayed, err)
	}
	store.findErr = failure
	if _, _, err := service.EnqueueExport(t.Context(), "customer", "export-find", RecordExportOptions{}, principal); !errors.Is(err, failure) {
		t.Fatalf("export preflight err=%v", err)
	}
}

func TestRecordBatchStatusConditionEdges(t *testing.T) {
	principal := recordBatchPrincipal()
	running := recordmodel.RecordBatchJob{ID: "running", WorkspaceID: principal.WorkspaceID, Status: "running"}
	store := &recordBatchFaultStore{recordBatchStoreProbe: &recordBatchStoreProbe{}, cancelJob: &running}
	service := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{Store: store})
	if job, err := service.Cancel(t.Context(), running.ID, principal); err != nil || job.Status != "running" {
		t.Fatalf("cancel running=%#v err=%v", job, err)
	}

	store.cancelJob = nil
	store.jobs = map[string]recordmodel.RecordBatchJob{
		"quarantined":   {ID: "quarantined", WorkspaceID: principal.WorkspaceID, Status: "quarantined"},
		"import-result": {ID: "import-result", WorkspaceID: principal.WorkspaceID, Status: "completed", Kind: "import"},
	}
	if job, err := service.InspectTerminal(t.Context(), "quarantined", principal); err != nil || job.Status != "quarantined" {
		t.Fatalf("inspect=%#v err=%v", job, err)
	}
	if _, _, err := service.Download(t.Context(), "import-result", principal); apperror.CodeOf(err) != "backend.record_batch.result_not_ready" {
		t.Fatalf("download kind err=%v", err)
	}
}

func TestRecordBatchWorkerAndQueueMetricConditionEdges(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	service := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{Store: &recordBatchStoreProbe{}})
	done := service.StartWorker(ctx, time.Millisecond, 1)
	time.Sleep(5 * time.Millisecond)
	cancel()
	<-done

	ctx, cancel = context.WithCancel(t.Context())
	store := &recordBatchFaultStore{recordBatchStoreProbe: &recordBatchStoreProbe{}, claimErr: errors.New("claim failed"), claimHook: cancel}
	service = NewRecordBatchJobApplicationService(RecordBatchJobDependencies{Store: store})
	done = service.StartWorker(ctx, time.Millisecond, 1)
	<-done

	store = &recordBatchFaultStore{recordBatchStoreProbe: &recordBatchStoreProbe{}, globalErr: errors.New("stats failed")}
	service = NewRecordBatchJobApplicationService(RecordBatchJobDependencies{Store: store})
	if err := service.ProcessDue(t.Context(), 1); err != nil {
		t.Fatalf("stats failure should not stop claims: %v", err)
	}
	store.globalErr = nil
	store.jobs = map[string]recordmodel.RecordBatchJob{"job": {ID: "job", WorkspaceID: "workspace-a", Kind: "unsupported", Status: "queued"}}
	if err := service.ProcessDue(t.Context(), 1); err != nil {
		t.Fatalf("successful claimed processing err=%v", err)
	}
}

func TestRecordBatchProcessDueObservesJobCancellationWithLiveParent(t *testing.T) {
	principal := recordBatchPrincipal()
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	exporter := NewRecordExportApplicationService(RecordExportDependencies{
		Repository: &exportEdgeRepository{list: func(ctx context.Context, _ string, _ definitionmodel.ObjectSchema, _ recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
			<-ctx.Done()
			return recordmodel.RecordPageResult{}, ctx.Err()
		}},
		Objects: func() map[string]definitionmodel.ObjectSchema {
			return map[string]definitionmodel.ObjectSchema{"customer": object}
		},
		CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
	})
	job := recordmodel.RecordBatchJob{ID: "job", WorkspaceID: principal.WorkspaceID, Kind: "export", ObjectKey: "customer", Status: "queued", PayloadJSON: `{"options":{}}`}
	cancelled := job
	cancelled.Status = "cancelled"
	store := &recordBatchRuntimeStore{recordBatchStoreProbe: &recordBatchStoreProbe{jobs: map[string]recordmodel.RecordBatchJob{"job": job}}, getJob: &cancelled}
	service := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{Store: store, Exporter: exporter, ResolvePrincipal: func(context.Context, string, string) principalmodel.Principal { return principal }})
	service.cancelPollInterval = time.Millisecond
	service.heartbeatInterval = time.Hour
	if err := service.ProcessDue(t.Context(), 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("job cancellation err=%v", err)
	}
}
