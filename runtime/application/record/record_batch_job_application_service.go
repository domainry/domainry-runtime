package record

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	exchangecontract "github.com/domainry/domainry-data-exchange-sdk"
	dataexchange "github.com/domainry/domainry-data-exchange/fileengine"
	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

const recordBatchResultChunkBytes = 1 << 20

type RecordBatchJobDependencies struct {
	Store                 recordcontract.RecordBatchJobStore
	Importer              *RecordImportApplicationService
	Exporter              *RecordExportApplicationService
	DataExchange          exchangecontract.Binding
	ResolvePrincipal      func(context.Context, string, string) principalmodel.Principal
	Audit                 func(context.Context, string, string, string, principalmodel.Principal, string, map[string]any, map[string]any, map[string]any)
	QueueLimit            int
	WorkspaceLimit        int
	Worker                workerplatform.Dependencies
	WorkerWakeups         *workerplatform.WakeupBroker
	NotificationCompiler  func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
	NotificationCommitter RecordBatchTerminalNotificationCommitter
}

type RecordBatchJobApplicationService struct {
	dependencies       RecordBatchJobDependencies
	cancelPollInterval time.Duration
	heartbeatInterval  time.Duration
	queued             atomic.Uint64
	completed          atomic.Uint64
	failed             atomic.Uint64
	quarantined        atomic.Uint64
	cancelled          atomic.Uint64
	inFlight           atomic.Int64
	retried            atomic.Uint64
	queueDepth         atomic.Int64
	oldestMillis       atomic.Int64
	processorsMu       sync.RWMutex
	processors         map[string]RecordBatchOwnedProcessor
	wakeups            <-chan workerplatform.DurableTaskLocator
}

type RecordBatchPageWriter interface {
	CommitPage(context.Context, *recordmodel.RecordBatchJob, string, string, int, int) error
	Chunks(context.Context, recordmodel.RecordBatchJob) ([]recordmodel.RecordBatchJobChunk, error)
}

type RecordBatchOwnedProcessor func(context.Context, *recordmodel.RecordBatchJob, principalmodel.Principal, RecordBatchPageWriter) error

type recordBatchPageWriter struct {
	service *RecordBatchJobApplicationService
}

func (w recordBatchPageWriter) CommitPage(ctx context.Context, job *recordmodel.RecordBatchJob, content, nextCursor string, processed, total int) error {
	if job == nil {
		return apperror.New(apperror.KindInternal, "backend.record_batch.job_required", nil, nil)
	}
	expected := job.CheckpointCursor
	committer, ok := w.service.dependencies.Store.(recordcontract.RecordBatchJobPageCommitter)
	if !ok {
		return apperror.New(apperror.KindInternal, "backend.record_batch.page_commit_unavailable", nil, nil)
	}
	if err := committer.CommitRecordBatchJobPage(ctx, *job, expected, recordmodel.RecordBatchJobChunk{WorkspaceID: job.WorkspaceID, JobID: job.ID, Sequence: job.ResultChunks, Content: content}, nextCursor, processed, total, w.service.dependencies.Worker.Clock.Now()); err != nil {
		return err
	}
	job.Checkpoint, job.Total, job.CheckpointCursor, job.ResultChunks = processed, total, nextCursor, job.ResultChunks+1
	return nil
}

func (w recordBatchPageWriter) Chunks(ctx context.Context, job recordmodel.RecordBatchJob) ([]recordmodel.RecordBatchJobChunk, error) {
	return w.service.dependencies.Store.ListRecordBatchJobChunks(ctx, job.WorkspaceID, job.ID)
}

type recordBatchImportPayload struct {
	CSV          string `json:"csv,omitempty"`
	SourceSHA256 string `json:"source_sha256,omitempty"`
	SourceBytes  int    `json:"source_bytes,omitempty"`
	SourceChunks int    `json:"source_chunks,omitempty"`
}

