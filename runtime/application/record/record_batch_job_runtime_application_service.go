package record

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"

	exchangecontract "github.com/domainry/domainry-data-exchange-sdk"
	identitysdk "github.com/domainry/domainry-identity-sdk"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/logging"
	"github.com/domainry/domainry-foundation/requestcontext"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	"github.com/domainry/domainry-runtime/pkg/dataexchange"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func (s *RecordBatchJobApplicationService) wake(job recordmodel.RecordBatchJob) {
	if s == nil || s.dependencies.WorkerWakeups == nil || strings.TrimSpace(job.ID) == "" {
		return
	}
	s.dependencies.WorkerWakeups.Publish(workerplatform.DurableTaskLocator{QueueKind: "record_batch", WorkspaceID: job.WorkspaceID, TaskID: job.ID})
}

// RecordBatchTerminalNotificationCommitter atomically persists a batch-job
// terminal transition and its already compiled Notification Event intent.
type RecordBatchTerminalNotificationCommitter interface {
	CommitRecordBatchTerminal(context.Context, recordmodel.RecordBatchJob, string, time.Time, notificationmodel.NotificationEvent) error
	CancelRecordBatchJob(context.Context, string, string, notificationmodel.NotificationEvent) (recordmodel.RecordBatchJob, bool, bool, error)
}

func (s *RecordBatchJobApplicationService) Cancel(ctx context.Context, jobID string, principal principalmodel.Principal) (recordmodel.RecordBatchJob, error) {
	if err := recordAuthorizeCommand(principal); err != nil {
		return recordmodel.RecordBatchJob{}, err
	}
	if s == nil || s.dependencies.Store == nil {
		return recordmodel.RecordBatchJob{}, apperror.New(apperror.KindInternal, "backend.record_batch.unavailable", nil, nil)
	}
	if s.dependencies.NotificationCompiler == nil {
		job, found, err := s.dependencies.Store.CancelRecordBatchJob(ctx, principal.WorkspaceID, strings.TrimSpace(jobID))
		if err != nil {
			return recordmodel.RecordBatchJob{}, err
		}
		if !found {
			return recordmodel.RecordBatchJob{}, apperror.New(apperror.KindNotFound, "backend.record_batch.not_found", nil, nil)
		}
		if job.Status == "cancelled" {
			s.cancelled.Add(1)
			s.audit(ctx, "record_batch_job_cancelled", job, principal, "Cancelled record batch job")
		}
		return job, nil
	}
	jobID = strings.TrimSpace(jobID)
	current, found, err := s.dependencies.Store.GetRecordBatchJob(ctx, principal.WorkspaceID, jobID)
	if err != nil {
		return recordmodel.RecordBatchJob{}, err
	}
	if !found {
		return recordmodel.RecordBatchJob{}, apperror.New(apperror.KindNotFound, "backend.record_batch.not_found", nil, nil)
	}
	job, changed, err := s.cancelWithNotification(ctx, current)
	if err != nil {
		return recordmodel.RecordBatchJob{}, err
	}
	if changed {
		s.cancelled.Add(1)
		s.audit(ctx, "record_batch_job_cancelled", job, principal, "Cancelled record batch job")
	}
	return job, nil
}

