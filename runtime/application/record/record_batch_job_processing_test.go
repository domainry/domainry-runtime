package record

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
)

type recordBatchExportErrorRepository struct {
	recordrepository.RecordRepository
	err    error
	cancel context.CancelFunc
}

func (r recordBatchExportErrorRepository) ListRecords(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	if r.cancel != nil {
		r.cancel()
	}
	return recordmodel.RecordPageResult{}, r.err
}

func recordBatchFailingExporter(err error) *RecordExportApplicationService {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	return NewRecordExportApplicationService(RecordExportDependencies{
		Repository: recordBatchExportErrorRepository{err: err},
		Objects: func() map[string]definitionmodel.ObjectSchema {
			return map[string]definitionmodel.ObjectSchema{"customer": object}
		},
		CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
	})
}

func TestRecordBatchProcessClaimedCompletionAndPersistenceFailures(t *testing.T) {
	principal := recordBatchPrincipal()
	payload := `{"options":{}}`
	base := &recordBatchStoreProbe{jobs: map[string]recordmodel.RecordBatchJob{}}
	store := &recordBatchFaultStore{recordBatchStoreProbe: base}
	service := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{Store: store, Exporter: recordBatchExporter(), ResolvePrincipal: func(context.Context, string, string) principalmodel.Principal { return principal }})
	job := recordmodel.RecordBatchJob{ID: "job", WorkspaceID: principal.WorkspaceID, Kind: "export", ObjectKey: "customer", PayloadJSON: payload, AttemptCount: 1}
	if err := service.processClaimed(t.Context(), job); err != nil || service.completed.Load() != 1 {
		t.Fatalf("completed=%d err=%v", service.completed.Load(), err)
	}
	store.completeErr = errRecordBatchJobTest
	if err := service.processClaimed(t.Context(), job); !errors.Is(err, errRecordBatchJobTest) {
		t.Fatalf("complete error: %v", err)
	}
	store.completeErr = nil
	bad := job
	bad.PayloadJSON = "bad"
	store.quarantineErr = errRecordBatchJobTest
	if err := service.processClaimed(t.Context(), bad); !errors.Is(err, errRecordBatchJobTest) {
		t.Fatalf("quarantine error: %v", err)
	}
	store.quarantineErr = nil
	if err := service.processClaimed(t.Context(), bad); err != nil || service.quarantined.Load() != 1 {
		t.Fatalf("quarantined=%d err=%v", service.quarantined.Load(), err)
	}
}

func TestRecordBatchProcessClaimedRetryAndFailure(t *testing.T) {
	principal := recordBatchPrincipal()
	internal := apperror.New(apperror.KindInternal, "backend.internal", errRecordBatchJobTest, nil)
	store := &recordBatchFaultStore{recordBatchStoreProbe: &recordBatchStoreProbe{jobs: map[string]recordmodel.RecordBatchJob{}}}
	service := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{Store: store, Exporter: recordBatchFailingExporter(internal), ResolvePrincipal: func(context.Context, string, string) principalmodel.Principal { return principal }})
	job := recordmodel.RecordBatchJob{ID: "retry", WorkspaceID: principal.WorkspaceID, Kind: "export", ObjectKey: "customer", PayloadJSON: `{"options":{}}`, AttemptCount: 1}
	if err := service.processClaimed(t.Context(), job); err != nil || service.retried.Load() != 1 {
		t.Fatalf("retried=%d err=%v", service.retried.Load(), err)
	}
	store.retryErr = errRecordBatchJobTest
	if err := service.processClaimed(t.Context(), job); !errors.Is(err, errRecordBatchJobTest) {
		t.Fatalf("retry store error: %v", err)
	}
	store.retryErr = nil
	job.AttemptCount = 3
	if err := service.processClaimed(t.Context(), job); err != nil || service.failed.Load() != 1 {
		t.Fatalf("failed=%d err=%v", service.failed.Load(), err)
	}
	store.failErr = errRecordBatchJobTest
	if err := service.processClaimed(t.Context(), job); !errors.Is(err, errRecordBatchJobTest) {
		t.Fatalf("fail store error: %v", err)
	}
}

func TestRecordBatchProcessClaimedImportAndEmptyErrorCodeFallback(t *testing.T) {
	principal := recordBatchPrincipal()
	importer := recordBatchImporter()
	importer.dependencies.CreateIdempotent = func(context.Context, string, map[string]any, string, principalmodel.Principal) (recordmodel.Record, bool, error) {
		return recordmodel.Record{}, false, &apperror.AppError{Kind: apperror.KindInternal}
	}
	base := &recordBatchStoreProbe{jobs: map[string]recordmodel.RecordBatchJob{}}
	service := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{
		Store:    base,
		Importer: importer,
		ResolvePrincipal: func(context.Context, string, string) principalmodel.Principal {
			return principal
		},
	})
	job := recordmodel.RecordBatchJob{
		ID:           "import-empty-code",
		WorkspaceID:  principal.WorkspaceID,
		Kind:         "import",
		ObjectKey:    "customer",
		PayloadJSON:  `{"csv":"Name\nAcme\n"}`,
		AttemptCount: 3,
	}
	if err := service.processClaimed(t.Context(), job); err != nil {
		t.Fatalf("process import: %v", err)
	}
	stored := base.jobs[job.ID]
	if stored.Status != "failed" || stored.ErrorCode != "backend.record_batch.failed" {
		t.Fatalf("stored job=%#v", stored)
	}
}

