package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/domainry/domainry-notification-sdk/contract"
	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
)

type notificationRetentionStub struct {
	preview contract.NotificationRetentionPreviewRequest
	batch   contract.NotificationRetentionBatchRequest
}

func (s *notificationRetentionStub) Preview(_ context.Context, request contract.NotificationRetentionPreviewRequest) (contract.NotificationRetentionPreview, error) {
	s.preview = request
	return contract.NotificationRetentionPreview{Rows: 2}, nil
}
func (s *notificationRetentionStub) ProcessBatch(_ context.Context, request contract.NotificationRetentionBatchRequest) (contract.NotificationRetentionBatchResult, error) {
	s.batch = request
	return contract.NotificationRetentionBatchResult{Scanned: 1, Purged: 1, Done: true}, nil
}

func TestNotificationSystemRetentionProjectsRuntimeLifecycleWithoutSQL(t *testing.T) {
	stub := &notificationRetentionStub{}
	adapter := notificationSystemRetention{retention: stub}
	now := time.Date(2026, 8, 29, 1, 0, 0, 0, time.UTC)
	policy := lifecyclemodel.PolicyVersion{Policy: lifecyclemodel.RetentionPolicy{Key: contract.NotificationRetentionHistoryPolicy, Version: "1", DefaultRetention: time.Hour}}
	preview, err := adapter.Preview(t.Context(), "workspace-a", policy, now)
	if err != nil || preview.Rows != 2 || stub.preview.WorkspaceID != "workspace-a" || stub.preview.Policy.DefaultRetentionSeconds != 3600 {
		t.Fatalf("preview=%+v request=%+v err=%v", preview, stub.preview, err)
	}
	ends := now.Add(time.Hour)
	result, err := adapter.ProcessBatch(t.Context(), lifecyclemodel.CleanupJob{ID: "job", WorkspaceID: "workspace-a", Operation: lifecyclemodel.OperationPurge, UpdatedAt: now}, policy, []lifecyclemodel.LegalHold{{Owner: "notification", ResourceType: "notification_events", ResourceID: "event", StartsAt: now.Add(-time.Hour), EndsAt: &ends}}, 25)
	if err != nil || result.Purged != 1 || stub.batch.Limit != 25 || len(stub.batch.Holds) != 1 || stub.batch.Holds[0].ResourceID != "event" {
		t.Fatalf("result=%+v request=%+v err=%v", result, stub.batch, err)
	}
}
