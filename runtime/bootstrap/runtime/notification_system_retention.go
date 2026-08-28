package runtime

import (
	"context"
	"time"

	notificationsdk "github.com/domainry/domainry-notification-sdk"
	"github.com/domainry/domainry-notification-sdk/contract"
	lifecyclecontract "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/contract"
	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
)

type notificationSystemRetention struct {
	retention notificationsdk.SystemRetention
}

func (notificationSystemRetention) Owner(context.Context) string { return "notification" }

func (r notificationSystemRetention) Preview(ctx context.Context, workspaceID string, policy lifecyclemodel.PolicyVersion, now time.Time) (lifecyclecontract.CleanupPreview, error) {
	result, err := r.retention.Preview(ctx, contract.NotificationRetentionPreviewRequest{WorkspaceID: workspaceID, Policy: notificationRetentionPolicy(policy.Policy), Now: now})
	return lifecyclecontract.CleanupPreview{Rows: result.Rows, Bytes: result.Bytes, OldestEligible: result.OldestEligible}, err
}

func (r notificationSystemRetention) ProcessBatch(ctx context.Context, job lifecyclemodel.CleanupJob, policy lifecyclemodel.PolicyVersion, holds []lifecyclemodel.LegalHold, limit int) (lifecyclemodel.CleanupBatchResult, error) {
	request := contract.NotificationRetentionBatchRequest{
		JobID: job.ID, WorkspaceID: job.WorkspaceID, Operation: string(job.Operation), DryRun: job.DryRun, Checkpoint: job.Checkpoint,
		Now: job.UpdatedAt, Policy: notificationRetentionPolicy(policy.Policy), Holds: make([]contract.NotificationRetentionHold, len(holds)), Limit: limit,
	}
	for index, hold := range holds {
		request.Holds[index] = contract.NotificationRetentionHold{Owner: hold.Owner, ResourceType: hold.ResourceType, ResourceID: hold.ResourceID, StartsAt: hold.StartsAt, EndsAt: hold.EndsAt}
	}
	result, err := r.retention.ProcessBatch(ctx, request)
	return lifecyclemodel.CleanupBatchResult{Checkpoint: result.Checkpoint, Scanned: result.Scanned, Archived: result.Archived, Purged: result.Purged, Skipped: result.Skipped, Failed: result.Failed, OldestEligible: result.OldestEligible, Done: result.Done}, err
}

func notificationRetentionPolicy(policy lifecyclemodel.RetentionPolicy) contract.NotificationRetentionPolicy {
	status := make(map[string]int64, len(policy.StatusRetention))
	for key, duration := range policy.StatusRetention {
		status[key] = int64(duration / time.Second)
	}
	return contract.NotificationRetentionPolicy{Key: policy.Key, Version: policy.Version, DefaultRetentionSeconds: int64(policy.DefaultRetention / time.Second), StatusRetentionSeconds: status}
}

var _ lifecyclecontract.OwnerLifecycleExecutor = notificationSystemRetention{}