func (s *RecordBatchJobApplicationService) processImport(ctx context.Context, job *recordmodel.RecordBatchJob, principal principalmodel.Principal) error {
	var payload recordBatchImportPayload
	if err := json.Unmarshal([]byte(job.PayloadJSON), &payload); err != nil {
		return apperror.New(apperror.KindBadRequest, "backend.record_batch.payload_invalid", err, nil)
	}
	openSource, sourceBytes, sourceSHA256, err := s.recordBatchImportSource(ctx, *job, payload)
	if err != nil {
		return err
	}
	provider := newLegacyRecordDataExchangeImportProvider(s.dependencies.Importer, principal)
	result, err := dataexchange.ProcessCSVImport(ctx, provider, dataexchange.ImportEngineRequest{
		Batch: dataexchange.ImportBatch{
			Scope:     dataexchange.Scope{WorkspaceID: principal.WorkspaceID, ActorID: principal.UserID, RoleKey: principal.RoleKey, RequestID: job.ID},
			ObjectKey: job.ObjectKey, JobID: job.ID,
		},
		Open:      openSource,
		Limits:    dataexchange.CSVDecodeLimits{MaxBytes: sourceBytes, MaxRows: 1_000_000, MaxColumns: recordImportMaxColumns},
		BatchSize: recordImportMaxRows / 2,
		OnValidated: func(ctx context.Context, validation dataexchange.ImportEngineResult) error {
			if sourceSHA256 != "" && (validation.SHA256 != sourceSHA256 || validation.Bytes != sourceBytes) {
				return apperror.New(apperror.KindConflict, "backend.record_batch.source_integrity_failed", nil, nil)
			}
			job.Total = validation.Validated + validation.Rejected
			return s.dependencies.Store.SaveRecordBatchJobCheckpoint(ctx, *job, s.dependencies.Worker.Clock.Now())
		},
	})
	if err != nil {
		return err
	}
	job.Total = result.Validated + result.Rejected
	if result.Rejected > 0 {
		return recordImportError(apperror.KindBadRequest, "backend.import.invalid_rows", nil)
	}
	job.Checkpoint = result.Applied
	return nil
}

func (s *RecordBatchJobApplicationService) recordBatchImportSource(ctx context.Context, job recordmodel.RecordBatchJob, payload recordBatchImportPayload) (dataexchange.ImportSourceOpener, int64, string, error) {
	if payload.CSV != "" {
		value := payload.CSV
		return func(context.Context) (io.ReadCloser, error) { return io.NopCloser(strings.NewReader(value)), nil }, int64(len(value)), "", nil
	}
	if payload.SourceBytes < 0 || payload.SourceChunks < 0 || strings.TrimSpace(payload.SourceSHA256) == "" {
		return nil, 0, "", apperror.New(apperror.KindBadRequest, "backend.record_batch.source_invalid", nil, nil)
	}
	if sourceReader, ok := s.dependencies.Store.(recordcontract.RecordBatchJobSourceReader); ok {
		return func(openCtx context.Context) (io.ReadCloser, error) {
			return sourceReader.OpenRecordBatchJobSource(openCtx, job.WorkspaceID, job.ID, payload.SourceChunks)
		}, int64(payload.SourceBytes), strings.TrimSpace(payload.SourceSHA256), nil
	}
	chunks, err := s.dependencies.Store.ListRecordBatchJobChunks(ctx, job.WorkspaceID, job.ID)
	if err != nil {
		return nil, 0, "", err
	}
	if len(chunks) != payload.SourceChunks {
		return nil, 0, "", apperror.New(apperror.KindConflict, "backend.record_batch.source_incomplete", nil, map[string]string{"expected_chunks": strconv.Itoa(payload.SourceChunks), "actual_chunks": strconv.Itoa(len(chunks))})
	}
	for index, chunk := range chunks {
		if chunk.Sequence != index {
			return nil, 0, "", apperror.New(apperror.KindConflict, "backend.record_batch.source_invalid", nil, nil)
		}
	}
	return func(context.Context) (io.ReadCloser, error) {
		readers := make([]io.Reader, 0, len(chunks))
		for _, chunk := range chunks {
			readers = append(readers, strings.NewReader(chunk.Content))
		}
		return io.NopCloser(io.MultiReader(readers...)), nil
	}, int64(payload.SourceBytes), strings.TrimSpace(payload.SourceSHA256), nil
}

