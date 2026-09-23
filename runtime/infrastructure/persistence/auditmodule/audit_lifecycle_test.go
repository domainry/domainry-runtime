package auditmodule

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

type auditLifecycleArchive struct {
	archived map[string]bool
}

func (a *auditLifecycleArchive) Archived(_ context.Context, workspaceID, sourceTable, resourceID, policyKey string) (bool, error) {
	return a.archived[workspaceID+"\x00"+sourceTable+"\x00"+resourceID+"\x00"+policyKey], nil
}

func (a *auditLifecycleArchive) ArchivePayload(_ context.Context, _ string, job lifecyclemodel.CleanupJob, policy lifecyclemodel.PolicyVersion, sourceTable, resourceID string, _ []byte) (bool, error) {
	key := job.WorkspaceID + "\x00" + sourceTable + "\x00" + resourceID + "\x00" + policy.Policy.Key
	if a.archived[key] {
		return false, nil
	}
	a.archived[key] = true
	return true, nil
}

func TestAuditLifecyclePreservesHeldEvidenceAndPurgesOnlyArchivedExpiredRows(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "audit-lifecycle.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB().ExecContext(t.Context(), `CREATE TABLE _audit_events (workspace_id TEXT NOT NULL, id TEXT NOT NULL, created_at TEXT NOT NULL, PRIMARY KEY (workspace_id,id))`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	old := now.Add(-8 * 365 * 24 * time.Hour).Format(time.RFC3339Nano)
	fresh := now.Add(-24 * time.Hour).Format(time.RFC3339Nano)
	for _, row := range []struct{ id, createdAt string }{{"held", old}, {"expired", old}, {"fresh", fresh}} {
		if _, err := store.DB().ExecContext(t.Context(), `INSERT INTO _audit_events (workspace_id,id,created_at) VALUES (?,?,?)`, "workspace-a", row.id, row.createdAt); err != nil {
			t.Fatal(err)
		}
	}
	archives := &auditLifecycleArchive{archived: map[string]bool{}}
	executor := LifecycleExecutor(store, archives)
	policy := lifecyclemodel.PolicyVersion{Policy: lifecyclemodel.RetentionPolicy{Key: "audit.evidence.v1", DefaultRetention: 7 * 365 * 24 * time.Hour}}
	result, err := executor.ProcessBatch(t.Context(), lifecyclemodel.CleanupJob{
		ID: "cleanup-audit", WorkspaceID: "workspace-a", Operation: lifecyclemodel.OperationPurge, UpdatedAt: now,
	}, policy, []lifecyclemodel.LegalHold{{
		ID: "hold-audit", WorkspaceID: "workspace-a", Owner: "audit", ResourceType: "_audit_events", ResourceID: "held", StartsAt: now.Add(-time.Hour), ReviewAt: now.Add(time.Hour),
	}}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.Scanned != 2 || result.Archived != 1 || result.Purged != 1 || result.Skipped != 1 || !result.Done {
		t.Fatalf("cleanup result=%+v", result)
	}
	for id, want := range map[string]int{"held": 1, "expired": 0, "fresh": 1} {
		var count int
		if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _audit_events WHERE workspace_id = ? AND id = ?`, "workspace-a", id).Scan(&count); err != nil || count != want {
			t.Fatalf("event %s count=%d want=%d err=%v", id, count, want, err)
		}
	}
}
