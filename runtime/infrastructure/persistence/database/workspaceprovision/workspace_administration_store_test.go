package workspaceprovision

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	workspaceprovisionapplication "github.com/domainry/domainry-runtime/runtime/application/workspaceprovision"
	workspaceprovisionmodel "github.com/domainry/domainry-runtime/runtime/domain/workspaceprovision/model"
	runtimeauditmodule "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/auditmodule"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-runtime/testsupport/auditmodulefixture"
)

func TestWorkspaceAdministrationStoreCatalogLifecycleCASAndRollback(t *testing.T) {
	runtimeStore, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "workspace-administration.db")})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtimeStore.CloseContext(context.Background()) })
	if err := runtimeStore.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	auditmodulefixture.Bind(t, t.Context(), runtimeStore)
	if _, err := runtimeStore.DB().ExecContext(t.Context(), `CREATE TABLE _identity_auth_refresh_tokens (
		workspace_id TEXT NOT NULL, user_id TEXT NOT NULL, session_id TEXT NOT NULL, expires_at INTEGER NOT NULL,
		revoked_at INTEGER, last_used_at INTEGER, updated_at INTEGER NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	nowInstant := time.Now().UTC()
	now, nowText := nowInstant.UnixMilli(), nowInstant.Format(time.RFC3339Nano)
	insertWorkspaceAdministrationFixture(t, runtimeStore.DB(), "workspace-initial", "omega", "Initial", "installation-a", now)
	insertWorkspaceAdministrationFixture(t, runtimeStore.DB(), "workspace-target", "alpha", "Target", "", now)
	expires := nowInstant.Add(time.Hour).UnixMilli()
	if _, err := runtimeStore.DB().ExecContext(t.Context(), `INSERT INTO _identity_auth_refresh_tokens(workspace_id,user_id,session_id,expires_at,revoked_at,last_used_at,updated_at) VALUES(?,?,?,?,?,?,?)`, "workspace-target", "staff-a", "session-a", expires, int64(0), int64(0), now); err != nil {
		t.Fatal(err)
	}
	store := NewWorkspaceAdministrationStore(runtimeStore)
	items, more, err := store.ListWorkspaceCatalog(t.Context(), "", 1)
	if err != nil || !more || len(items) != 1 || items[0].CanonicalCode != "alpha" {
		t.Fatalf("catalog items=%+v more=%t err=%v", items, more, err)
	}
	next, more, err := store.ListWorkspaceCatalog(t.Context(), "alpha", 1)
	if err != nil || more || len(next) != 1 || next[0].CanonicalCode != "omega" {
		t.Fatalf("catalog next=%+v more=%t err=%v", next, more, err)
	}

	actor := workspaceprovisionmodel.AdministrationActor{WorkspaceID: "workspace-initial", UserID: "admin-a", RoleKey: workspaceprovisionapplication.WorkspaceAdministratorRoleKey, RequestID: "request-a", CausationID: "cause-a", AuthorizationRevision: "auth-1"}
	suspended, err := store.SetWorkspaceStatus(t.Context(), actor, "alpha", 1, workspaceprovisionmodel.WorkspaceStatusSuspended, "suspend-1")
	if err != nil || suspended.Replayed || suspended.Workspace.Status != workspaceprovisionmodel.WorkspaceStatusSuspended || suspended.Workspace.Revision != 2 || suspended.RevokedSessions != 1 {
		t.Fatalf("suspend=%+v err=%v", suspended, err)
	}
	assertWorkspaceAdministrationState(t, runtimeStore.DB(), "workspace-target", "suspended", 2, "session-a", true)
	assertWorkspaceAdministrationEvidence(t, runtimeStore.DB(), 1, 1)
	var operationID, causationID string
	if err := runtimeStore.DB().QueryRowContext(t.Context(), `SELECT operation_id,causation_id FROM _audit_events WHERE event=?`, workspaceprovisionapplication.SuspendWorkspaceActionKey).Scan(&operationID, &causationID); err != nil || operationID != mustWorkspaceAdministrationOperationID(t, actor, workspaceprovisionapplication.SuspendWorkspaceActionKey, "suspend-1") || causationID != "cause-a" {
		t.Fatalf("workspace administration audit operation=%q causation=%q err=%v", operationID, causationID, err)
	}
	replay, err := store.SetWorkspaceStatus(t.Context(), actor, "alpha", 1, workspaceprovisionmodel.WorkspaceStatusSuspended, "suspend-1")
	if err != nil || !replay.Replayed || replay.Workspace.Revision != 2 || replay.RevokedSessions != 1 {
		t.Fatalf("suspend replay=%+v err=%v", replay, err)
	}
	assertWorkspaceAdministrationEvidence(t, runtimeStore.DB(), 1, 1)
	if _, err := store.SetWorkspaceStatus(t.Context(), actor, "alpha", 2, workspaceprovisionmodel.WorkspaceStatusSuspended, "suspend-1"); !errors.Is(err, workspaceprovisionmodel.ErrIdempotencyConflict) {
		t.Fatalf("fingerprint conflict=%v", err)
	}

	reactivated, err := store.SetWorkspaceStatus(t.Context(), actor, "alpha", 2, workspaceprovisionmodel.WorkspaceStatusActive, "reactivate-1")
	if err != nil || reactivated.Workspace.Status != workspaceprovisionmodel.WorkspaceStatusActive || reactivated.Workspace.Revision != 3 || reactivated.RevokedSessions != 0 {
		t.Fatalf("reactivate=%+v err=%v", reactivated, err)
	}
	assertWorkspaceAdministrationState(t, runtimeStore.DB(), "workspace-target", "active", 3, "session-a", true)

	if _, err := runtimeStore.DB().ExecContext(t.Context(), `INSERT INTO _identity_auth_refresh_tokens(workspace_id,user_id,session_id,expires_at,revoked_at,last_used_at,updated_at) VALUES(?,?,?,?,?,?,?)`, "workspace-target", "staff-b", "session-b", expires, int64(0), int64(0), now); err != nil {
		t.Fatal(err)
	}
	failingKey := "suspend-audit-failure"
	auditID := mustWorkspaceAdministrationOperationID(t, actor, workspaceprovisionapplication.SuspendWorkspaceActionKey, failingKey)
	insertWorkspaceAdministrationAuditCollision(t, runtimeStore, auditID, nowText)
	if _, err := store.SetWorkspaceStatus(t.Context(), actor, "alpha", 3, workspaceprovisionmodel.WorkspaceStatusSuspended, failingKey); err == nil {
		t.Fatal("audit failure committed Workspace lifecycle mutation")
	}
	assertWorkspaceAdministrationState(t, runtimeStore.DB(), "workspace-target", "active", 3, "session-b", false)
	if _, err := store.SetWorkspaceStatus(t.Context(), actor, "omega", 1, workspaceprovisionmodel.WorkspaceStatusSuspended, "initial-suspend"); !errors.Is(err, workspaceprovisionmodel.ErrInitialWorkspaceSuspension) {
		t.Fatalf("initial Workspace suspend=%v", err)
	}
}

func insertWorkspaceAdministrationAuditCollision(t *testing.T, store *database.RuntimeStore, operationID, now string) {
	t.Helper()
	auditID, err := workspaceAdministrationAuditEventID(operationID)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.AppendPreparedAuditWithin(t.Context(), runtimeauditmodule.NewTransaction(store.DB()), auditmodel.AuditEvent{
		AuditEventID: auditID, WorkspaceID: "workspace-target", Family: auditmodel.EventFamilyRuntimeWorkspace, Event: "existing", ActorID: "test", RoleKey: "test", Summary: "collision", Metadata: map[string]any{"action_key": "collision"}, CreatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
}

func mustWorkspaceAdministrationOperationID(t *testing.T, actor workspaceprovisionmodel.AdministrationActor, actionKey, idempotencyKey string) string {
	t.Helper()
	operationID, err := workspaceAdministrationOperationID(actor, actionKey, idempotencyKey)
	if err != nil {
		t.Fatal(err)
	}
	return operationID
}

func insertWorkspaceAdministrationFixture(t *testing.T, db *sql.DB, id, code, name, installationIdentity string, now int64) {
	t.Helper()
	var installation any
	if installationIdentity != "" {
		installation = installationIdentity
	}
	if _, err := db.ExecContext(t.Context(), `INSERT INTO _workspaces(
		workspace_id,canonical_code,name,status,initial_installation_identity,revision,created_at,updated_at
	) VALUES(?,?,?,?,?,?,?,?)`, id, code, name, "active", installation, 1, now, now); err != nil {
		t.Fatal(err)
	}
}

func assertWorkspaceAdministrationState(t *testing.T, db *sql.DB, workspaceID, status string, revision int, sessionID string, revoked bool) {
	t.Helper()
	var gotStatus string
	var gotRevision int
	if err := db.QueryRowContext(t.Context(), `SELECT status,revision FROM _workspaces WHERE workspace_id=?`, workspaceID).Scan(&gotStatus, &gotRevision); err != nil || gotStatus != status || gotRevision != revision {
		t.Fatalf("workspace status=%q revision=%d err=%v", gotStatus, gotRevision, err)
	}
	var revokedAt sql.NullInt64
	if err := db.QueryRowContext(t.Context(), `SELECT revoked_at FROM _identity_auth_refresh_tokens WHERE session_id=?`, sessionID).Scan(&revokedAt); err != nil || (revokedAt.Valid && revokedAt.Int64 != 0) != revoked {
		t.Fatalf("session=%q revoked=%v value=%+v err=%v", sessionID, revoked, revokedAt, err)
	}
}

func assertWorkspaceAdministrationEvidence(t *testing.T, db *sql.DB, audits, receipts int) {
	t.Helper()
	var gotAudits, gotReceipts int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _audit_events WHERE event=?`, workspaceprovisionapplication.SuspendWorkspaceActionKey).Scan(&gotAudits); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _operations WHERE system_purpose = 'workspace_administration' AND owner = 'workspace' AND kind = 'workspace.administration'`).Scan(&gotReceipts); err != nil {
		t.Fatal(err)
	}
	var legacyTables int
	if err := db.QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = '_workspace_administration_receipts_v1'`).Scan(&legacyTables); err != nil || legacyTables != 0 {
		t.Fatalf("legacy Workspace administration receipt tables=%d err=%v", legacyTables, err)
	}
	if gotAudits != audits || gotReceipts != receipts {
		t.Fatalf("audits=%d receipts=%d want=%d/%d", gotAudits, gotReceipts, audits, receipts)
	}
}
