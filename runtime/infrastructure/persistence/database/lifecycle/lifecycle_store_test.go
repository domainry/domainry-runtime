package lifecycle

import (
	"path/filepath"
	"testing"
	"time"

	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestLifecycleStorePersistsWorkspaceScopedPolicyHoldAndFencedJob(t *testing.T) {
	store := openLifecycleStore(t)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	repository := NewLifecycleStore(store)
	version := lifecyclemodel.PolicyVersion{WorkspaceID: "workspace-a", Policy: lifecyclemodel.RetentionPolicy{Key: "integration.webhook_nonce.v1", Version: "1", Owner: "integration", Class: lifecyclemodel.RetentionClassTechnical, DefaultRetention: time.Hour, MinimumRetention: 15 * time.Minute, BackupBehavior: lifecyclemodel.BackupBehaviorStandard, EraseBehavior: lifecyclemodel.EraseBehaviorDelete}, Status: lifecyclemodel.PolicyStatusPublished, Revision: 1, PublishedBy: "admin", PublishedAt: now}
	if err := repository.SavePolicy(t.Context(), version); err != nil {
		t.Fatal(err)
	}
	loaded, found, err := repository.LatestPolicy(t.Context(), "workspace-a", version.Policy.Key)
	if err != nil || !found || loaded.Policy.Version != "1" {
		t.Fatalf("loaded=%#v found=%v err=%v", loaded, found, err)
	}
	if _, found, err := repository.LatestPolicy(t.Context(), "workspace-b", version.Policy.Key); err != nil || found {
		t.Fatalf("policy crossed workspace: found=%v err=%v", found, err)
	}
	hold := lifecyclemodel.LegalHold{ID: "hold-1", WorkspaceID: "workspace-a", Owner: "integration", ResourceType: "integration_webhook_nonces", ResourceID: "nonce-1", Reason: "case", Authority: "legal", StartsAt: now.Add(-time.Hour), ReviewAt: now.Add(time.Hour), AuditEvidence: "audit-1"}
	if err := repository.SaveLegalHold(t.Context(), hold); err != nil {
		t.Fatal(err)
	}
	holds, err := repository.ActiveLegalHolds(t.Context(), lifecyclemodel.ResourceTarget{WorkspaceID: "workspace-a", Owner: "integration", ResourceType: "integration_webhook_nonces", ResourceID: "nonce-1"}, now)
	if err != nil || len(holds) != 1 {
		t.Fatalf("holds=%#v err=%v", holds, err)
	}
	job := lifecyclemodel.CleanupJob{ID: "job-1", WorkspaceID: "workspace-a", PolicyKey: version.Policy.Key, PolicyVersion: "1", Operation: lifecyclemodel.OperationPurge, Status: lifecyclemodel.CleanupStatusPending, RequestedBy: "admin", Reason: "retention", CreatedAt: now, UpdatedAt: now}
	if err := repository.SaveCleanupJob(t.Context(), job); err != nil {
		t.Fatal(err)
	}
	claimed, acquired, err := repository.ClaimCleanupJob(t.Context(), "workspace-a", "job-1", "worker-a", time.Minute, now)
	if err != nil || !acquired || claimed.FencingToken != 1 {
		t.Fatalf("claimed=%#v acquired=%v err=%v", claimed, acquired, err)
	}
	if _, acquired, err := repository.ClaimCleanupJob(t.Context(), "workspace-a", "job-1", "worker-b", time.Minute, now); err != nil || acquired {
		t.Fatalf("double claim acquired=%v err=%v", acquired, err)
	}
	scope := principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "test lifecycle cleanup discovery")
	runnable, err := repository.ListRunnableCleanupJobs(t.Context(), scope, 10, now.Add(2*time.Minute))
	if err != nil || len(runnable) != 1 || runnable[0].ID != "job-1" {
		t.Fatalf("expired running job not recoverable: jobs=%#v err=%v", runnable, err)
	}
	reclaimed, acquired, err := repository.ClaimCleanupJob(t.Context(), "workspace-a", "job-1", "worker-b", time.Minute, now.Add(2*time.Minute))
	if err != nil || !acquired || reclaimed.FencingToken != 2 {
		t.Fatalf("reclaimed=%#v acquired=%v err=%v", reclaimed, acquired, err)
	}
	claimed.Status = lifecyclemodel.CleanupStatusSucceeded
	if err := repository.UpdateCleanupJob(t.Context(), claimed); err == nil {
		t.Fatal("stale fencing token updated reclaimed job")
	}
}

