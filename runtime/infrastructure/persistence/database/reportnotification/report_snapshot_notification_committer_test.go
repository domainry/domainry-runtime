package reportnotification

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	reportsdkpersistence "github.com/domainry/domainry-report-sdk/persistence"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	"github.com/domainry/domainry-runtime/testsupport/notificationsdkfixture"
	"github.com/domainry/domainry-runtime/testsupport/reportmodulefixture"
)

type reportSnapshotNotificationCommitterContract interface {
	CompleteReportSnapshotWithNotification(context.Context, reportsdkpersistence.SnapshotCompleteRequest, notificationmodel.NotificationEvent) error
	FailReportSnapshotWithNotification(context.Context, reportsdkpersistence.SnapshotFailRequest, notificationmodel.NotificationEvent) error
}

var _ reportSnapshotNotificationCommitterContract = ReportSnapshotNotificationCommitter{}

func TestReportSnapshotCompletionAndNotificationAreAtomic(t *testing.T) {
	store := openReportNotificationStore(t)
	defer store.Close()
	snapshots := openReportSnapshotRepository(t, store)
	committer := NewReportSnapshotNotificationCommitter(store, snapshots)

	first := beginReportSnapshot(t, snapshots, "first")
	first.Status, first.RefreshedAt = "succeeded", "2026-07-28T01:00:00Z"
	event := reportNotificationEvent("report-event-first", "report-snapshot:shared:completed")
	if err := committer.CompleteReportSnapshotWithNotification(t.Context(), reportsdkpersistence.SnapshotCompleteRequest{Snapshot: first, ExpectedStatus: "refreshing", LeaseOwner: first.LeaseOwner, FencingToken: first.FencingToken}, event); err != nil {
		t.Fatal(err)
	}
	assertReportSnapshotStatus(t, store, first.ID, "succeeded")

	rollback := beginReportSnapshot(t, snapshots, "rollback")
	rollback.Status, rollback.RefreshedAt = "succeeded", "2026-07-28T01:01:00Z"
	duplicate := reportNotificationEvent("report-event-duplicate", event.SourceEventID)
	if err := committer.CompleteReportSnapshotWithNotification(t.Context(), reportsdkpersistence.SnapshotCompleteRequest{Snapshot: rollback, ExpectedStatus: "refreshing", LeaseOwner: rollback.LeaseOwner, FencingToken: rollback.FencingToken}, duplicate); err == nil {
		t.Fatal("expected duplicate notification identity to reject completion")
	}
	assertReportSnapshotStatus(t, store, rollback.ID, "refreshing")
}

func TestReportSnapshotFailureAndNotificationAreAtomic(t *testing.T) {
	store := openReportNotificationStore(t)
	defer store.Close()
	snapshots := openReportSnapshotRepository(t, store)
	committer := NewReportSnapshotNotificationCommitter(store, snapshots)

	seed := beginReportSnapshot(t, snapshots, "seed")
	event := reportNotificationEvent("report-failed-seed", "report-snapshot:shared:failed")
	if err := committer.FailReportSnapshotWithNotification(t.Context(), reportsdkpersistence.SnapshotFailRequest{WorkspaceID: seed.WorkspaceID, ID: seed.ID, ExpectedStatus: "refreshing", ErrorCode: "backend.report.snapshot_refresh_failed", LeaseOwner: seed.LeaseOwner, FencingToken: seed.FencingToken}, event); err != nil {
		t.Fatal(err)
	}
	assertReportSnapshotStatus(t, store, seed.ID, "failed")

	rollback := beginReportSnapshot(t, snapshots, "failed-rollback")
	duplicate := reportNotificationEvent("report-failed-duplicate", event.SourceEventID)
	if err := committer.FailReportSnapshotWithNotification(t.Context(), reportsdkpersistence.SnapshotFailRequest{WorkspaceID: rollback.WorkspaceID, ID: rollback.ID, ExpectedStatus: "refreshing", ErrorCode: "backend.report.snapshot_refresh_failed", LeaseOwner: rollback.LeaseOwner, FencingToken: rollback.FencingToken}, duplicate); err == nil {
		t.Fatal("expected duplicate notification identity to roll failure back")
	}
	assertReportSnapshotStatus(t, store, rollback.ID, "refreshing")
}