func (s *RecordBatchJobApplicationService) processExport(ctx context.Context, job *recordmodel.RecordBatchJob, principal principalmodel.Principal) error {
	var payload recordBatchExportPayload
	if err := json.Unmarshal([]byte(job.PayloadJSON), &payload); err != nil {
		return apperror.New(apperror.KindBadRequest, "backend.record_batch.payload_invalid", err, nil)
	}
	if _, ok := s.dependencies.Store.(recordcontract.RecordBatchJobPageCommitter); ok {
		return s.processPagedExport(ctx, job, principal, payload)
	}
	content, filename, err := s.dependencies.Exporter.exportWithVerifiedAssurance(ctx, job.ObjectKey, principal, payload.Options, payload.AssuranceEvidence)
	if err != nil {
		return err
	}
	chunks := make([]recordmodel.RecordBatchJobChunk, 0, (len(content)+recordBatchResultChunkBytes-1)/recordBatchResultChunkBytes)
	for offset := 0; offset < len(content); offset += recordBatchResultChunkBytes {
		if err := ctx.Err(); err != nil {
			return err
		}
		end := offset + recordBatchResultChunkBytes
		if end > len(content) {
			end = len(content)
		}
		chunks = append(chunks, recordmodel.RecordBatchJobChunk{WorkspaceID: job.WorkspaceID, JobID: job.ID, Sequence: len(chunks), Content: string(content[offset:end])})
	}
	if err := s.dependencies.Store.ReplaceRecordBatchJobChunks(ctx, *job, chunks, s.dependencies.Worker.Clock.Now()); err != nil {
		return err
	}
	job.ResultFilename, job.ResultType, job.ResultChunks = filename, "text/csv; charset=utf-8", len(chunks)
	job.Checkpoint, job.Total = bytesCountCSVRows(content), bytesCountCSVRows(content)
	return nil
}

const recordExportPageCursorPrefix = "record-export-page:"

// processPagedExport never materializes the complete result. Each authorized
// Record page is encoded into one bounded CSV chunk and committed atomically
// with the next source cursor. Retries resume after the last durable page.
func (s *RecordBatchJobApplicationService) processPagedExport(ctx context.Context, job *recordmodel.RecordBatchJob, principal principalmodel.Principal, payload recordBatchExportPayload) error {
	object, fields, evidence, err := s.dependencies.Exporter.prepareExport(ctx, job.ObjectKey, principal, payload.Options, payload.AssuranceEvidence, true)
	if err != nil {
		return err
	}
	prepared := recordExportPrepared{object: object, fields: fields, evidence: evidence, options: payload.Options, principal: principal}
	pageNumber, err := recordExportPageNumber(job.CheckpointCursor)
	if err != nil {
		return apperror.New(apperror.KindConflict, "backend.record_batch.checkpoint_invalid", err, nil)
	}
	job.ResultFilename, job.ResultType = object.Key+".csv", "text/csv; charset=utf-8"
	writer := recordBatchPageWriter{service: s}
	processed := job.Checkpoint
	for {
		page, err := s.dependencies.Exporter.encodeExportPage(ctx, prepared, pageNumber, job.ResultChunks == 0, recordBatchResultChunkBytes)
		if err != nil {
			return err
		}
		if processed+page.rows > recordExportMaxRows {
			return recordExportError(apperror.KindBadRequest, "backend.export.too_many_records", nil, "limit", fmt.Sprint(recordExportMaxRows))
		}
		processed += page.rows
		nextCursor := ""
		if page.hasNext {
			nextCursor = recordExportPageCursorPrefix + strconv.Itoa(pageNumber+1)
		}
		if err := writer.CommitPage(ctx, job, string(page.content), nextCursor, processed, processed); err != nil {
			return err
		}
		if !page.hasNext {
			return nil
		}
		pageNumber++
	}
}

