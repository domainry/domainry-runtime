package record

import (
	"context"
	"errors"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordruntime "github.com/domainry/domainry-runtime/runtime/domain/record/runtime"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type recordBatchFaultStore struct {
	*recordBatchStoreProbe
	enqueueErr       error
	enqueueReplayed  bool
	getErr           error
	findErr          error
	claimErr         error
	claimHook        func()
	cancelErr        error
	cancelFound      *bool
	cancelJob        *recordmodel.RecordBatchJob
	requeueErr       error
	requeueChanged   *bool
	chunksErr        error
	globalDepth      int
	globalErr        error
	workspaceDepth   int
	workspaceErr     error
	completeErr      error
	retryErr         error
	quarantineErr    error
	failErr          error
	checkpointErr    error
	replaceChunksErr error
}

func (s *recordBatchFaultStore) EnqueueRecordBatchJob(ctx context.Context, job recordmodel.RecordBatchJob) (recordmodel.RecordBatchJob, bool, error) {
	if s.enqueueErr != nil {
		return recordmodel.RecordBatchJob{}, false, s.enqueueErr
	}
	created, _, err := s.recordBatchStoreProbe.EnqueueRecordBatchJob(ctx, job)
	return created, s.enqueueReplayed, err
}
func (s *recordBatchFaultStore) GetRecordBatchJob(ctx context.Context, workspaceID, id string) (recordmodel.RecordBatchJob, bool, error) {
	if s.getErr != nil {
		return recordmodel.RecordBatchJob{}, false, s.getErr
	}
	return s.recordBatchStoreProbe.GetRecordBatchJob(ctx, workspaceID, id)
}
func (s *recordBatchFaultStore) FindRecordBatchJobByIdempotency(ctx context.Context, workspaceID, kind, objectKey, key string) (recordmodel.RecordBatchJob, bool, error) {
	if s.findErr != nil {
		return recordmodel.RecordBatchJob{}, false, s.findErr
	}
	return s.recordBatchStoreProbe.FindRecordBatchJobByIdempotency(ctx, workspaceID, kind, objectKey, key)
}
func (s *recordBatchFaultStore) ClaimRecordBatchJobs(ctx context.Context, limit int, owner string, lease time.Duration, now time.Time) ([]recordmodel.RecordBatchJob, error) {
	if s.claimHook != nil {
		s.claimHook()
	}
	if s.claimErr != nil {
		return nil, s.claimErr
	}
	return s.recordBatchStoreProbe.ClaimRecordBatchJobs(ctx, limit, owner, lease, now)
}
func (s *recordBatchFaultStore) CancelRecordBatchJob(ctx context.Context, workspaceID, id string) (recordmodel.RecordBatchJob, bool, error) {
	if s.cancelErr != nil {
		return recordmodel.RecordBatchJob{}, false, s.cancelErr
	}
	if s.cancelJob != nil {
		return *s.cancelJob, true, nil
	}
	job, found, err := s.recordBatchStoreProbe.CancelRecordBatchJob(ctx, workspaceID, id)
	if s.cancelFound != nil {
		found = *s.cancelFound
	}
	return job, found, err
}
func (s *recordBatchFaultStore) RequeueRecordBatchJob(ctx context.Context, workspaceID, id string, now time.Time) (recordmodel.RecordBatchJob, bool, error) {
	if s.requeueErr != nil {
		return recordmodel.RecordBatchJob{}, false, s.requeueErr
	}
	job, changed, err := s.recordBatchStoreProbe.RequeueRecordBatchJob(ctx, workspaceID, id, now)
	if s.requeueChanged != nil {
		changed = *s.requeueChanged
	}
	return job, changed, err
}
func (s *recordBatchFaultStore) ListRecordBatchJobChunks(ctx context.Context, workspaceID, id string) ([]recordmodel.RecordBatchJobChunk, error) {
	if s.chunksErr != nil {
		return nil, s.chunksErr
	}
	return s.recordBatchStoreProbe.ListRecordBatchJobChunks(ctx, workspaceID, id)
}
func (s *recordBatchFaultStore) GlobalRecordBatchJobQueueStats(context.Context, principalmodel.SystemScope) (int, time.Duration, error) {
	return s.globalDepth, time.Second, s.globalErr
}
func (s *recordBatchFaultStore) RecordBatchJobQueueStats(context.Context, string) (int, time.Duration, error) {
	return s.workspaceDepth, time.Second, s.workspaceErr
}
func (s *recordBatchFaultStore) CompleteRecordBatchJob(ctx context.Context, job recordmodel.RecordBatchJob, now time.Time) error {
	if s.completeErr != nil {
		return s.completeErr
	}
	return s.recordBatchStoreProbe.CompleteRecordBatchJob(ctx, job, now)
}
func (s *recordBatchFaultStore) RetryRecordBatchJob(ctx context.Context, job recordmodel.RecordBatchJob, next, now time.Time) error {
	if s.retryErr != nil {
		return s.retryErr
	}
	return s.recordBatchStoreProbe.RetryRecordBatchJob(ctx, job, next, now)
}
func (s *recordBatchFaultStore) QuarantineRecordBatchJob(ctx context.Context, job recordmodel.RecordBatchJob, now time.Time) error {
	if s.quarantineErr != nil {
		return s.quarantineErr
	}
	return s.recordBatchStoreProbe.QuarantineRecordBatchJob(ctx, job, now)
}
func (s *recordBatchFaultStore) FailRecordBatchJob(ctx context.Context, job recordmodel.RecordBatchJob, now time.Time) error {
	if s.failErr != nil {
		return s.failErr
	}
	return s.recordBatchStoreProbe.FailRecordBatchJob(ctx, job, now)
}
func (s *recordBatchFaultStore) SaveRecordBatchJobCheckpoint(ctx context.Context, job recordmodel.RecordBatchJob, now time.Time) error {
	if s.checkpointErr != nil {
		return s.checkpointErr
	}
	return s.recordBatchStoreProbe.SaveRecordBatchJobCheckpoint(ctx, job, now)
}
func (s *recordBatchFaultStore) ReplaceRecordBatchJobChunks(ctx context.Context, job recordmodel.RecordBatchJob, chunks []recordmodel.RecordBatchJobChunk, now time.Time) error {
	if s.replaceChunksErr != nil {
		return s.replaceChunksErr
	}
	return s.recordBatchStoreProbe.ReplaceRecordBatchJobChunks(ctx, job, chunks, now)
}

func recordBatchPrincipal() principalmodel.Principal {
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin"}}, accessfixture.Bundle{Key: "admin", Permissions: []string{"workspace.admin", "*"}, RecordScope: "all_records"})
}

func recordBatchImporter() *RecordImportApplicationService {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Name: "Name", Type: "text", Required: true}}}
	executions := &importExecutionProbe{}
	return NewRecordImportApplicationService(RecordImportDependencies{
		Repository: &importRepositoryProbe{existing: map[string]bool{}},
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return object, nil
		},
		CanWrite: func(principalmodel.Principal, definitionmodel.ObjectSchema, map[string]any) bool { return true },
		ValidateRelations: func(context.Context, definitionmodel.ObjectSchema, map[string]any, principalmodel.Principal) error {
			return nil
		},
		Execution: recordruntime.NewRecordMutationExecutionRuntime(executions),
		CreateIdempotent: func(_ context.Context, _ string, data map[string]any, key string, _ principalmodel.Principal) (recordmodel.Record, bool, error) {
			return recordmodel.Record{ID: key, Data: data}, false, nil
		},
	})
}