type recordBatchExportPayload struct {
	Options           RecordExportOptions `json:"options"`
	AssuranceEvidence map[string]string   `json:"assurance_evidence,omitempty"`
}

func NewRecordBatchJobApplicationService(dependencies RecordBatchJobDependencies) *RecordBatchJobApplicationService {
	dependencies.Worker = workerplatform.NormalizeDependencies(dependencies.Worker)
	if dependencies.QueueLimit <= 0 {
		dependencies.QueueLimit = 10_000
	}
	if dependencies.WorkspaceLimit <= 0 {
		dependencies.WorkspaceLimit = 1_000
	}
	service := &RecordBatchJobApplicationService{dependencies: dependencies, cancelPollInterval: 2 * time.Second, heartbeatInterval: 20 * time.Second, processors: map[string]RecordBatchOwnedProcessor{}}
	if dependencies.WorkerWakeups != nil {
		service.wakeups = dependencies.WorkerWakeups.Subscribe("record_batch", 256)
	}
	return service
}

func (s *RecordBatchJobApplicationService) RegisterOwnedProcessor(kind string, processor RecordBatchOwnedProcessor) error {
	kind = strings.TrimSpace(kind)
	if s == nil || kind == "" || processor == nil || kind == "import" || kind == "export" {
		return apperror.New(apperror.KindBadRequest, "backend.record_batch.processor_invalid", nil, nil)
	}
	s.processorsMu.Lock()
	defer s.processorsMu.Unlock()
	if s.processors[kind] != nil {
		return apperror.New(apperror.KindConflict, "backend.record_batch.processor_exists", nil, map[string]string{"kind": kind})
	}
	s.processors[kind] = processor
	return nil
}

func (s *RecordBatchJobApplicationService) EnqueueOwned(ctx context.Context, kind, objectKey, key, auditID string, payload []byte, principal principalmodel.Principal) (recordmodel.RecordBatchJob, bool, error) {
	if err := recordAuthorizeQuery(principal); err != nil {
		return recordmodel.RecordBatchJob{}, false, err
	}
	kind, objectKey, key = strings.TrimSpace(kind), strings.TrimSpace(objectKey), strings.TrimSpace(key)
	if s == nil || s.dependencies.Store == nil || kind == "" || objectKey == "" || key == "" {
		return recordmodel.RecordBatchJob{}, false, apperror.New(apperror.KindInternal, "backend.record_batch.unavailable", nil, nil)
	}
	s.processorsMu.RLock()
	processor := s.processors[kind]
	s.processorsMu.RUnlock()
	if processor == nil {
		return recordmodel.RecordBatchJob{}, false, apperror.New(apperror.KindInternal, "backend.record_batch.processor_unavailable", nil, map[string]string{"kind": kind})
	}
	if replay, found, err := s.preflightEnqueue(ctx, principal.WorkspaceID, kind, objectKey, key, string(payload)); err != nil || found {
		return replay, found, err
	}
	if err := s.admitEnqueue(ctx, principal.WorkspaceID); err != nil {
		return recordmodel.RecordBatchJob{}, false, err
	}
	job, replayed, err := s.dependencies.Store.EnqueueRecordBatchJob(ctx, recordmodel.RecordBatchJob{WorkspaceID: principal.WorkspaceID, Kind: kind, ObjectKey: objectKey, IdempotencyKey: key, PayloadJSON: string(payload), AuditID: strings.TrimSpace(auditID), ActorID: principal.UserID, RoleKey: principal.RoleKey})
	if errors.Is(err, recordcontract.ErrRecordBatchJobIdempotencyConflict) {
		return recordmodel.RecordBatchJob{}, false, apperror.New(apperror.KindConflict, idempotency.ErrorCodeKeyReused, err, map[string]string{"use_case": kind})
	}
	if err == nil && !replayed {
		s.queued.Add(1)
		s.audit(ctx, "record_batch_owned_queued", job, principal, "Queued owned record batch job")
		s.wake(job)
	}
	return job, replayed, err
}

