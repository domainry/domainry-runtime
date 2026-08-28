package record

import (
	"context"
	"errors"
	"testing"
	"time"

	notificationmodel "github.com/domainry/domainry-runtime/runtime/domain/notification/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type recordBatchNotificationCommitterProbe struct {
	commitErr   error
	cancelJob   recordmodel.RecordBatchJob
	cancelFound bool
	cancelled   bool
	cancelErr   error
	commits     int
}

func (p *recordBatchNotificationCommitterProbe) CommitRecordBatchTerminal(context.Context, recordmodel.RecordBatchJob, string, time.Time, notificationmodel.NotificationEvent) error {
	p.commits++
	return p.commitErr
}

func (p *recordBatchNotificationCommitterProbe) CancelRecordBatchJob(context.Context, string, string, notificationmodel.NotificationEvent) (recordmodel.RecordBatchJob, bool, bool, error) {
	return p.cancelJob, p.cancelFound, p.cancelled, p.cancelErr
}

func TestRecordBatchCancelNotificationLookupOutcomes(t *testing.T) {
	principal := recordBatchPrincipal()
	compiler := func(intent notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		return notificationmodel.NotificationEvent{EventType: intent.EventType}, nil
	}
	for name, store := range map[string]*recordBatchRuntimeStore{
		"lookup error": {recordBatchStoreProbe: &recordBatchStoreProbe{}, getErr: errors.New("lookup failed")},
		"not found":    {recordBatchStoreProbe: &recordBatchStoreProbe{jobs: map[string]recordmodel.RecordBatchJob{}}},
	} {
		t.Run(name, func(t *testing.T) {
			service := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{Store: store, NotificationCompiler: compiler})
			if _, err := service.Cancel(t.Context(), "job", principal); err == nil {
				t.Fatal("expected cancel error")
			}
		})
	}
	job := recordmodel.RecordBatchJob{ID: "job", WorkspaceID: principal.WorkspaceID, ActorID: principal.UserID, Status: "running"}
	store := &recordBatchStoreProbe{jobs: map[string]recordmodel.RecordBatchJob{job.ID: job}}
	committer := &recordBatchNotificationCommitterProbe{cancelJob: recordmodel.RecordBatchJob{ID: job.ID, Status: "cancelled"}, cancelFound: true, cancelled: true}
	service := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{Store: store, NotificationCompiler: compiler, NotificationCommitter: committer})
	result, err := service.Cancel(t.Context(), " job ", principal)
	if err != nil || result.Status != "cancelled" || service.cancelled.Load() != 1 {
		t.Fatalf("result=%+v cancelled=%d err=%v", result, service.cancelled.Load(), err)
	}
	compilerFailure := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{
		Store: store,
		NotificationCompiler: func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
			return notificationmodel.NotificationEvent{}, errors.New("compile failed")
		},
	})
	if _, err := compilerFailure.Cancel(t.Context(), job.ID, principal); err == nil {
		t.Fatal("compiler failure ignored by Cancel")
	}
	unchangedCommitter := &recordBatchNotificationCommitterProbe{cancelJob: job, cancelFound: true}
	unchanged := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{Store: store, NotificationCompiler: compiler, NotificationCommitter: unchangedCommitter})
	if result, err := unchanged.Cancel(t.Context(), job.ID, principal); err != nil || result.Status != "running" || unchanged.cancelled.Load() != 0 {
		t.Fatalf("result=%+v cancelled=%d err=%v", result, unchanged.cancelled.Load(), err)
	}
}

func TestRecordBatchTerminalNotificationAndCommitEdges(t *testing.T) {
	job := recordmodel.RecordBatchJob{ID: "job", WorkspaceID: "workspace", ActorID: "actor", Status: "running"}
	store := &recordBatchStoreProbe{jobs: map[string]recordmodel.RecordBatchJob{job.ID: job}}
	service := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{Store: store})
	if _, notify, err := service.terminalNotification(job, "completed", time.Now()); err != nil || notify {
		t.Fatalf("nil compiler notify=%v err=%v", notify, err)
	}
	compiledStatus := ""
	service.dependencies.NotificationCompiler = func(intent notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		compiledStatus = intent.Variables["status"].(string)
		return notificationmodel.NotificationEvent{EventType: intent.EventType}, nil
	}
	if _, notify, err := service.terminalNotification(job, "quarantined", time.Now()); err != nil || !notify || compiledStatus != "failed" {
		t.Fatalf("status=%q notify=%v err=%v", compiledStatus, notify, err)
	}
	if err := service.commitTerminal(t.Context(), job, "completed", time.Now()); apperror.CodeOf(err) != "backend.record_batch.notification_committer_unavailable" {
		t.Fatalf("missing committer err=%v", err)
	}
	committer := &recordBatchNotificationCommitterProbe{}
	service.dependencies.NotificationCommitter = committer
	if err := service.commitTerminal(t.Context(), job, "completed", time.Now()); err != nil || committer.commits != 1 {
		t.Fatalf("commits=%d err=%v", committer.commits, err)
	}
	committer.commitErr = errors.New("commit failed")
	if err := service.commitTerminal(t.Context(), job, "failed", time.Now()); err == nil {
		t.Fatal("commit failure ignored")
	}
	service.dependencies.NotificationCompiler = func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		return notificationmodel.NotificationEvent{}, errors.New("compile failed")
	}
	if err := service.commitTerminal(t.Context(), job, "failed", time.Now()); err == nil {
		t.Fatal("terminal notification compiler failure ignored")
	}
}