func recordExportPageNumber(cursor string) (int, error) {
	cursor = strings.TrimSpace(cursor)
	if cursor == "" {
		return 1, nil
	}
	if !strings.HasPrefix(cursor, recordExportPageCursorPrefix) {
		return 0, fmt.Errorf("unsupported Record export cursor")
	}
	page, err := strconv.Atoi(strings.TrimPrefix(cursor, recordExportPageCursorPrefix))
	if err != nil || page < 1 {
		return 0, fmt.Errorf("invalid Record export cursor")
	}
	return page, nil
}

func (s *RecordBatchJobApplicationService) resolvePrincipal(ctx context.Context, job recordmodel.RecordBatchJob) principalmodel.Principal {
	principal := principalmodel.Principal{Principal: identitysdk.Principal{Known: false}}
	if s.dependencies.ResolvePrincipal != nil {
		// Identity authorization is workspace-scoped. Queue workers do not enter
		// through HTTP middleware, so restore the persisted job workspace before
		// resolving the current project user-role assignment.
		principal = s.dependencies.ResolvePrincipal(requestcontext.WithWorkspaceID(ctx, job.WorkspaceID), job.ActorID, job.RoleKey)
	}
	principal.WorkspaceID, principal.UserID, principal.RequestID = job.WorkspaceID, job.ActorID, job.ID
	// Do not synthesize authorization facts here. Report export notifications
	// are delivered on Business Workspace, but Product Surface is not the
	// principal's optional business-profile SurfaceKey. The persisted Identity
	// resolver must reproduce the prepare principal exactly before the Report
	// owner validates its frozen authorization-scope hash.
	return principal
}

func (s *RecordBatchJobApplicationService) cancellationContext(parent context.Context, job recordmodel.RecordBatchJob) (context.Context, func()) {
	return s.cancellationContextWithIntervals(parent, job, s.cancelPollInterval, s.heartbeatInterval)
}

func (s *RecordBatchJobApplicationService) cancellationContextWithIntervals(parent context.Context, job recordmodel.RecordBatchJob, cancelPollInterval, heartbeatInterval time.Duration) (context.Context, func()) {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	var once sync.Once
	go func() {
		defer close(done)
		cancelPoll := time.NewTicker(cancelPollInterval)
		heartbeat := time.NewTicker(heartbeatInterval)
		defer cancelPoll.Stop()
		defer heartbeat.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-cancelPoll.C:
				current, found, err := s.dependencies.Store.GetRecordBatchJob(ctx, job.WorkspaceID, job.ID)
				if err == nil && found && current.Status == "cancelled" {
					cancel()
					return
				}
			case <-heartbeat.C:
				if err := s.dependencies.Store.HeartbeatRecordBatchJob(ctx, job, time.Minute, s.dependencies.Worker.Clock.Now()); err != nil {
					cancel()
					return
				}
			}
		}
	}()
	return ctx, func() {
		once.Do(cancel)
		<-done
	}
}

func recordBatchRetryable(err error) bool {
	return workerplatform.ClassifyApplicationError(err).Retryable()
}

func (s *RecordBatchJobApplicationService) terminalNotification(job recordmodel.RecordBatchJob, status string, now time.Time) (notificationmodel.NotificationEvent, bool, error) {
	if strings.TrimSpace(job.ActorID) == "" || s.dependencies.NotificationCompiler == nil {
		return notificationmodel.NotificationEvent{}, false, nil
	}
	eventStatus := status
	if status == "quarantined" {
		eventStatus = "failed"
	}
	prefix := "record-batch:"
	if job.Kind == "report_export" {
		prefix = "report-export:"
	}
	variables := map[string]any{
		"job_kind": job.Kind, "object_key": job.ObjectKey, "status": eventStatus, "total": job.Total, "error_code": job.ErrorCode,
		"job_id": job.ID, "audit_id": job.AuditID, "artifact_id": job.ResultArtifactID,
	}
	intent := notificationmodel.NotificationIntent{
		ID: prefix + job.ID + ":" + eventStatus, WorkspaceID: job.WorkspaceID, SourceEventID: prefix + job.ID + ":" + eventStatus, EventType: "record.batch." + eventStatus,
		Surface: "business_workspace", RecipientUserIDs: []string{job.ActorID}, SubjectType: "record_batch_job", SubjectID: job.ID,
		DedupeKey: prefix + job.ID + ":" + eventStatus, ActionState: notificationmodel.NotificationActionOpen,
		OccurredAt: now.UTC().Format(time.RFC3339Nano), Variables: variables,
	}
	event, err := s.dependencies.NotificationCompiler(intent)
	return event, true, err
}