func (s *RecordBatchJobApplicationService) GetOwned(ctx context.Context, jobID, kind string, principal principalmodel.Principal) (recordmodel.RecordBatchJob, error) {
	job, err := s.Get(ctx, jobID, principal)
	if err != nil {
		return recordmodel.RecordBatchJob{}, err
	}
	if job.Kind != strings.TrimSpace(kind) || job.ActorID != strings.TrimSpace(principal.UserID) {
		return recordmodel.RecordBatchJob{}, apperror.New(apperror.KindNotFound, "backend.record_batch.not_found", nil, nil)
	}
	return job, nil
}

func (s *RecordBatchJobApplicationService) FindOwnedByFingerprint(ctx context.Context, kind, objectKey, fingerprint string, principal principalmodel.Principal) (recordmodel.RecordBatchJob, bool, error) {
	reader, ok := s.dependencies.Store.(recordcontract.RecordBatchJobFingerprintReader)
	if !ok {
		return recordmodel.RecordBatchJob{}, false, nil
	}
	job, found, err := reader.FindLatestRecordBatchJobByFingerprint(ctx, principal.WorkspaceID, strings.TrimSpace(kind), strings.TrimSpace(objectKey), strings.TrimSpace(fingerprint))
	if err != nil || !found {
		return job, found, err
	}
	if job.ActorID != strings.TrimSpace(principal.UserID) {
		return recordmodel.RecordBatchJob{}, false, nil
	}
	return job, true, nil
}

func (s *RecordBatchJobApplicationService) FindOwnedByIdempotency(ctx context.Context, kind, objectKey, key string, principal principalmodel.Principal) (recordmodel.RecordBatchJob, bool, error) {
	if s == nil || s.dependencies.Store == nil {
		return recordmodel.RecordBatchJob{}, false, apperror.New(apperror.KindInternal, "backend.record_batch.unavailable", nil, nil)
	}
	job, found, err := s.dependencies.Store.FindRecordBatchJobByIdempotency(ctx, principal.WorkspaceID, strings.TrimSpace(kind), strings.TrimSpace(objectKey), strings.TrimSpace(key))
	if err != nil || !found {
		return job, found, err
	}
	if job.ActorID != strings.TrimSpace(principal.UserID) {
		return recordmodel.RecordBatchJob{}, false, nil
	}
	return job, true, nil
}

func (s *RecordBatchJobApplicationService) EnqueueImport(ctx context.Context, objectKey string, rawCSV []byte, key string, principal principalmodel.Principal) (recordmodel.RecordBatchJob, bool, error) {
	return s.EnqueueImportStream(ctx, objectKey, bytes.NewReader(rawCSV), objectKey+".csv", "text/csv", int64(len(rawCSV)), key, principal)
}

