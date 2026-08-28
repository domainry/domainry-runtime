package record

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/requestcontext"
	workerplatform "github.com/domainry/domainry-runtime/runtime/platform/worker"
)

type recordBatchStoreProbe struct {
	jobs   map[string]recordmodel.RecordBatchJob
	chunks map[string][]recordmodel.RecordBatchJobChunk
}

func (p *recordBatchStoreProbe) EnqueueRecordBatchJob(_ context.Context, job recordmodel.RecordBatchJob) (recordmodel.RecordBatchJob, bool, error) {
	if p.jobs == nil {
		p.jobs = map[string]recordmodel.RecordBatchJob{}
	}
	job.ID, job.Status = fmt.Sprintf("job-%d", len(p.jobs)+1), "queued"
	p.jobs[job.ID] = job
	return job, false, nil
}
func (p *recordBatchStoreProbe) GetRecordBatchJob(_ context.Context, workspaceID, id string) (recordmodel.RecordBatchJob, bool, error) {
	job, ok := p.jobs[id]
	return job, ok && job.WorkspaceID == workspaceID, nil
}
func (p *recordBatchStoreProbe) FindRecordBatchJobByIdempotency(_ context.Context, workspaceID, kind, objectKey, key string) (recordmodel.RecordBatchJob, bool, error) {
	for _, job := range p.jobs {
		if job.WorkspaceID == workspaceID && job.Kind == kind && job.ObjectKey == objectKey && job.IdempotencyKey == key {
			return job, true, nil
		}
	}
	return recordmodel.RecordBatchJob{}, false, nil
}
func (p *recordBatchStoreProbe) ClaimRecordBatchJobs(_ context.Context, limit int, owner string, _ time.Duration, _ time.Time) ([]recordmodel.RecordBatchJob, error) {
	out := []recordmodel.RecordBatchJob{}
	for id, job := range p.jobs {
		if job.Status != "queued" || len(out) >= limit {
			continue
		}
		job.Status, job.LeaseOwner, job.FencingToken, job.AttemptCount = "running", owner, job.FencingToken+1, job.AttemptCount+1
		p.jobs[id] = job
		out = append(out, job)
	}
	return out, nil
}
func (p *recordBatchStoreProbe) SaveRecordBatchJobCheckpoint(_ context.Context, job recordmodel.RecordBatchJob, _ time.Time) error {
	p.jobs[job.ID] = job
	return nil
}
func (p *recordBatchStoreProbe) HeartbeatRecordBatchJob(context.Context, recordmodel.RecordBatchJob, time.Duration, time.Time) error {
	return nil
}
func (p *recordBatchStoreProbe) RetryRecordBatchJob(_ context.Context, job recordmodel.RecordBatchJob, _, _ time.Time) error {
	job.Status = "queued"
	p.jobs[job.ID] = job
	return nil
}
func (p *recordBatchStoreProbe) CompleteRecordBatchJob(_ context.Context, job recordmodel.RecordBatchJob, _ time.Time) error {
	job.Status = "completed"
	p.jobs[job.ID] = job
	return nil
}
func (p *recordBatchStoreProbe) FailRecordBatchJob(_ context.Context, job recordmodel.RecordBatchJob, _ time.Time) error {
	job.Status = "failed"
	p.jobs[job.ID] = job
	return nil
}
func (p *recordBatchStoreProbe) QuarantineRecordBatchJob(_ context.Context, job recordmodel.RecordBatchJob, _ time.Time) error {
	job.Status = "quarantined"
	p.jobs[job.ID] = job
	return nil
}
func (p *recordBatchStoreProbe) RequeueRecordBatchJob(_ context.Context, workspaceID, id string, _ time.Time) (recordmodel.RecordBatchJob, bool, error) {
	job, ok := p.jobs[id]
	if !ok || job.WorkspaceID != workspaceID || (job.Status != "failed" && job.Status != "quarantined") {
		return job, false, nil
	}
	job.Status, job.LeaseOwner, job.LeaseExpiresAt, job.FencingToken = "queued", "", "", job.FencingToken+1
	p.jobs[id] = job
	return job, true, nil
}
func (p *recordBatchStoreProbe) CancelRecordBatchJob(_ context.Context, workspaceID, id string) (recordmodel.RecordBatchJob, bool, error) {
	job, ok := p.jobs[id]
	if ok && job.WorkspaceID == workspaceID {
		job.Status = "cancelled"
		p.jobs[id] = job
	}
	return job, ok, nil
}
func (p *recordBatchStoreProbe) ReplaceRecordBatchJobChunks(_ context.Context, job recordmodel.RecordBatchJob, chunks []recordmodel.RecordBatchJobChunk, _ time.Time) error {
	if p.chunks == nil {
		p.chunks = map[string][]recordmodel.RecordBatchJobChunk{}
	}
	p.chunks[job.ID] = chunks
	return nil
}
func (p *recordBatchStoreProbe) ListRecordBatchJobChunks(_ context.Context, _, id string) ([]recordmodel.RecordBatchJobChunk, error) {
	return p.chunks[id], nil
}
func (p *recordBatchStoreProbe) RecordBatchJobQueueStats(context.Context, string) (int, time.Duration, error) {
	return 1, 2 * time.Second, nil
}
func (p *recordBatchStoreProbe) GlobalRecordBatchJobQueueStats(context.Context, principalmodel.SystemScope) (int, time.Duration, error) {
	return 1, 2 * time.Second, nil
}

