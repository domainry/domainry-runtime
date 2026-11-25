package auditmodule

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/entityid"
	lifecyclemodel "github.com/domainry/domainry-lifecycle-sdk/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	lifecyclepersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/lifecyclemodule"
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
	if _, err := store.DB().ExecContext(t.Context(), `CREATE TABLE _audit_events (id INTEGER PRIMARY KEY AUTOINCREMENT, audit_event_id TEXT NOT NULL UNIQUE, workspace_id TEXT NOT NULL, created_at INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	old := now.Add(-8 * 365 * 24 * time.Hour).UnixMilli()
	fresh := now.Add(-24 * time.Hour).UnixMilli()
	rows := []struct {
		id        string
		createdAt int64
	}{{auditLifecycleTestID(t, "held"), old}, {auditLifecycleTestID(t, "expired"), old}, {auditLifecycleTestID(t, "fresh"), fresh}}
	for _, row := range rows {
		if _, err := store.DB().ExecContext(t.Context(), `INSERT INTO _audit_events (workspace_id,audit_event_id,created_at) VALUES (?,?,?)`, "workspace-a", row.id, row.createdAt); err != nil {
			t.Fatal(err)
		}
	}
	archives := &auditLifecycleArchive{archived: map[string]bool{}}
	executor := LifecycleExecutor(store, archives)
	policy := lifecyclemodel.PolicyVersion{Policy: lifecyclemodel.RetentionPolicy{Key: "audit.evidence.v1", DefaultRetention: 7 * 365 * 24 * time.Hour}}
	result, err := executor.ProcessBatch(t.Context(), lifecyclemodel.CleanupJob{
		JobID: auditLifecycleEntityID(t, "cleanup", "cleanup-audit"), WorkspaceID: "workspace-a", Operation: lifecyclemodel.OperationPurge, UpdatedAt: now,
	}, policy, []lifecyclemodel.LegalHold{{
		HoldID: auditLifecycleEntityID(t, "hold", "hold-audit"), WorkspaceID: "workspace-a", Owner: "audit", ResourceType: "_audit_events", ResourceID: rows[0].id, StartsAt: now.Add(-time.Hour), ReviewAt: now.Add(time.Hour),
	}}, 10)
	if err != nil {
		t.Fatal(err)
	}
	if result.Scanned != 2 || result.Archived != 1 || result.Purged != 1 || result.Skipped != 1 || !result.Done {
		t.Fatalf("cleanup result=%+v", result)
	}
	for id, want := range map[string]int{rows[0].id: 1, rows[1].id: 0, rows[2].id: 1} {
		var count int
		if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _audit_events WHERE workspace_id = ? AND audit_event_id = ?`, "workspace-a", id).Scan(&count); err != nil || count != want {
			t.Fatalf("event %s count=%d want=%d err=%v", id, count, want, err)
		}
	}
}