func (s *RecordBatchJobApplicationService) EnqueueImportStream(ctx context.Context, objectKey string, source io.Reader, filename, contentType string, maxBytes int64, key string, principal principalmodel.Principal) (recordmodel.RecordBatchJob, bool, error) {
	if err := recordAuthorizeCommand(principal); err != nil {
		return recordmodel.RecordBatchJob{}, false, err
	}
	if strings.TrimSpace(key) == "" {
		return recordmodel.RecordBatchJob{}, false, apperror.New(apperror.KindBadRequest, idempotency.ErrorCodeMissingKey, nil, map[string]string{"use_case": "record.import.async"})
	}
	if s != nil && s.dependencies.DataExchange != nil {
		job, replayed, err := s.dependencies.DataExchange.SubmitImport(ctx, exchangecontract.ImportRequest{Scope: exchangeScope(principal), Provider: "records", ObjectKey: strings.TrimSpace(objectKey), IdempotencyKey: key, Filename: filename, ContentType: contentType, Source: source, MaxBytes: maxBytes})
		if err != nil {
			if errors.Is(err, exchangecontract.ErrSourceTooLarge) {
				return recordmodel.RecordBatchJob{}, false, apperror.New(apperror.KindBadRequest, "backend.import.payload_too_large", err, nil)
			}
			if errors.Is(err, exchangecontract.ErrSourceUnreadable) {
				return recordmodel.RecordBatchJob{}, false, apperror.New(apperror.KindBadRequest, "backend.import.read_csv_failed", err, nil)
			}
			return recordmodel.RecordBatchJob{}, false, err
		}
		return recordJobFromExchange(job), replayed, nil
	}
	rawCSV, err := io.ReadAll(io.LimitReader(source, recordImportMaxBytes+1))
	if err != nil {
		return recordmodel.RecordBatchJob{}, false, apperror.New(apperror.KindBadRequest, "backend.import.read_csv_failed", err, nil)
	}
	if int64(len(rawCSV)) > recordImportMaxBytes {
		return recordmodel.RecordBatchJob{}, false, apperror.New(apperror.KindBadRequest, "backend.import.payload_too_large", nil, nil)
	}
	if s == nil || s.dependencies.Store == nil || s.dependencies.Importer == nil {
		return recordmodel.RecordBatchJob{}, false, apperror.New(apperror.KindInternal, "backend.record_batch.unavailable", nil, nil)
	}
	sourceDigest := sha256.Sum256(rawCSV)
	sourceChunks := make([]recordmodel.RecordBatchJobChunk, 0, (len(rawCSV)+recordBatchResultChunkBytes-1)/recordBatchResultChunkBytes)
	chunker, _ := dataexchange.NewChunkWriter(ctx, recordBatchResultChunkBytes, func(_ context.Context, chunk dataexchange.Chunk) error {
		sourceChunks = append(sourceChunks, recordmodel.RecordBatchJobChunk{WorkspaceID: principal.WorkspaceID, Sequence: chunk.Sequence, Content: string(chunk.Content)})
		return nil
	})
	if _, err := chunker.Write(rawCSV); err != nil {
		return recordmodel.RecordBatchJob{}, false, err
	}
	if err := chunker.Close(); err != nil {
		return recordmodel.RecordBatchJob{}, false, err
	}
	sourceStore, durableSource := s.dependencies.Store.(recordcontract.RecordBatchJobSourceEnqueuer)
	metadata := recordBatchImportPayload{SourceSHA256: hex.EncodeToString(sourceDigest[:]), SourceBytes: len(rawCSV), SourceChunks: len(sourceChunks)}
	if !durableSource {
		metadata = recordBatchImportPayload{CSV: string(rawCSV)}
	}
	payload, _ := json.Marshal(metadata)
	if replay, found, err := s.preflightEnqueue(ctx, principal.WorkspaceID, "import", objectKey, key, string(payload)); err != nil || found {
		return replay, found, err
	}
	if _, err := s.dependencies.Importer.Preview(ctx, objectKey, rawCSV, principal); err != nil {
		return recordmodel.RecordBatchJob{}, false, err
	}
	if err := s.admitEnqueue(ctx, principal.WorkspaceID); err != nil {
		return recordmodel.RecordBatchJob{}, false, err
	}
	jobInput := recordmodel.RecordBatchJob{WorkspaceID: principal.WorkspaceID, Kind: "import", ObjectKey: strings.TrimSpace(objectKey), IdempotencyKey: key, PayloadJSON: string(payload), ActorID: principal.UserID, RoleKey: principal.RoleKey}
	var job recordmodel.RecordBatchJob
	var replayed bool
	if durableSource {
		job, replayed, err = sourceStore.EnqueueRecordBatchJobWithSource(ctx, jobInput, sourceChunks)
	} else {
		// Compatibility for simple external stores during the contract migration.
		// Production stores implement the atomic source capability.
		job, replayed, err = s.dependencies.Store.EnqueueRecordBatchJob(ctx, jobInput)
	}
	if errors.Is(err, recordcontract.ErrRecordBatchJobIdempotencyConflict) {
		return recordmodel.RecordBatchJob{}, false, apperror.New(apperror.KindConflict, idempotency.ErrorCodeKeyReused, err, map[string]string{"use_case": "record.import.async"})
	}
	if err == nil && !replayed {
		s.queued.Add(1)
		s.audit(ctx, "record_batch_import_queued", job, principal, "Queued record import batch job")
		s.wake(job)
	}
	return job, replayed, err
}