func TestRecordBatchJobExportRunsOffHTTPWithDurableStatusAndChunks(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}
	exporter := NewRecordExportApplicationService(RecordExportDependencies{
		Repository: &exportRepositoryProbe{page: recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "c1", Data: map[string]any{"name": "Acme"}}}}},
		Objects: func() map[string]definitionmodel.ObjectSchema {
			return map[string]definitionmodel.ObjectSchema{"customer": object}
		},
		CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
	})
	store := &recordBatchStoreProbe{}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin"}}, accessfixture.Bundle{Key: "admin", Permissions: []string{"workspace.admin", "*"}, RecordScope: "all_records"})
	service := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{Store: store, Exporter: exporter, ResolvePrincipal: func(context.Context, string, string) principalmodel.Principal { return principal }})
	job, replayed, err := service.EnqueueExport(t.Context(), "customer", "request-1", RecordExportOptions{}, principal)
	if err != nil || replayed || job.Status != "queued" {
		t.Fatalf("enqueue=%+v replayed=%v err=%v", job, replayed, err)
	}
	if err := service.ProcessDue(t.Context(), 5); err != nil {
		t.Fatal(err)
	}
	completed, chunks, err := service.Download(t.Context(), job.ID, principal)
	if err != nil || completed.Status != "completed" || completed.Checkpoint != 1 || len(chunks) != 1 || !strings.Contains(chunks[0].Content, "Acme") {
		t.Fatalf("completed=%+v chunks=%+v err=%v", completed, chunks, err)
	}
	metrics := (&RecordApplicationService{batchJobs: service}).BatchJobOpenMetrics(t.Context())
	if !strings.Contains(metrics, `outcome="completed"} 1`) || !strings.Contains(metrics, "domainry_runtime_record_batch_queue_depth 1") {
		t.Fatalf("metrics=%s", metrics)
	}
	limited := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{Store: store, Exporter: exporter, QueueLimit: 1, WorkspaceLimit: 1})
	replayedJob, replayed, err := limited.EnqueueExport(t.Context(), "customer", "request-1", RecordExportOptions{}, principal)
	if err != nil || !replayed || replayedJob.ID != job.ID {
		t.Fatalf("full queue did not preserve idempotent replay: job=%+v replayed=%v err=%v", replayedJob, replayed, err)
	}
	if _, _, err := limited.EnqueueExport(t.Context(), "customer", "request-2", RecordExportOptions{}, principal); apperror.KindOf(err) != apperror.KindUnavailable || apperror.CodeOf(err) != "capacity.record_batch_queue_exhausted" {
		t.Fatalf("full queue error=%v", err)
	}
}