func TestRecordBatchProcessExportChunksAndCancellation(t *testing.T) {
	principal := recordBatchPrincipal()
	large := strings.Repeat("x", recordBatchResultChunkBytes+100)
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	exporter := NewRecordExportApplicationService(RecordExportDependencies{
		Repository: &exportRepositoryProbe{page: recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "one", Data: map[string]any{"name": large}}}}},
		Objects: func() map[string]definitionmodel.ObjectSchema {
			return map[string]definitionmodel.ObjectSchema{"customer": object}
		},
		CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
	})
	store := &recordBatchFaultStore{recordBatchStoreProbe: &recordBatchStoreProbe{}}
	service := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{Store: store, Exporter: exporter})
	job := recordmodel.RecordBatchJob{ID: "large", WorkspaceID: principal.WorkspaceID, Kind: "export", ObjectKey: "customer", PayloadJSON: `{"options":{}}`}
	if err := service.processExport(t.Context(), &job, principal); err != nil || job.ResultChunks != 2 || len(store.chunks[job.ID]) != 2 || job.Total != 1 {
		t.Fatalf("job=%#v chunks=%d err=%v", job, len(store.chunks[job.ID]), err)
	}
	canceled, cancel := context.WithCancel(t.Context())
	cancel()
	if err := service.processExport(canceled, &job, principal); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel error: %v", err)
	}
	store.replaceChunksErr = errRecordBatchJobTest
	if err := service.processExport(t.Context(), &job, principal); !errors.Is(err, errRecordBatchJobTest) {
		t.Fatalf("replace chunks error: %v", err)
	}
}

type pagedRecordBatchStore struct {
	*recordBatchStoreProbe
	commits int
}

func (s *pagedRecordBatchStore) CommitRecordBatchJobPage(_ context.Context, job recordmodel.RecordBatchJob, expected string, chunk recordmodel.RecordBatchJobChunk, next string, processed, total int, _ time.Time) error {
	if expected != job.CheckpointCursor {
		return fmt.Errorf("cursor mismatch: expected=%q job=%q", expected, job.CheckpointCursor)
	}
	if s.chunks == nil {
		s.chunks = map[string][]recordmodel.RecordBatchJobChunk{}
	}
	s.chunks[job.ID] = append(s.chunks[job.ID], chunk)
	job.Checkpoint, job.Total, job.CheckpointCursor = processed, total, next
	job.ResultChunks++
	if s.jobs == nil {
		s.jobs = map[string]recordmodel.RecordBatchJob{}
	}
	s.jobs[job.ID] = job
	s.commits++
	return nil
}

type pagedExportRepository struct {
	recordrepository.RecordRepository
	pages []recordmodel.RecordPageResult
	seen  []int
}

func (r *pagedExportRepository) ListRecords(_ context.Context, _ string, _ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	r.seen = append(r.seen, query.Page)
	if query.Page < 1 || query.Page > len(r.pages) {
		return recordmodel.RecordPageResult{}, fmt.Errorf("unexpected page %d", query.Page)
	}
	return r.pages[query.Page-1], nil
}

