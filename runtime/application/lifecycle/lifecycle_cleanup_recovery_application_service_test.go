package lifecycle

import (
	"context"
	"fmt"
	"testing"
	"time"

	lifecyclecontract "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/contract"
	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
)

type recoverableCleanupExecutor struct{ fail bool }

func (e *recoverableCleanupExecutor) Owner(context.Context) string { return "integration" }
func (e *recoverableCleanupExecutor) Preview(context.Context, string, lifecyclemodel.PolicyVersion, time.Time) (lifecyclecontract.CleanupPreview, error) {
	return lifecyclecontract.CleanupPreview{Rows: 1}, nil
}
func (e *recoverableCleanupExecutor) ProcessBatch(context.Context, lifecyclemodel.CleanupJob, lifecyclemodel.PolicyVersion, []lifecyclemodel.LegalHold, int) (lifecyclemodel.CleanupBatchResult, error) {
	if e.fail {
		return lifecyclemodel.CleanupBatchResult{Scanned: 1, Failed: 1}, fmt.Errorf("injected database timeout")
	}
	return lifecyclemodel.CleanupBatchResult{Scanned: 1, Archived: 1, Purged: 1, Checkpoint: "done", Done: true}, nil
}

func TestCleanupFailureReleasesLeaseAndCanBeFencedRetry(t *testing.T) {
	service, _ := newLifecycleApplicationTestService(t)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	admin := lifecycleAdmin("workspace-a", "admin")
	policy := publishTestPolicy(t, service, admin, now)
	job, err := service.CreateCleanupJob(t.Context(), lifecyclemodel.CleanupJob{WorkspaceID: "workspace-a", PolicyKey: policy.Policy.Key, PolicyVersion: policy.Policy.Version, Operation: lifecyclemodel.OperationPurge, Reason: "retention"}, admin)
	if err != nil {
		t.Fatal(err)
	}
	executor := &recoverableCleanupExecutor{fail: true}
	service.executors["integration"] = executor
	failed, err := service.ProcessCleanupJob(t.Context(), "workspace-a", job.ID, "worker-1", time.Minute, 10, now, admin)
	if err == nil || failed.Status != lifecyclemodel.CleanupStatusFailed || failed.LeaseOwner != "" || failed.FencingToken != 1 {
		t.Fatalf("failed=%#v err=%v", failed, err)
	}
	executor.fail = false
	retried, err := service.ProcessCleanupJob(t.Context(), "workspace-a", job.ID, "worker-2", time.Minute, 10, now.Add(time.Minute), admin)
	if err != nil || retried.Status != lifecyclemodel.CleanupStatusSucceeded || retried.FencingToken != 2 || retried.Purged != 1 {
		t.Fatalf("retried=%#v err=%v", retried, err)
	}
}
