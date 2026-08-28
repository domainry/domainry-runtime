package lifecycle

import (
	"context"
	"errors"
	"testing"
	"time"

	lifecyclecontract "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/contract"
	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type lifecycleCleanupExecutorProbe struct {
	preview func(context.Context, string, lifecyclemodel.PolicyVersion, time.Time) (lifecyclecontract.CleanupPreview, error)
	process func(context.Context, lifecyclemodel.CleanupJob, lifecyclemodel.PolicyVersion, []lifecyclemodel.LegalHold, int) (lifecyclemodel.CleanupBatchResult, error)
}

func (*lifecycleCleanupExecutorProbe) Owner(context.Context) string { return "integration" }
func (p *lifecycleCleanupExecutorProbe) Preview(ctx context.Context, workspaceID string, version lifecyclemodel.PolicyVersion, now time.Time) (lifecyclecontract.CleanupPreview, error) {
	if p.preview != nil {
		return p.preview(ctx, workspaceID, version, now)
	}
	return lifecyclecontract.CleanupPreview{}, nil
}
func (p *lifecycleCleanupExecutorProbe) ProcessBatch(ctx context.Context, job lifecyclemodel.CleanupJob, version lifecyclemodel.PolicyVersion, holds []lifecyclemodel.LegalHold, batchSize int) (lifecyclemodel.CleanupBatchResult, error) {
	if p.process != nil {
		return p.process(ctx, job, version, holds, batchSize)
	}
	return lifecyclemodel.CleanupBatchResult{}, nil
}

func newLifecycleCleanupProbe(t *testing.T, now time.Time) (*LifecycleApplicationService, *lifecycleRepositoryProbe, *lifecycleCleanupExecutorProbe, lifecyclemodel.PolicyVersion) {
	t.Helper()
	version := lifecycleValidPolicyVersion(now)
	repository := &lifecycleRepositoryProbe{latestPolicy: func(context.Context, string, string) (lifecyclemodel.PolicyVersion, bool, error) {
		return version, true, nil
	}}
	executor := &lifecycleCleanupExecutorProbe{}
	service := NewLifecycleApplicationService(t.Context(), LifecycleApplicationDependencies{Repository: repository, Executors: []lifecyclecontract.OwnerLifecycleExecutor{executor}})
	return service, repository, executor, version
}

func TestLifecyclePreviewAndCreateCleanupJobFailureBoundaries(t *testing.T) {
	now := time.Date(2026, 7, 19, 9, 10, 11, 0, time.UTC)
	service, repository, executor, version := newLifecycleCleanupProbe(t, now)
	admin := lifecycleAdmin("workspace-a", "cleanup-admin")
	previewFailure := errors.New("cleanup preview failed")
	executor.preview = func(context.Context, string, lifecyclemodel.PolicyVersion, time.Time) (lifecyclecontract.CleanupPreview, error) {
		return lifecyclecontract.CleanupPreview{}, previewFailure
	}
	if _, err := service.PreviewCleanup(t.Context(), "workspace-a", version.Policy.Key, admin, now); !errors.Is(err, previewFailure) {
		t.Fatalf("preview error = %v", err)
	}
	job := lifecyclemodel.CleanupJob{ID: "job-1", WorkspaceID: "workspace-a", PolicyKey: version.Policy.Key, Operation: lifecyclemodel.OperationPurge, Reason: "retention", CreatedAt: now}
	if _, err := service.CreateCleanupJob(t.Context(), job, admin); !errors.Is(err, previewFailure) {
		t.Fatalf("create preview error = %v", err)
	}
	executor.preview = func(_ context.Context, workspaceID string, received lifecyclemodel.PolicyVersion, receivedNow time.Time) (lifecyclecontract.CleanupPreview, error) {
		if workspaceID != job.WorkspaceID || received.Policy.Key != version.Policy.Key || receivedNow != now {
			t.Fatalf("workspace=%q version=%#v now=%v", workspaceID, received, receivedNow)
		}
		return lifecyclecontract.CleanupPreview{Rows: 3, Bytes: 40, OldestEligible: now.Add(-time.Hour)}, nil
	}
	invalid := job
	invalid.Operation = "unknown"
	if _, err := service.CreateCleanupJob(t.Context(), invalid, admin); err == nil {
		t.Fatal("invalid cleanup operation accepted")
	}
	saveFailure := errors.New("cleanup job save failed")
	repository.saveCleanupJob = func(context.Context, lifecyclemodel.CleanupJob) error { return saveFailure }
	if _, err := service.CreateCleanupJob(t.Context(), job, admin); !errors.Is(err, saveFailure) {
		t.Fatalf("save error = %v", err)
	}
	repository.saveCleanupJob = nil
	auditFailure := errors.New("cleanup creation audit failed")
	repository.appendAudit = func(context.Context, lifecyclemodel.AuditEvidence) error { return auditFailure }
	if _, err := service.CreateCleanupJob(t.Context(), job, admin); !errors.Is(err, auditFailure) {
		t.Fatalf("audit error = %v", err)
	}
	repository.appendAudit = nil
	created, err := service.CreateCleanupJob(t.Context(), job, admin)
	if err != nil || created.PolicyVersion != version.Policy.Version || created.RequestedBy != admin.UserID || created.Status != lifecyclemodel.CleanupStatusPending || created.EstimatedRows != 3 || created.EstimatedBytes != 40 || created.UpdatedAt != now {
		t.Fatalf("created=%#v err=%v", created, err)
	}
}

func TestLifecycleProcessCleanupJobClaimAndFailureConvergence(t *testing.T) {
	now := time.Date(2026, 7, 19, 10, 11, 12, 0, time.UTC)
	service, repository, executor, version := newLifecycleCleanupProbe(t, now)
	admin := lifecycleAdmin("workspace-a", "cleanup-admin")
	job := lifecyclemodel.CleanupJob{ID: "job-1", WorkspaceID: "workspace-a", PolicyKey: version.Policy.Key, Status: lifecyclemodel.CleanupStatusRunning, LeaseOwner: "worker", LeaseExpiresAt: now.Add(time.Minute)}
	claimFailure := errors.New("cleanup claim failed")
	repository.claimCleanupJob = func(context.Context, string, string, string, time.Duration, time.Time) (lifecyclemodel.CleanupJob, bool, error) {
		return job, false, claimFailure
	}
	if claimed, err := service.ProcessCleanupJob(t.Context(), "workspace-a", job.ID, "worker", time.Minute, 10, now, admin); claimed.ID != job.ID || !errors.Is(err, claimFailure) {
		t.Fatalf("claimed=%#v err=%v", claimed, err)
	}
	repository.claimCleanupJob = func(context.Context, string, string, string, time.Duration, time.Time) (lifecyclemodel.CleanupJob, bool, error) {
		return job, false, nil
	}
	if claimed, err := service.ProcessCleanupJob(t.Context(), "workspace-a", job.ID, "worker", time.Minute, 10, now, admin); claimed.ID != job.ID || err != nil {
		t.Fatalf("claimed=%#v err=%v", claimed, err)
	}
	repository.claimCleanupJob = func(context.Context, string, string, string, time.Duration, time.Time) (lifecyclemodel.CleanupJob, bool, error) {
		return job, true, nil
	}
	repository.latestPolicy = func(context.Context, string, string) (lifecyclemodel.PolicyVersion, bool, error) {
		return lifecyclemodel.PolicyVersion{}, false, nil
	}
	updateFailure := errors.New("failed cleanup update failed")
	repository.updateCleanupJob = func(context.Context, lifecyclemodel.CleanupJob) error { return updateFailure }
	failed, err := service.ProcessCleanupJob(t.Context(), "workspace-a", job.ID, "worker", time.Minute, 10, now, admin)
	if failed.Status != lifecyclemodel.CleanupStatusFailed || !errors.Is(err, updateFailure) {
		t.Fatalf("failed=%#v err=%v", failed, err)
	}
	repository.updateCleanupJob = nil
	auditFailure := errors.New("failed cleanup audit failed")
	repository.appendAudit = func(context.Context, lifecyclemodel.AuditEvidence) error { return auditFailure }
	failed, err = service.ProcessCleanupJob(t.Context(), "workspace-a", job.ID, "worker", time.Minute, 10, now, admin)
	if failed.Status != lifecyclemodel.CleanupStatusFailed || !errors.Is(err, auditFailure) {
		t.Fatalf("failed=%#v err=%v", failed, err)
	}
	repository.appendAudit = nil
	failed, err = service.ProcessCleanupJob(t.Context(), "workspace-a", job.ID, "worker", time.Minute, 10, now, admin)
	if failed.Status != lifecyclemodel.CleanupStatusFailed || err == nil || failed.LeaseOwner != "" || !failed.LeaseExpiresAt.IsZero() || failed.LastError == "" {
		t.Fatalf("failed=%#v err=%v", failed, err)
	}

	repository.latestPolicy = func(context.Context, string, string) (lifecyclemodel.PolicyVersion, bool, error) {
		return version, true, nil
	}
	holdFailure := errors.New("cleanup hold lookup failed")
	repository.activeLegalHolds = func(context.Context, lifecyclemodel.ResourceTarget, time.Time) ([]lifecyclemodel.LegalHold, error) {
		return nil, holdFailure
	}
	failed, err = service.ProcessCleanupJob(t.Context(), "workspace-a", job.ID, "worker", time.Minute, 10, now, admin)
	if failed.Status != lifecyclemodel.CleanupStatusFailed || !errors.Is(err, holdFailure) {
		t.Fatalf("failed=%#v err=%v", failed, err)
	}
	repository.activeLegalHolds = func(context.Context, lifecyclemodel.ResourceTarget, time.Time) ([]lifecyclemodel.LegalHold, error) {
		return nil, nil
	}
	processFailure := errors.New("cleanup batch failed")
	executor.process = func(context.Context, lifecyclemodel.CleanupJob, lifecyclemodel.PolicyVersion, []lifecyclemodel.LegalHold, int) (lifecyclemodel.CleanupBatchResult, error) {
		return lifecyclemodel.CleanupBatchResult{}, processFailure
	}
	failed, err = service.ProcessCleanupJob(t.Context(), "workspace-a", job.ID, "worker", time.Minute, 10, now, admin)
	if failed.Status != lifecyclemodel.CleanupStatusFailed || !errors.Is(err, processFailure) {
		t.Fatalf("failed=%#v err=%v", failed, err)
	}
}

func TestLifecycleProcessCleanupJobProgressPersistenceAndAudit(t *testing.T) {
	now := time.Date(2026, 7, 19, 11, 12, 13, 0, time.UTC)
	service, repository, executor, version := newLifecycleCleanupProbe(t, now)
	admin := lifecycleAdmin("workspace-a", "cleanup-admin")
	job := lifecyclemodel.CleanupJob{ID: "job-1", WorkspaceID: "workspace-a", PolicyKey: version.Policy.Key, Status: lifecyclemodel.CleanupStatusRunning, Scanned: 2, Archived: 1, Purged: 1, Skipped: 1, Failed: 1, LeaseOwner: "worker", LeaseExpiresAt: now.Add(time.Minute)}
	repository.claimCleanupJob = func(context.Context, string, string, string, time.Duration, time.Time) (lifecyclemodel.CleanupJob, bool, error) {
		return job, true, nil
	}
	repository.activeLegalHolds = func(context.Context, lifecyclemodel.ResourceTarget, time.Time) ([]lifecyclemodel.LegalHold, error) {
		return []lifecyclemodel.LegalHold{{ID: "hold-1"}}, nil
	}
	executor.process = func(_ context.Context, received lifecyclemodel.CleanupJob, receivedVersion lifecyclemodel.PolicyVersion, holds []lifecyclemodel.LegalHold, batchSize int) (lifecyclemodel.CleanupBatchResult, error) {
		if received.ID != job.ID || receivedVersion.Policy.Key != version.Policy.Key || len(holds) != 1 || batchSize != 100 {
			t.Fatalf("job=%#v version=%#v holds=%#v batch=%d", received, receivedVersion, holds, batchSize)
		}
		return lifecyclemodel.CleanupBatchResult{Checkpoint: "next", Scanned: 3, Archived: 4, Purged: 5, Skipped: 6, Failed: 7, OldestEligible: now.Add(-time.Hour)}, nil
	}
	updateFailure := errors.New("progress update failed")
	repository.updateCleanupJob = func(context.Context, lifecyclemodel.CleanupJob) error { return updateFailure }
	progressed, err := service.ProcessCleanupJob(t.Context(), "workspace-a", job.ID, "worker", time.Minute, 0, now, admin)
	if progressed.Status != lifecyclemodel.CleanupStatusPending || progressed.Scanned != 5 || progressed.Archived != 5 || progressed.Purged != 6 || progressed.Skipped != 7 || progressed.Failed != 8 || !errors.Is(err, updateFailure) {
		t.Fatalf("progressed=%#v err=%v", progressed, err)
	}
	repository.updateCleanupJob = nil
	auditFailure := errors.New("progress audit failed")
	repository.appendAudit = func(context.Context, lifecyclemodel.AuditEvidence) error { return auditFailure }
	progressed, err = service.ProcessCleanupJob(t.Context(), "workspace-a", job.ID, "worker", time.Minute, 1001, now, admin)
	if progressed.Status != lifecyclemodel.CleanupStatusPending || !errors.Is(err, auditFailure) {
		t.Fatalf("progressed=%#v err=%v", progressed, err)
	}
	repository.appendAudit = nil
	job.DryRun = true
	progressed, err = service.ProcessCleanupJob(t.Context(), "workspace-a", job.ID, "worker", time.Minute, 1001, now, admin)
	if err != nil || progressed.Status != lifecyclemodel.CleanupStatusSucceeded || progressed.LeaseOwner != "" || !progressed.LeaseExpiresAt.IsZero() || progressed.Checkpoint != "next" {
		t.Fatalf("progressed=%#v err=%v", progressed, err)
	}
}

func TestLifecycleProcessCleanupJobAllowsValidSystemPrincipal(t *testing.T) {
	now := time.Now().UTC()
	service, repository, _, version := newLifecycleCleanupProbe(t, now)
	job := lifecyclemodel.CleanupJob{ID: "job-1", WorkspaceID: "workspace-a", PolicyKey: version.Policy.Key}
	repository.claimCleanupJob = func(context.Context, string, string, string, time.Duration, time.Time) (lifecyclemodel.CleanupJob, bool, error) {
		return job, false, nil
	}
	system := principalmodel.NewSystemPrincipal("worker", principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "cleanup"))
	if _, err := service.ProcessCleanupJob(t.Context(), "workspace-a", job.ID, "worker", time.Minute, 10, now, system); err != nil {
		t.Fatal(err)
	}
}