func (s *RecordBatchJobApplicationService) EnqueueExport(ctx context.Context, objectKey, key string, options RecordExportOptions, principal principalmodel.Principal) (recordmodel.RecordBatchJob, bool, error) {
	if err := recordAuthorizeQuery(principal); err != nil {
		return recordmodel.RecordBatchJob{}, false, err
	}
	if strings.TrimSpace(key) == "" {
		return recordmodel.RecordBatchJob{}, false, apperror.New(apperror.KindBadRequest, idempotency.ErrorCodeMissingKey, nil, map[string]string{"use_case": "record.export.async"})
	}
	if s != nil && s.dependencies.DataExchange != nil {
		evidence, err := s.dependencies.Exporter.authorizeForBatch(ctx, objectKey, principal, options)
		if err != nil {
			return recordmodel.RecordBatchJob{}, false, err
		}
		options.AssuranceToken = ""
		payload, err := json.Marshal(recordBatchExportPayload{Options: options, AssuranceEvidence: evidence})
		if err != nil {
			return recordmodel.RecordBatchJob{}, false, err
		}
		job, replayed, err := s.dependencies.DataExchange.SubmitExport(ctx, exchangecontract.ExportRequest{Scope: exchangeScope(principal), Provider: "records", ObjectKey: strings.TrimSpace(objectKey), IdempotencyKey: key, Options: payload})
		if err != nil {
			return recordmodel.RecordBatchJob{}, false, err
		}
		return recordJobFromExchange(job), replayed, nil
	}
	if s == nil || s.dependencies.Store == nil || s.dependencies.Exporter == nil {
		return recordmodel.RecordBatchJob{}, false, apperror.New(apperror.KindInternal, "backend.record_batch.unavailable", nil, nil)
	}
	requestPayload, err := json.Marshal(recordBatchExportPayload{Options: options})
	if err != nil {
		return recordmodel.RecordBatchJob{}, false, err
	}
	if replay, found, err := s.preflightExportEnqueue(ctx, principal.WorkspaceID, objectKey, key, string(requestPayload)); err != nil || found {
		return replay, found, err
	}
	evidence, err := s.dependencies.Exporter.authorizeForBatch(ctx, objectKey, principal, options)
	if err != nil {
		return recordmodel.RecordBatchJob{}, false, err
	}
	options.AssuranceToken = ""
	// requestPayload already proved Options JSON-safe, and evidence is map[string]string.
	payload, _ := json.Marshal(recordBatchExportPayload{Options: options, AssuranceEvidence: evidence})
	if err := s.admitEnqueue(ctx, principal.WorkspaceID); err != nil {
		return recordmodel.RecordBatchJob{}, false, err
	}
	job, replayed, err := s.dependencies.Store.EnqueueRecordBatchJob(ctx, recordmodel.RecordBatchJob{WorkspaceID: principal.WorkspaceID, Kind: "export", ObjectKey: strings.TrimSpace(objectKey), IdempotencyKey: key, PayloadJSON: string(payload), ActorID: principal.UserID, RoleKey: principal.RoleKey})
	if errors.Is(err, recordcontract.ErrRecordBatchJobIdempotencyConflict) {
		return recordmodel.RecordBatchJob{}, false, apperror.New(apperror.KindConflict, idempotency.ErrorCodeKeyReused, err, map[string]string{"use_case": "record.export.async"})
	}
	if err == nil && !replayed {
		s.queued.Add(1)
		s.audit(ctx, "record_batch_export_queued", job, principal, "Queued record export batch job")
		s.wake(job)
	}
	return job, replayed, err
}