func TestRecordBatchPagedExportCommitsAndResumesAtOwnerPageBoundary(t *testing.T) {
	principal := recordBatchPrincipal()
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	repository := &pagedExportRepository{pages: []recordmodel.RecordPageResult{
		{Items: []recordmodel.Record{{ID: "one", Data: map[string]any{"name": "Ada"}}}, HasNext: true},
		{Items: []recordmodel.Record{{ID: "two", Data: map[string]any{"name": "Grace"}}}},
	}}
	exporter := NewRecordExportApplicationService(RecordExportDependencies{
		Repository: repository,
		Objects: func() map[string]definitionmodel.ObjectSchema {
			return map[string]definitionmodel.ObjectSchema{"customer": object}
		},
		CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
	})
	store := &pagedRecordBatchStore{recordBatchStoreProbe: &recordBatchStoreProbe{}}
	service := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{Store: store, Exporter: exporter})
	job := recordmodel.RecordBatchJob{ID: "paged", WorkspaceID: principal.WorkspaceID, Kind: "export", ObjectKey: "customer", PayloadJSON: `{"options":{}}`}
	if err := service.processExport(t.Context(), &job, principal); err != nil {
		t.Fatal(err)
	}
	chunks := store.chunks[job.ID]
	if store.commits != 2 || job.Checkpoint != 2 || job.ResultChunks != 2 || job.CheckpointCursor != "" || len(chunks) != 2 {
		t.Fatalf("job=%+v commits=%d chunks=%+v", job, store.commits, chunks)
	}
	if !strings.HasPrefix(chunks[0].Content, "id,created_at,updated_at,name\n") || strings.Contains(chunks[1].Content, "id,created_at") {
		t.Fatalf("chunks=%+v", chunks)
	}

	// A reclaimed job resumes from the next durable owner page and does not
	// repeat the CSV header or the already committed source query.
	repository.seen = nil
	store.commits = 0
	store.chunks[job.ID] = chunks[:1]
	resumed := recordmodel.RecordBatchJob{ID: job.ID, WorkspaceID: job.WorkspaceID, Kind: "export", ObjectKey: "customer", PayloadJSON: job.PayloadJSON, Checkpoint: 1, CheckpointCursor: recordExportPageCursorPrefix + "2", ResultChunks: 1}
	if err := service.processExport(t.Context(), &resumed, principal); err != nil {
		t.Fatal(err)
	}
	if fmt.Sprint(repository.seen) != "[2]" || resumed.Checkpoint != 2 || resumed.ResultChunks != 2 || strings.Contains(store.chunks[job.ID][1].Content, "id,created_at") {
		t.Fatalf("seen=%v resumed=%+v chunks=%+v", repository.seen, resumed, store.chunks[job.ID])
	}
}

func TestRecordBatchPagedExportRejectsUnknownCheckpoint(t *testing.T) {
	service := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{Store: &pagedRecordBatchStore{recordBatchStoreProbe: &recordBatchStoreProbe{}}, Exporter: recordBatchExporter()})
	job := recordmodel.RecordBatchJob{ID: "bad-cursor", WorkspaceID: "workspace-a", Kind: "export", ObjectKey: "customer", PayloadJSON: `{"options":{}}`, CheckpointCursor: "foreign:cursor"}
	if err := service.processExport(t.Context(), &job, recordBatchPrincipal()); apperror.CodeOf(err) != "backend.record_batch.checkpoint_invalid" {
		t.Fatalf("err=%v", err)
	}
}

func TestRecordBatchProcessImportContracts(t *testing.T) {
	principal := recordBatchPrincipal()
	store := &recordBatchFaultStore{recordBatchStoreProbe: &recordBatchStoreProbe{jobs: map[string]recordmodel.RecordBatchJob{}}}
	service := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{Store: store, Importer: recordBatchImporter()})
	job := recordmodel.RecordBatchJob{ID: "import-1", WorkspaceID: principal.WorkspaceID, Kind: "import", ObjectKey: "customer", PayloadJSON: `{"csv":"Name\nAcme\n"}`}
	if err := service.processImport(t.Context(), &job, principal); err != nil || job.Total != 1 || job.Checkpoint != 1 {
		t.Fatalf("job=%#v err=%v", job, err)
	}
	bad := job
	bad.PayloadJSON = "bad"
	if err := service.processImport(t.Context(), &bad, principal); apperror.CodeOf(err) != "backend.record_batch.payload_invalid" {
		t.Fatalf("invalid payload: %v", err)
	}
	bad.PayloadJSON = `{"csv":"Name\n\"unterminated"}`
	if err := service.processImport(t.Context(), &bad, principal); err == nil {
		t.Fatal("expected preview error")
	}
	store.checkpointErr = errRecordBatchJobTest
	job.ID = "import-2"
	if err := service.processImport(t.Context(), &job, principal); !errors.Is(err, errRecordBatchJobTest) {
		t.Fatalf("checkpoint error: %v", err)
	}
	store.checkpointErr = nil
	previewOnly := recordBatchImporter()
	previewOnly.dependencies.Execution = nil
	service.dependencies.Importer = previewOnly
	job.ID = "import-3"
	if err := service.processImport(t.Context(), &job, principal); apperror.CodeOf(err) != "backend.idempotency.receipt_unavailable" {
		t.Fatalf("apply error: %v", err)
	}
}

func TestRecordBatchStartWorkerDefaultsAndUnavailable(t *testing.T) {
	if _, open := <-(*RecordBatchJobApplicationService)(nil).StartWorker(t.Context(), 0, 0); open {
		t.Fatal("unavailable worker channel should be closed")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	service := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{Store: &recordBatchStoreProbe{}})
	if _, open := <-service.StartWorker(ctx, 0, 0); open {
		t.Fatal("canceled worker channel should close")
	}
	ctx, cancel = context.WithCancel(t.Context())
	store := &recordBatchFaultStore{recordBatchStoreProbe: &recordBatchStoreProbe{}, claimErr: errRecordBatchJobTest}
	service = NewRecordBatchJobApplicationService(RecordBatchJobDependencies{Store: store})
	done := service.StartWorker(ctx, time.Millisecond, 1)
	time.Sleep(5 * time.Millisecond)
	cancel()
	<-done
}