func recordBatchExporter() *RecordExportApplicationService {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	return NewRecordExportApplicationService(RecordExportDependencies{
		Repository: &exportRepositoryProbe{page: recordmodel.RecordPageResult{}},
		Objects: func() map[string]definitionmodel.ObjectSchema {
			return map[string]definitionmodel.ObjectSchema{"customer": object}
		},
		CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
	})
}

func TestRecordBatchEnqueueImportContracts(t *testing.T) {
	principal := recordBatchPrincipal()
	base := &recordBatchStoreProbe{}
	store := &recordBatchFaultStore{recordBatchStoreProbe: base}
	audits := 0
	service := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{Store: store, Importer: recordBatchImporter(), Audit: func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any) {
		audits++
	}})
	if _, _, err := service.EnqueueImport(t.Context(), "customer", []byte("Name\nAcme\n"), "", principal); apperror.CodeOf(err) != "backend.idempotency.key_required" {
		t.Fatalf("missing key: %v", err)
	}
	if _, _, err := (*RecordBatchJobApplicationService)(nil).EnqueueImport(t.Context(), "customer", nil, "key", principal); apperror.CodeOf(err) != "backend.record_batch.unavailable" {
		t.Fatalf("unavailable: %v", err)
	}
	if _, _, err := service.EnqueueImport(t.Context(), "customer", []byte("Name\n\"unterminated"), "invalid-preview", principal); err == nil {
		t.Fatal("expected preview failure")
	}
	job, replayed, err := service.EnqueueImport(t.Context(), "customer", []byte("Name\nAcme\n"), "key-1", principal)
	if err != nil || replayed || job.Kind != "import" || audits != 1 {
		t.Fatalf("job=%#v replayed=%v audits=%d err=%v", job, replayed, audits, err)
	}
	replay, replayed, err := service.EnqueueImport(t.Context(), "customer", []byte("Name\nAcme\n"), "key-1", principal)
	if err != nil || !replayed || replay.ID != job.ID {
		t.Fatalf("replay=%#v replayed=%v err=%v", replay, replayed, err)
	}
	if _, _, err := service.EnqueueImport(t.Context(), "customer", []byte("Name\nOther\n"), "key-1", principal); apperror.CodeOf(err) != "backend.idempotency.key_reused" {
		t.Fatalf("mismatch: %v", err)
	}
	store.findErr = errRecordBatchJobTest
	if _, _, err := service.EnqueueImport(t.Context(), "customer", []byte("Name\nAcme\n"), "key-2", principal); !errors.Is(err, errRecordBatchJobTest) {
		t.Fatalf("find error: %v", err)
	}
	missingScope := principal
	missingScope.WorkspaceID = ""
	if _, _, err := service.EnqueueImport(t.Context(), "customer", nil, "key", missingScope); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("scope error: %v", err)
	}
	store.findErr, store.globalDepth = nil, 1
	service.dependencies.QueueLimit = 1
	if _, _, err := service.EnqueueImport(t.Context(), "customer", []byte("Name\nAcme\n"), "key-2", principal); apperror.CodeOf(err) != "capacity.record_batch_queue_exhausted" {
		t.Fatalf("capacity error: %v", err)
	}
	store.globalDepth, store.enqueueErr = 0, recordcontract.ErrRecordBatchJobIdempotencyConflict
	if _, _, err := service.EnqueueImport(t.Context(), "customer", []byte("Name\nAcme\n"), "key-3", principal); apperror.CodeOf(err) != "backend.idempotency.key_reused" {
		t.Fatalf("enqueue conflict: %v", err)
	}
}