func TestRecordBatchEnqueuePublishesCommittedWorkerWakeup(t *testing.T) {
	broker := workerplatform.NewWakeupBroker()
	observed := broker.Subscribe("record_batch", 1)
	service := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{
		Store:         &recordBatchStoreProbe{},
		Exporter:      recordBatchExporter(),
		WorkerWakeups: broker,
	})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "admin"}}, accessfixture.Bundle{Key: "admin", Permissions: []string{"workspace.admin", "*"}, RecordScope: "all_records"})
	job, replayed, err := service.EnqueueExport(t.Context(), "customer", "wake-1", RecordExportOptions{}, principal)
	if err != nil || replayed {
		t.Fatalf("enqueue replayed=%v err=%v", replayed, err)
	}
	select {
	case locator := <-observed:
		if locator.WorkspaceID != job.WorkspaceID || locator.TaskID != job.ID {
			t.Fatalf("locator=%+v job=%+v", locator, job)
		}
	case <-time.After(time.Second):
		t.Fatal("record batch enqueue did not wake worker")
	}
}

func TestRecordBatchTerminalNotificationUsesTypedSafeIntent(t *testing.T) {
	intents := []notificationmodel.NotificationIntent{}
	service := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{NotificationCompiler: func(intent notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		intents = append(intents, intent)
		return notificationmodel.NotificationEvent{EventType: intent.EventType}, nil
	}})
	job := recordmodel.RecordBatchJob{ID: "job-1", WorkspaceID: "workspace-a", ActorID: "user-1", Kind: "export", ObjectKey: "customer", Total: 42}
	for _, status := range []string{"completed", "failed", "cancelled"} {
		if _, notify, err := service.terminalNotification(job, status, time.Now()); err != nil || !notify {
			t.Fatalf("status=%s notify=%v err=%v", status, notify, err)
		}
	}
	if len(intents) != 3 || intents[0].EventType != "record.batch.completed" || intents[0].RecipientUserIDs[0] != "user-1" || intents[0].SubjectType != "record_batch_job" || intents[0].Variables["total"] != 42 {
		t.Fatalf("intents=%+v", intents)
	}
	service.dependencies.NotificationCompiler = func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error) {
		return notificationmodel.NotificationEvent{}, errors.New("notification unavailable")
	}
	if _, _, err := service.terminalNotification(job, "failed", time.Now()); err == nil {
		t.Fatal("expected compiler failure")
	}
	job.ActorID = ""
	if _, notify, err := service.terminalNotification(job, "completed", time.Now()); err != nil || notify {
		t.Fatalf("empty actor notify=%v err=%v", notify, err)
	}
}

func TestReportExportWorkerPreservesResolvedAuthorizationSurface(t *testing.T) {
	resolved := principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "requester"}, SurfaceKey: "member_portal"}
	service := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{ResolvePrincipal: func(ctx context.Context, _, _ string) principalmodel.Principal {
		if requestcontext.WorkspaceID(ctx) != "workspace-a" {
			t.Fatalf("resolver workspace=%q", requestcontext.WorkspaceID(ctx))
		}
		return resolved
	}})
	reportPrincipal := service.resolvePrincipal(t.Context(), recordmodel.RecordBatchJob{ID: "report-job", WorkspaceID: "workspace-a", ActorID: "requester", Kind: "report_export"})
	if reportPrincipal.SurfaceKey != resolved.SurfaceKey {
		t.Fatalf("report worker surface=%q", reportPrincipal.SurfaceKey)
	}
	genericPrincipal := service.resolvePrincipal(t.Context(), recordmodel.RecordBatchJob{ID: "generic-job", WorkspaceID: "workspace-a", ActorID: "requester", Kind: "export"})
	if genericPrincipal.SurfaceKey != resolved.SurfaceKey {
		t.Fatalf("generic worker surface changed to %q", genericPrincipal.SurfaceKey)
	}
}