func (s *RecordBatchJobApplicationService) commitTerminal(ctx context.Context, job recordmodel.RecordBatchJob, status string, now time.Time) error {
	event, notify, err := s.terminalNotification(job, status, now)
	if err != nil {
		return err
	}
	if notify {
		if s.dependencies.NotificationCommitter == nil {
			return apperror.New(apperror.KindInternal, "backend.record_batch.notification_committer_unavailable", nil, nil)
		}
		return s.dependencies.NotificationCommitter.CommitRecordBatchTerminal(ctx, job, status, now, event)
	}
	switch status {
	case "completed":
		return s.dependencies.Store.CompleteRecordBatchJob(ctx, job, now)
	case "quarantined":
		return s.dependencies.Store.QuarantineRecordBatchJob(ctx, job, now)
	default:
		return s.dependencies.Store.FailRecordBatchJob(ctx, job, now)
	}
}

func (s *RecordBatchJobApplicationService) cancelWithNotification(ctx context.Context, job recordmodel.RecordBatchJob) (recordmodel.RecordBatchJob, bool, error) {
	if job.Status == "cancelled" {
		return job, false, nil
	}
	now := s.dependencies.Worker.Clock.Now()
	event, notify, err := s.terminalNotification(job, "cancelled", now)
	if err != nil {
		return recordmodel.RecordBatchJob{}, false, err
	}
	if notify {
		if s.dependencies.NotificationCommitter == nil {
			return recordmodel.RecordBatchJob{}, false, apperror.New(apperror.KindInternal, "backend.record_batch.notification_committer_unavailable", nil, nil)
		}
		updated, found, changed, commitErr := s.dependencies.NotificationCommitter.CancelRecordBatchJob(ctx, job.WorkspaceID, job.ID, event)
		if !found && commitErr == nil {
			commitErr = apperror.New(apperror.KindNotFound, "backend.record_batch.not_found", nil, nil)
		}
		return updated, changed, commitErr
	}
	updated, found, err := s.dependencies.Store.CancelRecordBatchJob(ctx, job.WorkspaceID, job.ID)
	return updated, found && updated.Status == "cancelled" && job.Status != "cancelled", err
}

func (s *RecordBatchJobApplicationService) audit(ctx context.Context, event string, job recordmodel.RecordBatchJob, principal principalmodel.Principal, summary string) {
	if s.dependencies.Audit != nil {
		s.dependencies.Audit(ctx, event, job.ObjectKey, job.ID, principal, summary, nil, map[string]any{"status": job.Status}, map[string]any{"job_kind": job.Kind, "attempt_count": job.AttemptCount})
	}
}

func (s *RecordBatchJobApplicationService) StartWorker(ctx context.Context, interval time.Duration, limit int) <-chan struct{} {
	if s != nil && s.dependencies.DataExchange != nil {
		return s.dependencies.DataExchange.Start(ctx, exchangecontract.WorkerConfig{Enabled: true, PollInterval: interval, BatchSize: limit, LeaseTTL: time.Minute})
	}
	if s == nil || s.dependencies.Store == nil {
		return workerplatform.Stopped()
	}
	if interval <= 0 {
		interval = time.Second
	}
	if limit <= 0 {
		limit = 10
	}
	return workerplatform.StartWakeableAdaptiveLoop(ctx, "record_batch", interval, 5*interval, s.wakeups, func() bool {
		worked := false
		s.dependencies.Worker.Control.RunIfAccepting(func() {
			var err error
			worked, err = s.processDue(ctx, limit)
			if err != nil && ctx.Err() == nil {
				logging.FromContext(ctx).Error("record batch worker failed", logging.StableErrorFields(err)...)
			}
		})
		return worked
	})
}

