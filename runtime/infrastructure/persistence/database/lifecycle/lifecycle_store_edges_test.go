package lifecycle

import (
	"encoding/json"
	"testing"
	"time"

	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestLifecycleStoreListsPolicyVersionsAndUpdatesLegalHolds(t *testing.T) {
	store := openLifecycleStore(t)
	repository := NewLifecycleStore(store)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	for _, version := range []lifecyclemodel.PolicyVersion{
		{WorkspaceID: "workspace-a", Policy: lifecyclemodel.RetentionPolicy{Key: "audit.evidence.v1", Version: "1", Owner: "audit"}, Status: lifecyclemodel.PolicyStatusPublished, Revision: 1, PublishedAt: now},
		{WorkspaceID: "workspace-a", Policy: lifecyclemodel.RetentionPolicy{Key: "audit.evidence.v1", Version: "2", Owner: "audit"}, Status: lifecyclemodel.PolicyStatusDraft, Revision: 2},
		{WorkspaceID: "workspace-a", Policy: lifecyclemodel.RetentionPolicy{Key: "record.object.v1", Version: "1", Owner: "record"}, Status: lifecyclemodel.PolicyStatusPublished, Revision: 1, PublishedAt: now},
		{WorkspaceID: "workspace-b", Policy: lifecyclemodel.RetentionPolicy{Key: "audit.evidence.v1", Version: "1", Owner: "audit"}, Status: lifecyclemodel.PolicyStatusPublished, Revision: 1, PublishedAt: now},
	} {
		if err := repository.SavePolicy(t.Context(), version); err != nil {
			t.Fatal(err)
		}
	}
	versions, err := repository.ListPolicies(t.Context(), "workspace-a")
	if err != nil || len(versions) != 3 {
		t.Fatalf("versions=%+v err=%v", versions, err)
	}
	if versions[0].Policy.Key != "audit.evidence.v1" || versions[0].Revision != 2 || versions[1].Revision != 1 || versions[2].Policy.Key != "record.object.v1" {
		t.Fatalf("policy ordering=%+v", versions)
	}

	hold := lifecyclemodel.LegalHold{ID: "hold-update", WorkspaceID: "workspace-a", Owner: "audit", ResourceType: "audit_event", ResourceID: "event-1", StartsAt: now.Add(-time.Hour), ReviewAt: now.Add(time.Hour)}
	if err := repository.SaveLegalHold(t.Context(), hold); err != nil {
		t.Fatal(err)
	}
	ends := now.Add(2 * time.Hour)
	hold.EndsAt, hold.Reason = &ends, "extended"
	if err := repository.SaveLegalHold(t.Context(), hold); err != nil {
		t.Fatal(err)
	}
	loaded, found, err := repository.GetLegalHold(t.Context(), "workspace-a", hold.ID)
	if err != nil || !found || loaded.Reason != "extended" || loaded.EndsAt == nil || !loaded.EndsAt.Equal(ends) {
		t.Fatalf("hold=%+v found=%v err=%v", loaded, found, err)
	}
	if _, found, err := repository.GetLegalHold(t.Context(), "workspace-a", "missing"); err != nil || found {
		t.Fatalf("missing hold found=%v err=%v", found, err)
	}
}

func TestLifecycleMetricsAggregateBacklogHoldsAndAuditEvidence(t *testing.T) {
	store := openLifecycleStore(t)
	repository := NewLifecycleStore(store)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	for _, job := range []lifecyclemodel.CleanupJob{
		{ID: "pending", WorkspaceID: "workspace-a", PolicyKey: "policy", Status: lifecyclemodel.CleanupStatusPending, UpdatedAt: now.Add(-25 * time.Hour)},
		{ID: "failed", WorkspaceID: "workspace-a", PolicyKey: "policy", Status: lifecyclemodel.CleanupStatusFailed, UpdatedAt: now.Add(-time.Hour)},
		{ID: "succeeded", WorkspaceID: "workspace-a", PolicyKey: "policy", Status: lifecyclemodel.CleanupStatusSucceeded, UpdatedAt: now},
		{ID: "other", WorkspaceID: "workspace-b", PolicyKey: "policy", Status: lifecyclemodel.CleanupStatusPending, UpdatedAt: now.Add(-48 * time.Hour)},
	} {
		if err := repository.SaveCleanupJob(t.Context(), job); err != nil {
			t.Fatal(err)
		}
	}
	hold := lifecyclemodel.LegalHold{ID: "hold-metrics", WorkspaceID: "workspace-a", StartsAt: now.Add(-time.Hour), ReviewAt: now.Add(time.Hour)}
	if err := repository.SaveLegalHold(t.Context(), hold); err != nil {
		t.Fatal(err)
	}
	succeededJob, err := json.Marshal(lifecyclemodel.CleanupJob{ID: "completed", Purged: 7})
	if err != nil {
		t.Fatal(err)
	}
	for _, evidence := range []lifecyclemodel.AuditEvidence{
		{ID: "audit-success", WorkspaceID: "workspace-a", Event: "lifecycle.cleanup.succeeded", Payload: succeededJob, CreatedAt: now},
		{ID: "audit-failure", WorkspaceID: "workspace-a", Event: "lifecycle.cleanup.failed", Payload: json.RawMessage(`{}`), CreatedAt: now},
		{ID: "audit-ignored", WorkspaceID: "workspace-a", Event: "lifecycle.cleanup.started", Payload: json.RawMessage(`{}`), CreatedAt: now},
	} {
		if err := repository.AppendAuditEvidence(t.Context(), evidence); err != nil {
			t.Fatal(err)
		}
	}
	metrics, err := repository.Metrics(t.Context(), "workspace-a", now)
	if err != nil {
		t.Fatal(err)
	}
	if metrics.EligibleBacklog != 2 || metrics.LegalHoldCount != 1 || metrics.PurgedTotal != 7 || metrics.FailureTotal != 1 || !metrics.Warning || !metrics.OldestEligible.Equal(now.Add(-25*time.Hour)) {
		t.Fatalf("metrics=%+v", metrics)
	}
	empty, err := repository.Metrics(t.Context(), "workspace-empty", now)
	if err != nil || empty.EligibleBacklog != 0 || empty.LegalHoldCount != 0 || empty.Warning || !empty.OldestEligible.IsZero() {
		t.Fatalf("empty metrics=%+v err=%v", empty, err)
	}
}

func TestExpireSubjectExportReferencesClearsOnlyExpiredDownloads(t *testing.T) {
	store := openLifecycleStore(t)
	repository := NewLifecycleStore(store)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	requests := []lifecyclemodel.SubjectRequest{
		{ID: "expired", WorkspaceID: "workspace-a", Kind: lifecyclemodel.SubjectRequestExport, Status: lifecyclemodel.SubjectRequestSucceeded, ResultReference: "download://expired", DownloadExpiresAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Hour)},
		{ID: "expired-empty", WorkspaceID: "workspace-a", Kind: lifecyclemodel.SubjectRequestExport, Status: lifecyclemodel.SubjectRequestSucceeded, DownloadExpiresAt: now.Add(-time.Minute), UpdatedAt: now.Add(-time.Hour)},
		{ID: "future", WorkspaceID: "workspace-a", Kind: lifecyclemodel.SubjectRequestExport, Status: lifecyclemodel.SubjectRequestSucceeded, ResultReference: "download://future", DownloadExpiresAt: now.Add(time.Hour), UpdatedAt: now},
		{ID: "erase", WorkspaceID: "workspace-a", Kind: lifecyclemodel.SubjectRequestErase, Status: lifecyclemodel.SubjectRequestSucceeded, ResultReference: "erase://result", DownloadExpiresAt: now.Add(-time.Minute), UpdatedAt: now},
	}
	for _, request := range requests {
		if err := repository.SaveSubjectRequest(t.Context(), request); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := repository.ExpireSubjectExportReferences(t.Context(), principalmodel.SystemScope{}, now); err == nil {
		t.Fatal("unscoped expiration was accepted")
	}
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "expire subject exports")
	expired, err := repository.ExpireSubjectExportReferences(t.Context(), scope, now)
	if err != nil || len(expired) != 1 || expired[0].ID != "expired" || expired[0].ResultReference != "" || !expired[0].UpdatedAt.Equal(now) {
		t.Fatalf("expired=%+v err=%v", expired, err)
	}
	for id, wantReference := range map[string]string{"expired": "", "future": "download://future", "erase": "erase://result"} {
		loaded, found, err := repository.GetSubjectRequest(t.Context(), "workspace-a", id)
		if err != nil || !found || loaded.ResultReference != wantReference {
			t.Fatalf("request=%s loaded=%+v found=%v err=%v", id, loaded, found, err)
		}
	}
}

func TestCleanupJobDiscoveryAndClaimInputEdges(t *testing.T) {
	store := openLifecycleStore(t)
	repository := NewLifecycleStore(store)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	if _, err := repository.ListRunnableCleanupJobs(t.Context(), principalmodel.SystemScope{}, 0, now); err == nil {
		t.Fatal("unscoped cleanup discovery was accepted")
	}
	if _, acquired, err := repository.ClaimCleanupJob(t.Context(), "workspace-a", "missing", " ", 0, now); err == nil || acquired {
		t.Fatalf("blank owner acquired=%v err=%v", acquired, err)
	}
	if _, acquired, err := repository.ClaimCleanupJob(t.Context(), "workspace-a", "missing", "worker", 0, now); err != nil || acquired {
		t.Fatalf("missing job acquired=%v err=%v", acquired, err)
	}
}