func TestReportSnapshotNotificationCommitStagesFailClosed(t *testing.T) {
	wantErr := errors.New("commit stage unavailable")
	closed := openReportNotificationStore(t)
	closedSnapshots := openReportSnapshotRepository(t, closed)
	committer := NewReportSnapshotNotificationCommitter(closed, closedSnapshots)
	if err := closed.Close(); err != nil {
		t.Fatal(err)
	}
	if err := committer.commit(t.Context(), reportNotificationEvent("begin", "begin"), func(context.Context) error { return nil }); err == nil {
		t.Fatal("begin failure was accepted")
	}
	store := openReportNotificationStore(t)
	defer store.Close()
	snapshots := openReportSnapshotRepository(t, store)
	committer = NewReportSnapshotNotificationCommitter(store, snapshots)
	if err := committer.commit(t.Context(), reportNotificationEvent("update", "update"), func(context.Context) error { return wantErr }); !errors.Is(err, wantErr) {
		t.Fatalf("update error=%v", err)
	}
	committer.commitTx = func(*sql.Tx) error { return wantErr }
	if err := committer.commit(t.Context(), reportNotificationEvent("commit", "commit"), func(context.Context) error { return nil }); !errors.Is(err, wantErr) {
		t.Fatalf("commit error=%v", err)
	}
}

func openReportNotificationStore(t *testing.T) *database.RuntimeStore {
	t.Helper()
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "report-notification.db")})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := reportmodulefixture.EnsureSchema(t.Context(), store); err != nil {
		store.Close()
		t.Fatal(err)
	}
	if err := notificationsdkfixture.BindTransactions(store); err != nil {
		store.Close()
		t.Fatal(err)
	}
	return store
}

func openReportSnapshotRepository(t *testing.T, store *database.RuntimeStore) reportsdkpersistence.SnapshotRepository {
	t.Helper()
	binding, err := reportmodulefixture.Open(t.Context(), store)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = binding.Close(context.WithoutCancel(t.Context())) })
	return binding.Snapshots()
}

func beginReportSnapshot(t *testing.T, snapshots reportsdkpersistence.SnapshotRepository, key string) reportsdkpersistence.Snapshot {
	t.Helper()
	claim, err := snapshots.Claim(t.Context(), reportsdkpersistence.SnapshotBeginRequest{
		WorkspaceID: "workspace-a", ReportKey: "revenue", AccessScopeHash: "scope", IdempotencyKey: key, StartedAt: "2026-07-28T00:00:00Z", LeaseOwner: "worker-" + key, LeaseExpiresAt: "2026-07-28T00:02:00Z",
	})
	if err != nil || claim.Disposition != reportsdkpersistence.SnapshotClaimAcquired {
		t.Fatalf("claim=%+v err=%v", claim, err)
	}
	return claim.Snapshot
}

func reportNotificationEvent(id, sourceID string) notificationmodel.NotificationEvent {
	return notificationmodel.NotificationEvent{
		ID: id, WorkspaceID: "workspace-a", Source: "report", SourceEventID: sourceID, EventType: "report.snapshot.completed",
		Category: "long_task", Severity: "info", RecipientUserIDs: []string{"user-1"},
		SubjectType: "report", SubjectID: "revenue", OccurredAt: "2026-07-28T01:00:00Z", Status: "queued",
		CreatedAt: "2026-07-28T01:00:00Z", UpdatedAt: "2026-07-28T01:00:00Z",
	}
}

func assertReportSnapshotStatus(t *testing.T, store *database.RuntimeStore, id, want string) {
	t.Helper()
	var status string
	query := "SELECT status FROM " + store.TableIdentifier("_report_snapshots") + " WHERE id = " + store.Placeholder(1)
	if err := store.DB().QueryRowContext(t.Context(), query, id).Scan(&status); err != nil || status != want {
		t.Fatalf("snapshot=%s status=%s want=%s err=%v", id, status, want, err)
	}
}