func (s *RecordBatchJobApplicationService) ProcessDue(ctx context.Context, limit int) error {
	_, err := s.processDue(ctx, limit)
	return err
}

func (s *RecordBatchJobApplicationService) processDue(ctx context.Context, limit int) (bool, error) {
	if s == nil || s.dependencies.Store == nil {
		return false, nil
	}
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "record batch worker queue metrics")
	if depth, oldest, statsErr := s.dependencies.Store.GlobalRecordBatchJobQueueStats(ctx, scope); statsErr == nil {
		s.queueDepth.Store(int64(depth))
		s.oldestMillis.Store(oldest.Milliseconds())
	}
	jobs, err := s.dependencies.Store.ClaimRecordBatchJobs(ctx, limit, s.dependencies.Worker.WorkerID.String(), time.Minute, s.dependencies.Worker.Clock.Now())
	if err != nil {
		return false, err
	}
	for _, job := range jobs {
		if err := ctx.Err(); err != nil {
			return len(jobs) > 0, err
		}
		jobCtx := requestcontext.WithActorID(requestcontext.WithWorkspaceID(ctx, job.WorkspaceID), job.ActorID)
		s.inFlight.Add(1)
		err := s.processClaimed(jobCtx, job)
		s.inFlight.Add(-1)
		if err != nil {
			return true, err
		}
	}
	return len(jobs) > 0, nil
}

func (s *RecordBatchJobApplicationService) processClaimed(ctx context.Context, job recordmodel.RecordBatchJob) error {
	jobCtx, stop := s.cancellationContext(ctx, job)
	defer stop()
	principal := s.resolvePrincipal(jobCtx, job)
	var err error
	switch job.Kind {
	case "import":
		err = s.processImport(jobCtx, &job, principal)
	case "export":
		err = s.processExport(jobCtx, &job, principal)
	default:
		s.processorsMu.RLock()
		processor := s.processors[job.Kind]
		s.processorsMu.RUnlock()
		if processor == nil {
			err = apperror.New(apperror.KindBadRequest, "backend.record_batch.kind_invalid", nil, nil)
		} else {
			err = processor(jobCtx, &job, principal, recordBatchPageWriter{service: s})
		}
	}
	if err == nil {
		if err = s.commitTerminal(ctx, job, "completed", s.dependencies.Worker.Clock.Now()); err == nil {
			s.completed.Add(1)
		}
		return err
	}
	if ctx.Err() != nil || jobCtx.Err() != nil {
		return err
	}
	job.ErrorCode = apperror.CodeOf(err)
	if job.ErrorCode == "" {
		job.ErrorCode = "backend.record_batch.failed"
	}
	retryable := recordBatchRetryable(err)
	if job.AttemptCount < 3 && retryable {
		delay := time.Second * time.Duration(1<<(job.AttemptCount-1))
		delay += s.dependencies.Worker.Jitter.Duration(250 * time.Millisecond)
		now := s.dependencies.Worker.Clock.Now()
		if saveErr := s.dependencies.Store.RetryRecordBatchJob(ctx, job, now.Add(delay), now); saveErr != nil {
			return saveErr
		}
		s.retried.Add(1)
		return nil
	}
	if !retryable {
		if saveErr := s.commitTerminal(ctx, job, "quarantined", s.dependencies.Worker.Clock.Now()); saveErr != nil {
			return saveErr
		}
		s.quarantined.Add(1)
		return nil
	}
	if saveErr := s.commitTerminal(ctx, job, "failed", s.dependencies.Worker.Clock.Now()); saveErr != nil {
		return saveErr
	}
	s.failed.Add(1)
	return nil
}