func (s *RecordBatchJobApplicationService) Get(ctx context.Context, jobID string, principal principalmodel.Principal) (recordmodel.RecordBatchJob, error) {
	if err := recordAuthorizeQuery(principal); err != nil {
		return recordmodel.RecordBatchJob{}, err
	}
	if s != nil && s.dependencies.DataExchange != nil && strings.HasPrefix(strings.TrimSpace(jobID), "data_exchange:") {
		job, err := s.dependencies.DataExchange.Job(ctx, exchangecontract.JobRequest{Scope: exchangeScope(principal), JobID: strings.TrimSpace(jobID)})
		if err != nil {
			return recordmodel.RecordBatchJob{}, err
		}
		return recordJobFromExchange(job), nil
	}
	if s == nil || s.dependencies.Store == nil {
		return recordmodel.RecordBatchJob{}, apperror.New(apperror.KindInternal, "backend.record_batch.unavailable", nil, nil)
	}
	job, found, err := s.dependencies.Store.GetRecordBatchJob(ctx, principal.WorkspaceID, strings.TrimSpace(jobID))
	if err != nil {
		return recordmodel.RecordBatchJob{}, err
	}
	if !found {
		return recordmodel.RecordBatchJob{}, apperror.New(apperror.KindNotFound, "backend.record_batch.not_found", nil, nil)
	}
	return job, nil
}

func (s *RecordBatchJobApplicationService) InspectTerminal(ctx context.Context, jobID string, principal principalmodel.Principal) (recordmodel.RecordBatchJob, error) {
	job, err := s.Get(ctx, jobID, principal)
	if err != nil {
		return recordmodel.RecordBatchJob{}, err
	}
	if job.Status != "failed" && job.Status != "quarantined" {
		return recordmodel.RecordBatchJob{}, apperror.New(apperror.KindConflict, "backend.record_batch.not_dead_letter", nil, nil)
	}
	return job, nil
}

func (s *RecordBatchJobApplicationService) RetryTerminal(ctx context.Context, jobID string, principal principalmodel.Principal) (recordmodel.RecordBatchJob, error) {
	if err := recordAuthorizeCommand(principal); err != nil {
		return recordmodel.RecordBatchJob{}, err
	}
	if _, err := s.InspectTerminal(ctx, jobID, principal); err != nil {
		return recordmodel.RecordBatchJob{}, err
	}
	job, changed, err := s.dependencies.Store.RequeueRecordBatchJob(ctx, principal.WorkspaceID, strings.TrimSpace(jobID), s.dependencies.Worker.Clock.Now())
	if err != nil {
		return recordmodel.RecordBatchJob{}, err
	}
	if !changed {
		return recordmodel.RecordBatchJob{}, apperror.New(apperror.KindConflict, "backend.record_batch.retry_conflict", nil, nil)
	}
	s.audit(ctx, "record_batch_dead_letter_retried", job, principal, "Retried record batch terminal job")
	s.wake(job)
	return job, nil
}

func (s *RecordBatchJobApplicationService) ResolveTerminal(ctx context.Context, jobID string, principal principalmodel.Principal) (recordmodel.RecordBatchJob, error) {
	if _, err := s.InspectTerminal(ctx, jobID, principal); err != nil {
		return recordmodel.RecordBatchJob{}, err
	}
	return s.Cancel(ctx, jobID, principal)
}