func TestRecordBatchEnqueueCapacityAndStoreConflicts(t *testing.T) {
	principal := recordBatchPrincipal()
	store := &recordBatchFaultStore{recordBatchStoreProbe: &recordBatchStoreProbe{}, globalDepth: 1}
	service := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{Store: store, Exporter: recordBatchExporter(), QueueLimit: 1, WorkspaceLimit: 1})
	if _, _, err := service.EnqueueExport(t.Context(), "customer", "key", RecordExportOptions{}, principal); apperror.CodeOf(err) != "capacity.record_batch_queue_exhausted" {
		t.Fatalf("global capacity: %v", err)
	}
	store.globalDepth, store.workspaceDepth = 0, 1
	if _, _, err := service.EnqueueExport(t.Context(), "customer", "key", RecordExportOptions{}, principal); apperror.CodeOf(err) != "capacity.record_batch_workspace_queue_exhausted" {
		t.Fatalf("workspace capacity: %v", err)
	}
	store.workspaceDepth, store.enqueueErr = 0, recordcontract.ErrRecordBatchJobIdempotencyConflict
	if _, _, err := service.EnqueueExport(t.Context(), "customer", "key", RecordExportOptions{}, principal); apperror.CodeOf(err) != "backend.idempotency.key_reused" {
		t.Fatalf("enqueue conflict: %v", err)
	}
	store.enqueueErr, store.globalErr = nil, errRecordBatchJobTest
	if _, _, err := service.EnqueueExport(t.Context(), "customer", "key", RecordExportOptions{}, principal); !errors.Is(err, errRecordBatchJobTest) {
		t.Fatalf("global stats error: %v", err)
	}
	store.globalErr, store.workspaceErr = nil, errRecordBatchJobTest
	if _, _, err := service.EnqueueExport(t.Context(), "customer", "key", RecordExportOptions{}, principal); !errors.Is(err, errRecordBatchJobTest) {
		t.Fatalf("workspace stats error: %v", err)
	}
	store.workspaceErr = nil
	if _, _, err := service.EnqueueExport(t.Context(), "customer", "marshal", RecordExportOptions{Query: recordmodel.RecordListQuery{Filters: map[string]any{"bad": func() {}}}}, principal); err == nil {
		t.Fatal("expected export payload marshal error")
	}
}