func TestLifecycleStoreWorkspaceIsolationContract(t *testing.T) {
	store := openLifecycleStore(t)
	repository := NewLifecycleStore(store)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)

	for _, workspaceID := range []string{"workspace-a", "workspace-b"} {
		request := lifecyclemodel.SubjectRequest{
			ID:          "request-shared",
			WorkspaceID: workspaceID,
			Kind:        lifecyclemodel.SubjectRequestErase,
			Status:      lifecyclemodel.SubjectRequestPendingVerification,
			SubjectType: "user",
			SubjectID:   "user-shared",
			RequestedBy: "requester",
			Reason:      workspaceID,
			CreatedAt:   now,
			UpdatedAt:   now,
		}
		if err := repository.SaveSubjectRequest(t.Context(), request); err != nil {
			t.Fatalf("save %s: %v", workspaceID, err)
		}
	}

	requestA, found, err := repository.GetSubjectRequest(t.Context(), "workspace-a", "request-shared")
	if err != nil || !found || requestA.Reason != "workspace-a" {
		t.Fatalf("workspace A read: request=%#v found=%v err=%v", requestA, found, err)
	}
	requestA.Status = lifecyclemodel.SubjectRequestSucceeded
	requestA.Reason = "workspace-a-updated"
	requestA.UpdatedAt = now.Add(time.Minute)
	if err := repository.SaveSubjectRequest(t.Context(), requestA); err != nil {
		t.Fatalf("update workspace A: %v", err)
	}

	requestB, found, err := repository.GetSubjectRequest(t.Context(), "workspace-b", "request-shared")
	if err != nil || !found {
		t.Fatalf("workspace B read: request=%#v found=%v err=%v", requestB, found, err)
	}
	if requestB.Status != lifecyclemodel.SubjectRequestPendingVerification || requestB.Reason != "workspace-b" {
		t.Fatalf("workspace A update crossed into workspace B: %#v", requestB)
	}
	if _, found, err := repository.GetSubjectRequest(t.Context(), "workspace-c", "request-shared"); err != nil || found {
		t.Fatalf("unknown workspace observed tenant request: found=%v err=%v", found, err)
	}
}

func TestLifecycleStoreUsesInstallationDefaultUntilWorkspaceOverride(t *testing.T) {
	store := openLifecycleStore(t)
	repository := NewLifecycleStore(store)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	base := lifecyclemodel.PolicyVersion{WorkspaceID: principalmodel.InstallationWorkspaceID, Policy: lifecyclemodel.RetentionPolicy{Key: "audit.evidence.v1", Version: "1", Owner: "audit"}, Status: lifecyclemodel.PolicyStatusPublished, Revision: 1, PublishedAt: now}
	if err := repository.SavePolicy(t.Context(), base); err != nil {
		t.Fatal(err)
	}
	loaded, found, err := repository.LatestPolicy(t.Context(), "workspace-a", base.Policy.Key)
	if err != nil || !found || loaded.WorkspaceID != principalmodel.InstallationWorkspaceID {
		t.Fatalf("default=%#v found=%v err=%v", loaded, found, err)
	}
	override := base
	override.WorkspaceID, override.Revision = "workspace-a", 2
	if err := repository.SavePolicy(t.Context(), override); err != nil {
		t.Fatal(err)
	}
	loaded, found, err = repository.LatestPolicy(t.Context(), "workspace-a", base.Policy.Key)
	if err != nil || !found || loaded.WorkspaceID != "workspace-a" {
		t.Fatalf("override=%#v found=%v err=%v", loaded, found, err)
	}
}