func TestAuditLifecycleAdvancesPastHeldAndAlreadyArchivedPages(t *testing.T) {
	for _, operation := range []lifecyclemodel.Operation{lifecyclemodel.OperationArchive, lifecyclemodel.OperationPurge} {
		t.Run(string(operation), func(t *testing.T) {
			ctx := t.Context()
			store, err := database.OpenContext(ctx, config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "audit-lifecycle.db")})
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			// This synthetic table gives all fixture rows an identical timestamp.
			if _, err := store.DB().ExecContext(ctx, `CREATE TABLE _audit_events (audit_event_id TEXT NOT NULL UNIQUE, workspace_id TEXT NOT NULL, created_at INTEGER NOT NULL)`); err != nil {
				t.Fatal(err)
			}
			const workspace, policyKey = "workspace-a", "audit.evidence.v1"
			now := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)
			ids := []string{auditLifecycleTestID(t, "one"), auditLifecycleTestID(t, "two"), auditLifecycleTestID(t, "three"), auditLifecycleTestID(t, "four"), auditLifecycleTestID(t, "five")}
			slices.Sort(ids)
			for _, id := range ids {
				if _, err := store.DB().ExecContext(ctx, `INSERT INTO _audit_events (workspace_id,audit_event_id,created_at) VALUES (?,?,?)`, workspace, id, now.Add(-8*365*24*time.Hour).UnixMilli()); err != nil {
					t.Fatal(err)
				}
			}
			var holds []lifecyclemodel.LegalHold
			for _, id := range ids[:2] {
				holds = append(holds, lifecyclemodel.LegalHold{WorkspaceID: workspace, Owner: "audit", ResourceType: "_audit_events", ResourceID: id, StartsAt: now.Add(-time.Hour)})
			}
			archives := &auditLifecycleArchive{archived: map[string]bool{}}
			executor := LifecycleExecutor(store, archives)
			policy := lifecyclemodel.PolicyVersion{Policy: lifecyclemodel.RetentionPolicy{Key: policyKey, DefaultRetention: 7 * 365 * 24 * time.Hour}}
			job := lifecyclemodel.CleanupJob{JobID: auditLifecycleEntityID(t, "cleanup", "pages"), WorkspaceID: workspace, Operation: operation, CreatedAt: now, UpdatedAt: now}
			if _, err := archives.ArchivePayload(ctx, "audit", job, policy, "_audit_events", ids[2], nil); err != nil {
				t.Fatal(err)
			}
			var scanned, skipped, archived, purged int64
			for page := 1; page <= 3; page++ {
				result, err := executor.ProcessBatch(ctx, job, policy, holds, 2)
				if err != nil {
					t.Fatal(err)
				}
				if result.Done != (page == 3) || result.Checkpoint == job.Checkpoint || result.Scanned > 2 {
					t.Fatalf("page %d checkpoint=%q result=%+v", page, job.Checkpoint, result)
				}
				scanned += result.Scanned
				skipped += result.Skipped
				archived += result.Archived
				purged += result.Purged
				job.Checkpoint = result.Checkpoint
			}
			wantPurged := int64(0)
			if operation == lifecyclemodel.OperationPurge {
				wantPurged = 3
			}
			if scanned != 5 || skipped != 2 || archived != 2 || purged != wantPurged {
				t.Fatalf("scanned=%d skipped=%d archived=%d purged=%d", scanned, skipped, archived, purged)
			}
			for i, id := range ids {
				var count int
				if err := store.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM _audit_events WHERE workspace_id = ? AND audit_event_id = ?`, workspace, id).Scan(&count); err != nil {
					t.Fatal(err)
				}
				exists := count == 1
				if exists != (i < 2 || wantPurged == 0) {
					t.Fatalf("row %d exists=%v", i, exists)
				}
			}
		})
	}
}

func TestRelationalCleanupResumesAcrossSameTableSpecsWithOriginalCutoff(t *testing.T) {
	ctx := t.Context()
	store, err := database.OpenContext(ctx, config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "cleanup.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.DB().ExecContext(ctx, `CREATE TABLE cleanup_rows (id INTEGER PRIMARY KEY, workspace_id TEXT NOT NULL, state TEXT NOT NULL, created_at INTEGER NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	const workspace, policyKey = "workspace-a", "fixture.retention"
	now := time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC)
	for _, row := range []struct {
		id    int64
		state string
		age   time.Duration
	}{{2, "first", 48 * time.Hour}, {10, "first", 48 * time.Hour}, {20, "first", 23 * time.Hour}, {1, "second", 48 * time.Hour}, {3, "second", 48 * time.Hour}} {
		if _, err := store.DB().ExecContext(ctx, `INSERT INTO cleanup_rows (id,workspace_id,state,created_at) VALUES (?,?,?,?)`, row.id, workspace, row.state, now.Add(-row.age).UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	first := lifecyclepersistence.RelationalCleanupSpec{PolicyKey: policyKey, Table: "cleanup_rows", IDColumn: "id", TenantColumn: "workspace_id", TimeColumn: "created_at", StatusColumn: "state", EligibleStatuses: []string{"first"}}
	second := first
	second.EligibleStatuses = []string{"second"}
	archives := &auditLifecycleArchive{archived: map[string]bool{}}
	executor := lifecyclepersistence.NewRelationalOwnerExecutor(store, archives, "fixture", first, second)
	policy := lifecyclemodel.PolicyVersion{Policy: lifecyclemodel.RetentionPolicy{Key: policyKey, DefaultRetention: 24 * time.Hour}}
	job := lifecyclemodel.CleanupJob{WorkspaceID: workspace, Operation: lifecyclemodel.OperationPurge, CreatedAt: now, UpdatedAt: now}
	for page := 1; page <= 4; page++ {
		batch, err := executor.ProcessBatch(ctx, job, policy, nil, 1)
		if err != nil || batch.Scanned != 1 || batch.Purged != 1 || batch.Done != (page == 4) {
			t.Fatalf("batch %d checkpoint=%q result=%+v err=%v", page, job.Checkpoint, batch, err)
		}
		job.Checkpoint = batch.Checkpoint
		job.UpdatedAt = job.UpdatedAt.Add(2 * time.Hour)
	}
	var remaining int64
	if err := store.DB().QueryRowContext(ctx, `SELECT id FROM cleanup_rows WHERE workspace_id = ?`, workspace).Scan(&remaining); err != nil || remaining != 20 {
		t.Fatalf("remaining row=%d err=%v", remaining, err)
	}
}

func auditLifecycleTestID(t *testing.T, label string) string {
	return auditLifecycleEntityID(t, "audevt", label)
}

func auditLifecycleEntityID(t *testing.T, prefix, label string) string {
	t.Helper()
	id, err := entityid.Derive(prefix, "runtime-audit-lifecycle-test", label)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
