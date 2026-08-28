package operations

import (
	"path/filepath"
	"testing"
	"time"

	operationsmodel "github.com/domainry/domainry-runtime/runtime/domain/operations/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestOperationsBreakGlassStoreEnforcesSingleActiveAndRevisionCAS(t *testing.T) {
	runtimeStore, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "break-glass.db")})
	if err != nil {
		t.Fatal(err)
	}
	defer runtimeStore.Close()
	if err := runtimeStore.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	store := NewOperationsStore(runtimeStore)
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	grant := operationsmodel.OperationsBreakGlassGrant{ID: "grant-1", WorkspaceID: "workspace-a", State: operationsmodel.OperationsBreakGlassActive, ActorID: "actor", ApproverIDs: []string{"a", "b"}, Reason: "incident", IncidentRef: "INC-1", AlertTarget: "pager", AuditEventID: "audit-1", ExpiresAt: now.Add(time.Hour), Revision: 1, CreatedAt: now, UpdatedAt: now}
	if created, err := store.CreateOperationsBreakGlass(t.Context(), grant); err != nil || !created {
		t.Fatalf("created=%v err=%v", created, err)
	}
	grant2 := grant
	grant2.ID, grant2.AuditEventID = "grant-2", "audit-2"
	if created, err := store.CreateOperationsBreakGlass(t.Context(), grant2); err != nil || created {
		t.Fatalf("second created=%v err=%v", created, err)
	}
	read, found, err := store.GetOperationsBreakGlass(t.Context(), grant.ID)
	if err != nil || !found || len(read.ApproverIDs) != 2 || !read.Active(now) {
		t.Fatalf("read=%#v found=%v err=%v", read, found, err)
	}
	revoked := now.Add(time.Minute)
	read.State, read.Revision, read.UpdatedAt, read.RevokedAt, read.RevokedBy, read.RevocationNote = operationsmodel.OperationsBreakGlassRevoked, 2, revoked, &revoked, "actor", "done"
	if changed, err := store.RevokeOperationsBreakGlass(t.Context(), read, 2); err != nil || changed {
		t.Fatalf("stale changed=%v err=%v", changed, err)
	}
	if changed, err := store.RevokeOperationsBreakGlass(t.Context(), read, 1); err != nil || !changed {
		t.Fatalf("changed=%v err=%v", changed, err)
	}
	listed, err := store.ListOperationsBreakGlass(t.Context(), "workspace-a", 10)
	if err != nil || len(listed) != 1 || listed[0].State != operationsmodel.OperationsBreakGlassRevoked {
		t.Fatalf("listed=%#v err=%v", listed, err)
	}
}