func TestLifecycleStorePersistsBackupDeletionRegistryByWorkspace(t *testing.T) {
	store := openLifecycleStore(t)
	repository := NewLifecycleStore(store)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	registration := lifecyclemodel.DeletionRegistration{RequestID: "erase-1", WorkspaceID: "workspace-a", ResolvedIdentity: "user-1", BackupPending: true, Evidence: "erase-evidence:erase-1", UpdatedAt: now}
	if err := repository.SaveDeletionRegistration(t.Context(), registration); err != nil {
		t.Fatal(err)
	}
	items, err := repository.ListPendingDeletionRegistrations(t.Context(), "workspace-a", 10)
	if err != nil || len(items) != 1 || items[0].RequestID != registration.RequestID {
		t.Fatalf("items=%#v err=%v", items, err)
	}
	other, err := repository.ListPendingDeletionRegistrations(t.Context(), "workspace-b", 10)
	if err != nil || len(other) != 0 {
		t.Fatalf("workspace boundary failed: items=%#v err=%v", other, err)
	}
}

func TestLifecycleStoreReconcilesExternalErasureWithEvidence(t *testing.T) {
	store := openLifecycleStore(t)
	repository := NewLifecycleStore(store)
	item := lifecyclemodel.ExternalErasure{ID: "provider-erase-1", RequestID: "erase-1", WorkspaceID: "workspace-a", ConnectorKey: "slack", ProviderRef: "U123", Status: "requested"}
	if err := repository.SaveExternalErasures(t.Context(), []lifecyclemodel.ExternalErasure{item}); err != nil {
		t.Fatal(err)
	}
	updated, found, err := repository.ReconcileExternalErasure(t.Context(), "workspace-a", item.ID, "provider-ticket-42", time.Now().UTC())
	if err != nil || !found || updated.Status != "reconciled" || updated.Evidence == "" {
		t.Fatalf("updated=%#v found=%v err=%v", updated, found, err)
	}
	if err := repository.SaveExternalErasures(t.Context(), []lifecyclemodel.ExternalErasure{item}); err != nil {
		t.Fatal(err)
	}
	items, err := repository.ListExternalErasures(t.Context(), "workspace-a", item.RequestID)
	if err != nil || len(items) != 1 || items[0].Status != "reconciled" {
		t.Fatalf("items=%#v err=%v", items, err)
	}
	if _, found, err := repository.ReconcileExternalErasure(t.Context(), "workspace-b", item.ID, "wrong-workspace", time.Now().UTC()); err != nil || found {
		t.Fatalf("cross-workspace reconciliation found=%v err=%v", found, err)
	}
}

func TestSubjectRequestTransitionRejectsStaleWriter(t *testing.T) {
	store := openLifecycleStore(t)
	repository := NewLifecycleStore(store)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	current := lifecyclemodel.SubjectRequest{ID: "request-cas", WorkspaceID: "workspace-a", Kind: lifecyclemodel.SubjectRequestExport, Status: lifecyclemodel.SubjectRequestPendingVerification, SubjectID: "user-1", UpdatedAt: now}
	if err := repository.SaveSubjectRequest(t.Context(), current); err != nil {
		t.Fatal(err)
	}
	next := current
	next.Status, next.UpdatedAt = lifecyclemodel.SubjectRequestVerified, now.Add(time.Minute)
	if err := repository.TransitionSubjectRequest(t.Context(), current, next); err != nil {
		t.Fatal(err)
	}
	stale := current
	stale.Status, stale.UpdatedAt = lifecyclemodel.SubjectRequestFailed, now.Add(2*time.Minute)
	if err := repository.TransitionSubjectRequest(t.Context(), current, stale); err == nil {
		t.Fatal("stale subject request writer replaced a newer transition")
	}
}

func openLifecycleStore(t *testing.T) *database.RuntimeStore {
	t.Helper()
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "lifecycle.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	return store
}