func TestReportExportTerminalNotificationIsRequesterOnlyAndDeduplicated(t *testing.T) {
	job := recordmodel.RecordBatchJob{ID: "report-job", WorkspaceID: "workspace", ActorID: "requester", Kind: "report_export", AuditID: "audit-1", ResultArtifactID: "artifact-1", Status: "running"}
	var intent notificationmodel.NotificationIntent
	service := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{NotificationCompiler: func(value notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		intent = value
		return notificationmodel.NotificationEvent{EventType: value.EventType}, nil
	}})
	if _, notify, err := service.terminalNotification(job, "completed", time.Now()); err != nil || !notify {
		t.Fatalf("notify=%v err=%v", notify, err)
	}
	if intent.Surface != "business_workspace" || len(intent.RecipientUserIDs) != 1 || intent.RecipientUserIDs[0] != "requester" || intent.SourceEventID != "report-export:report-job:completed" || intent.DedupeKey != intent.SourceEventID || intent.SubjectType != "record_batch_job" || intent.SubjectID != job.ID {
		t.Fatalf("intent=%+v", intent)
	}
	if intent.Variables["job_id"] != job.ID || intent.Variables["audit_id"] != job.AuditID || intent.Variables["artifact_id"] != job.ResultArtifactID {
		t.Fatalf("notification linkage=%+v", intent.Variables)
	}
}

func TestRecordBatchCancelWithNotificationEdges(t *testing.T) {
	job := recordmodel.RecordBatchJob{ID: "job", WorkspaceID: "workspace", ActorID: "actor", Status: "running"}
	store := &recordBatchStoreProbe{jobs: map[string]recordmodel.RecordBatchJob{job.ID: job}}
	withoutNotification := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{Store: store})
	if result, changed, err := withoutNotification.cancelWithNotification(t.Context(), job); err != nil || !changed || result.Status != "cancelled" {
		t.Fatalf("store cancel result=%+v changed=%v err=%v", result, changed, err)
	}
	store.jobs[job.ID] = job
	compiler := func(intent notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		return notificationmodel.NotificationEvent{EventType: intent.EventType}, nil
	}
	service := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{Store: store, NotificationCompiler: compiler})
	already := job
	already.Status = "cancelled"
	if result, changed, err := service.cancelWithNotification(t.Context(), already); err != nil || changed || result.Status != "cancelled" {
		t.Fatalf("result=%+v changed=%v err=%v", result, changed, err)
	}
	if _, _, err := service.cancelWithNotification(t.Context(), job); apperror.CodeOf(err) != "backend.record_batch.notification_committer_unavailable" {
		t.Fatalf("missing committer err=%v", err)
	}
	for name, committer := range map[string]*recordBatchNotificationCommitterProbe{
		"not found":    {},
		"commit error": {cancelErr: errors.New("cancel commit failed")},
		"changed":      {cancelJob: recordmodel.RecordBatchJob{ID: "job", Status: "cancelled"}, cancelFound: true, cancelled: true},
		"unchanged":    {cancelJob: job, cancelFound: true},
	} {
		t.Run(name, func(t *testing.T) {
			service.dependencies.NotificationCommitter = committer
			result, changed, err := service.cancelWithNotification(t.Context(), job)
			switch name {
			case "not found":
				if apperror.KindOf(err) != apperror.KindNotFound {
					t.Fatalf("err=%v", err)
				}
			case "commit error":
				if err == nil {
					t.Fatal("commit error ignored")
				}
			case "changed":
				if err != nil || !changed || result.Status != "cancelled" {
					t.Fatalf("result=%+v changed=%v err=%v", result, changed, err)
				}
			case "unchanged":
				if err != nil || changed {
					t.Fatalf("changed=%v err=%v", changed, err)
				}
			}
		})
	}
	service.dependencies.NotificationCompiler = func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		return notificationmodel.NotificationEvent{}, errors.New("compile failed")
	}
	if _, _, err := service.cancelWithNotification(t.Context(), job); err == nil {
		t.Fatal("compiler failure ignored")
	}
}