func TestRecordBatchProcessDueReturnsCancellationFromClaimedJob(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	principal := recordBatchPrincipal()
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	exporter := NewRecordExportApplicationService(RecordExportDependencies{
		Repository: recordBatchExportErrorRepository{err: context.Canceled, cancel: cancel},
		Objects: func() map[string]definitionmodel.ObjectSchema {
			return map[string]definitionmodel.ObjectSchema{"customer": object}
		},
		CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
	})
	store := &recordBatchFaultStore{recordBatchStoreProbe: &recordBatchStoreProbe{jobs: map[string]recordmodel.RecordBatchJob{
		"job": {ID: "job", WorkspaceID: principal.WorkspaceID, Kind: "export", ObjectKey: "customer", Status: "queued", PayloadJSON: `{"options":{}}`},
	}}}
	service := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{Store: store, Exporter: exporter, ResolvePrincipal: func(context.Context, string, string) principalmodel.Principal { return principal }})
	if err := service.ProcessDue(ctx, 1); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}

func TestRecordBatchReplayReResolvesPrincipalAndRejectsRevokedAuthorization(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	repository := &exportRepositoryProbe{}
	exporter := NewRecordExportApplicationService(RecordExportDependencies{
		Repository: repository,
		Objects: func() map[string]definitionmodel.ObjectSchema {
			return map[string]definitionmodel.ObjectSchema{"customer": object}
		},
		CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
	})
	store := &recordBatchStoreProbe{jobs: map[string]recordmodel.RecordBatchJob{}}
	resolveCalls := 0
	service := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{
		Store: store, Exporter: exporter,
		ResolvePrincipal: func(context.Context, string, string) principalmodel.Principal {
			resolveCalls++
			return principalmodel.Principal{Principal: identitysdk.Principal{Known: true, AuthorizationRevision: "revoked-current"}}
		},
	})
	job := recordmodel.RecordBatchJob{
		ID: "replayed-export", WorkspaceID: "workspace-a", ActorID: "user-1", RoleKey: "exporter",
		Kind: "export", ObjectKey: "customer", PayloadJSON: `{"options":{}}`, AttemptCount: 1,
	}
	if err := service.processClaimed(t.Context(), job); err != nil {
		t.Fatalf("revoked async replay processing: %v", err)
	}
	stored := store.jobs[job.ID]
	if resolveCalls != 1 || stored.Status != "quarantined" || stored.ErrorCode != "backend.permission.denied" || repository.lastQuery.Page != 0 {
		t.Fatalf("resolver calls=%d stored=%#v query=%#v", resolveCalls, stored, repository.lastQuery)
	}
}

func TestRecordBatchProcessExportObservesCancellationBetweenExportAndChunking(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	principal := recordBatchPrincipal()
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	exporter := NewRecordExportApplicationService(RecordExportDependencies{
		Repository: &exportRepositoryProbe{page: recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "one", Data: map[string]any{"name": "value"}}}}},
		Objects: func() map[string]definitionmodel.ObjectSchema {
			return map[string]definitionmodel.ObjectSchema{"customer": object}
		},
		CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
		Audit: func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any) {
			cancel()
		},
	})
	service := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{Store: &recordBatchStoreProbe{}, Exporter: exporter})
	job := recordmodel.RecordBatchJob{ID: "job", WorkspaceID: principal.WorkspaceID, Kind: "export", ObjectKey: "customer", PayloadJSON: `{"options":{}}`}
	if err := service.processExport(ctx, &job, principal); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected chunk cancellation, got %v", err)
	}
}

func TestRecordBatchResolvePrincipalFallbackAndOverride(t *testing.T) {
	job := recordmodel.RecordBatchJob{ID: "job", WorkspaceID: "workspace", ActorID: "actor", RoleKey: "role"}
	service := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{})
	principal := service.resolvePrincipal(t.Context(), job)
	if principal.Known || principal.WorkspaceID != "workspace" || principal.UserID != "actor" || principal.RequestID != "job" {
		t.Fatalf("principal=%#v", principal)
	}
	service.dependencies.ResolvePrincipal = func(context.Context, string, string) principalmodel.Principal {
		return principalmodel.Principal{Principal: identitysdk.Principal{Known: true}}
	}
	if principal = service.resolvePrincipal(t.Context(), job); !principal.Known || principal.WorkspaceID != "workspace" {
		t.Fatalf("principal=%#v", principal)
	}
}
