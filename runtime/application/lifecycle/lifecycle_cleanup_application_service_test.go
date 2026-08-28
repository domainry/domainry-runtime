package lifecycle

import (
	"testing"
	"time"

	lifecyclemodel "github.com/domainry/domainry-runtime/runtime/domain/lifecycle/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestLifecycleCleanupIsWorkspaceScopedFencedAndLegalHoldSafe(t *testing.T) {
	service, store := newLifecycleApplicationTestService(t)
	now := time.Now().UTC()
	admin := lifecycleAdmin("workspace-a", "admin-a")
	publishTestPolicy(t, service, admin, now)
	insert := "INSERT INTO integration_webhook_nonces (id, workspace_id, connector_key, nonce, request_timestamp, created_at, expires_at) VALUES (?, ?, ?, ?, ?, ?, ?)"
	for _, row := range [][2]string{{"nonce-held", "workspace-a"}, {"nonce-purge", "workspace-a"}, {"nonce-other", "workspace-b"}} {
		if _, err := store.DB().ExecContext(t.Context(), insert, row[0], row[1], "webhook", row[0], now.Add(-48*time.Hour).Format(time.RFC3339Nano), now.Add(-48*time.Hour).Format(time.RFC3339Nano), now.Add(-24*time.Hour).Format(time.RFC3339Nano)); err != nil {
			t.Fatal(err)
		}
	}
	_, err := service.CreateLegalHold(t.Context(), lifecyclemodel.LegalHold{ID: "hold-1", WorkspaceID: "workspace-a", Owner: "integration", ResourceType: "integration_webhook_nonces", ResourceID: "nonce-held", Reason: "litigation", Authority: "legal", StartsAt: now.Add(-time.Hour), ReviewAt: now.Add(24 * time.Hour), AuditEvidence: "audit-hold"}, admin)
	if err != nil {
		t.Fatal(err)
	}
	job, err := service.CreateCleanupJob(t.Context(), lifecyclemodel.CleanupJob{WorkspaceID: "workspace-a", PolicyKey: "integration.webhook_nonce.v1", Operation: lifecyclemodel.OperationPurge, Reason: "retention cleanup"}, admin)
	if err != nil {
		t.Fatal(err)
	}
	system := principalmodel.NewSystemPrincipal("lifecycle-worker", principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "lifecycle cleanup"))
	completed, err := service.ProcessCleanupJob(t.Context(), "workspace-a", job.ID, "worker-a", time.Minute, 100, now, system)
	if err != nil || completed.Purged != 1 || completed.Skipped != 1 || completed.Status != lifecyclemodel.CleanupStatusSucceeded {
		t.Fatalf("completed=%#v err=%v", completed, err)
	}
	for id, want := range map[string]int{"nonce-held": 1, "nonce-purge": 0, "nonce-other": 1} {
		var count int
		if err := store.DB().QueryRowContext(t.Context(), "SELECT COUNT(*) FROM integration_webhook_nonces WHERE id = ?", id).Scan(&count); err != nil || count != want {
			t.Fatalf("id=%s count=%d want=%d err=%v", id, count, want, err)
		}
	}
}