func TestRecordBatchExportConsumesAssuranceBeforeQueueWithoutPersistingToken(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}, ExportAssurancePolicy: &definitionmodel.ActionAssurancePolicy{RequiredMethods: []string{definitionmodel.ActionAssuranceOTP}}}
	validated := 0
	exporter := NewRecordExportApplicationService(RecordExportDependencies{
		Repository: &exportRepositoryProbe{page: recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "c1", Data: map[string]any{"name": "Acme"}}}}},
		Objects: func() map[string]definitionmodel.ObjectSchema {
			return map[string]definitionmodel.ObjectSchema{"customer": object}
		},
		ValidateAssurance: func(_ context.Context, _ definitionmodel.ObjectSchema, _ principalmodel.Principal, _ map[string]any, token string) (map[string]string, error) {
			validated++
			if token != "one-time-secret" {
				return nil, apperror.New(apperror.KindForbidden, "backend.action.assurance_token_invalid", nil, nil)
			}
			return map[string]string{"grant_id": "grant-async", "methods": "otp", "payload_digest": "digest-async"}, nil
		},
	})
	store := &recordBatchStoreProbe{}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "operator"}}, accessfixture.Bundle{Key: "admin", Permissions: []string{"workspace.admin", "*"}, RecordScope: "all_records"})
	service := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{Store: store, Exporter: exporter, ResolvePrincipal: func(context.Context, string, string) principalmodel.Principal { return principal }})
	options := RecordExportOptions{Fields: []string{"name"}, Reason: "audit", AssuranceToken: "one-time-secret"}
	job, replayed, err := service.EnqueueExport(t.Context(), object.Key, "export-assured-1", options, principal)
	if err != nil || replayed || validated != 1 {
		t.Fatalf("job=%+v replayed=%v validated=%d err=%v", job, replayed, validated, err)
	}
	stored := store.jobs[job.ID]
	if strings.Contains(stored.PayloadJSON, "one-time-secret") || !strings.Contains(stored.PayloadJSON, "grant-async") {
		t.Fatalf("unsafe queued payload=%s", stored.PayloadJSON)
	}
	if replayedJob, replayed, err := service.EnqueueExport(t.Context(), object.Key, "export-assured-1", options, principal); err != nil || !replayed || replayedJob.ID != job.ID || validated != 1 {
		t.Fatalf("replay=%+v replayed=%v validated=%d err=%v", replayedJob, replayed, validated, err)
	}
	if err := service.ProcessDue(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	completed, chunks, err := service.Download(t.Context(), job.ID, principal)
	if err != nil || completed.Status != "completed" || len(chunks) != 1 || !strings.Contains(chunks[0].Content, "Acme") || validated != 1 {
		t.Fatalf("completed=%+v chunks=%+v validated=%d err=%v", completed, chunks, validated, err)
	}
}

func TestRecordBatchJobQuarantinesTerminalPoisonPayload(t *testing.T) {
	store := &recordBatchStoreProbe{jobs: map[string]recordmodel.RecordBatchJob{
		"poison": {ID: "poison", WorkspaceID: "workspace-a", Kind: "unsupported", Status: "queued"},
	}}
	service := NewRecordBatchJobApplicationService(RecordBatchJobDependencies{Store: store})
	if err := service.ProcessDue(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	if got := store.jobs["poison"]; got.Status != "quarantined" || got.FencingToken != 1 || got.ErrorCode != "backend.record_batch.kind_invalid" {
		t.Fatalf("poison job was not quarantined with durable evidence: %#v", got)
	}
	metrics := (&RecordApplicationService{batchJobs: service}).BatchJobOpenMetrics(t.Context())
	if !strings.Contains(metrics, `outcome="quarantined"} 1`) {
		t.Fatalf("quarantine metric missing: %s", metrics)
	}
}
