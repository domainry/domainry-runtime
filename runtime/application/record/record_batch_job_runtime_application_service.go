package record

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/logging"
	"github.com/domainry/domainry-runtime/runtime/platform/requestcontext"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
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
	preview, err := s.dependencies.Importer.Preview(ctx, job.ObjectKey, []byte(payload.CSV), principal)
	if err != nil {
		return err
	}
	job.Total = len(preview.Rows)
	if err := s.dependencies.Store.SaveRecordBatchJobCheckpoint(ctx, *job, s.dependencies.Worker.Clock.Now()); err != nil {
		return err
	}
	result, _, err := s.dependencies.Importer.ApplyIdempotent(ctx, job.ObjectKey, []byte(payload.CSV), "record-batch:"+job.ID, principal)
	if err != nil {
		return err
	}
	job.Checkpoint = result.Created
	return nil
}

func (s *RecordBatchJobApplicationService) processExport(ctx context.Context, job *recordmodel.RecordBatchJob, principal principalmodel.Principal) error {
	var payload recordBatchExportPayload
	if err := json.Unmarshal([]byte(job.PayloadJSON), &payload); err != nil {
		return apperror.New(apperror.KindBadRequest, "backend.record_batch.payload_invalid", err, nil)
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