func (s *RecordBatchJobApplicationService) Download(ctx context.Context, jobID string, principal principalmodel.Principal) (recordmodel.RecordBatchJob, []recordmodel.RecordBatchJobChunk, error) {
	job, err := s.Get(ctx, jobID, principal)
	if err != nil {
		return recordmodel.RecordBatchJob{}, nil, err
	}
	if job.Status != "completed" || job.Kind != "export" {
		return recordmodel.RecordBatchJob{}, nil, apperror.New(apperror.KindConflict, "backend.record_batch.result_not_ready", nil, nil)
	}
	if s.dependencies.DataExchange != nil && strings.HasPrefix(job.ID, "data_exchange:") {
		artifact, err := s.dependencies.DataExchange.Download(ctx, exchangecontract.JobRequest{Scope: exchangeScope(principal), JobID: job.ID})
		if err != nil {
			return recordmodel.RecordBatchJob{}, nil, err
		}
		defer artifact.Content.Close()
		chunks := make([]recordmodel.RecordBatchJobChunk, 0)
		buffer := make([]byte, recordBatchResultChunkBytes)
		for sequence := 0; ; sequence++ {
			n, readErr := io.ReadFull(artifact.Content, buffer)
			if n > 0 {
				chunks = append(chunks, recordmodel.RecordBatchJobChunk{WorkspaceID: principal.WorkspaceID, JobID: job.ID, Sequence: sequence, Content: string(buffer[:n])})
			}
			if readErr == io.EOF || readErr == io.ErrUnexpectedEOF {
				break
			}
			if readErr != nil {
				return recordmodel.RecordBatchJob{}, nil, readErr
			}
		}
		job.ResultFilename, job.ResultType, job.ResultChunks = artifact.Filename, artifact.ContentType, len(chunks)
		return job, chunks, nil
	}
	chunks, err := s.dependencies.Store.ListRecordBatchJobChunks(ctx, principal.WorkspaceID, job.ID)
	if err == nil && s.dependencies.Audit != nil {
		hasher := sha256.New()
		bytes := 0
		for _, chunk := range chunks {
			_, _ = hasher.Write([]byte(chunk.Content))
			bytes += len(chunk.Content)
		}
		var payload recordBatchExportPayload
		_ = json.Unmarshal([]byte(job.PayloadJSON), &payload)
		s.dependencies.Audit(ctx, "record_batch_export_downloaded", job.ObjectKey, job.ID, principal, "Downloaded record export batch result", nil, nil, map[string]any{
			"job_id": job.ID, "record_count": job.Total, "fields": payload.Options.Fields, "filter_summary": payload.Options.FilterSummary,
			"masking_policy": payload.Options.MaskingPolicy, "export_reason": payload.Options.Reason, "download_bytes": bytes,
			"download_sha256": hex.EncodeToString(hasher.Sum(nil)), "assurance_grant_id": payload.AssuranceEvidence["grant_id"],
			"assurance_methods": payload.AssuranceEvidence["methods"], "assurance_payload_digest": payload.AssuranceEvidence["payload_digest"],
		})
	}
	return job, chunks, err
}

func (s *RecordBatchJobApplicationService) OpenDownload(ctx context.Context, jobID string, principal principalmodel.Principal) (recordmodel.RecordBatchJob, io.ReadCloser, error) {
	job, err := s.Get(ctx, jobID, principal)
	if err != nil {
		return recordmodel.RecordBatchJob{}, nil, err
	}
	if job.Status != "completed" || job.Kind != "export" {
		return recordmodel.RecordBatchJob{}, nil, apperror.New(apperror.KindConflict, "backend.record_batch.result_not_ready", nil, nil)
	}
	if s.dependencies.DataExchange != nil && strings.HasPrefix(job.ID, "data_exchange:") {
		artifact, err := s.dependencies.DataExchange.Download(ctx, exchangecontract.JobRequest{Scope: exchangeScope(principal), JobID: job.ID})
		if err != nil {
			return recordmodel.RecordBatchJob{}, nil, err
		}
		job.ResultFilename, job.ResultType, job.ResultArtifactID = artifact.Filename, artifact.ContentType, artifact.ID
		return job, artifact.Content, nil
	}
	_, chunks, err := s.Download(ctx, jobID, principal)
	if err != nil {
		return recordmodel.RecordBatchJob{}, nil, err
	}
	readers := make([]io.Reader, len(chunks))
	for i := range chunks {
		readers[i] = strings.NewReader(chunks[i].Content)
	}
	return job, io.NopCloser(io.MultiReader(readers...)), nil
}

func exchangeScope(principal principalmodel.Principal) exchangecontract.Scope {
	return exchangecontract.Scope{WorkspaceID: principal.WorkspaceID, ActorID: principal.UserID, RoleKey: principal.RoleKey, RequestID: principal.RequestID}
}
func recordJobFromExchange(job exchangecontract.Job) recordmodel.RecordBatchJob {
	return recordmodel.RecordBatchJob{ID: job.ID, WorkspaceID: job.WorkspaceID, Kind: job.Operation, ObjectKey: job.ObjectKey, ActorID: job.ActorID, RoleKey: job.RoleKey, Status: job.Status, Checkpoint: job.Checkpoint, Total: job.Total, ResultArtifactID: job.ArtifactID, ErrorCode: job.ErrorCode, CreatedAt: job.CreatedAt.Format(time.RFC3339Nano), UpdatedAt: job.UpdatedAt.Format(time.RFC3339Nano)}
}

func (s *RecordBatchJobApplicationService) admitEnqueue(ctx context.Context, workspaceID string) error {
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "record batch admission capacity")
	depth, _, err := s.dependencies.Store.GlobalRecordBatchJobQueueStats(ctx, scope)
	if err != nil {
		return err
	}
	if depth >= s.dependencies.QueueLimit {
		return apperror.New(apperror.KindUnavailable, "capacity.record_batch_queue_exhausted", nil, map[string]string{"dimension": "process"})
	}
	workspaceDepth, _, err := s.dependencies.Store.RecordBatchJobQueueStats(ctx, workspaceID)
	if err != nil {
		return err
	}
	if workspaceDepth >= s.dependencies.WorkspaceLimit {
		return apperror.New(apperror.KindRateLimited, "capacity.record_batch_workspace_queue_exhausted", nil, map[string]string{"dimension": "workspace"})
	}
	return nil
}

func (s *RecordBatchJobApplicationService) preflightEnqueue(ctx context.Context, workspaceID, kind, objectKey, key, payload string) (recordmodel.RecordBatchJob, bool, error) {
	existing, found, err := s.dependencies.Store.FindRecordBatchJobByIdempotency(ctx, workspaceID, kind, strings.TrimSpace(objectKey), strings.TrimSpace(key))
	if err != nil || !found {
		return recordmodel.RecordBatchJob{}, false, err
	}
	if existing.PayloadJSON != payload {
		return recordmodel.RecordBatchJob{}, false, apperror.New(apperror.KindConflict, idempotency.ErrorCodeKeyReused, recordcontract.ErrRecordBatchJobIdempotencyConflict, map[string]string{"use_case": "record." + kind + ".async"})
	}
	return existing, true, nil
}

func (s *RecordBatchJobApplicationService) preflightExportEnqueue(ctx context.Context, workspaceID, objectKey, key, requestPayload string) (recordmodel.RecordBatchJob, bool, error) {
	existing, found, err := s.dependencies.Store.FindRecordBatchJobByIdempotency(ctx, workspaceID, "export", strings.TrimSpace(objectKey), strings.TrimSpace(key))
	if err != nil || !found {
		return recordmodel.RecordBatchJob{}, false, err
	}
	var stored recordBatchExportPayload
	if err := json.Unmarshal([]byte(existing.PayloadJSON), &stored); err != nil {
		return recordmodel.RecordBatchJob{}, false, apperror.New(apperror.KindConflict, idempotency.ErrorCodeKeyReused, err, map[string]string{"use_case": "record.export.async"})
	}
	stored.AssuranceEvidence = nil
	// stored was decoded into a JSON-only contract, so normalization cannot fail.
	normalized, _ := json.Marshal(stored)
	if string(normalized) != requestPayload {
		return recordmodel.RecordBatchJob{}, false, apperror.New(apperror.KindConflict, idempotency.ErrorCodeKeyReused, recordcontract.ErrRecordBatchJobIdempotencyConflict, map[string]string{"use_case": "record.export.async"})
	}
	return existing, true, nil
}
