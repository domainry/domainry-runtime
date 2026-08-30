package report

import (
	"path/filepath"
	"testing"

	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
)

func TestReportSnapshotStorePersistsIdempotentFencedScopedResults(t *testing.T) {
	store, err := database.OpenContext(t.Context(), config.Config{DatabaseDriver: "sqlite", DBPath: filepath.Join(t.TempDir(), "snapshot.db"), IntegrationSecretKey: "snapshot-test-key"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	repository := NewReportSnapshotStore(store)
	begin := reportcontract.ReportSnapshotBeginRequest{WorkspaceID: "workspace-a", ReportKey: "operations", AccessScopeHash: "scope-a", IdempotencyKey: "window-1", StartedAt: "2026-07-21T10:00:00Z", LeaseOwner: "worker-a", LeaseExpiresAt: "2026-07-21T10:02:00Z"}
	claim, err := repository.BeginReportSnapshot(t.Context(), begin)
	snapshot := claim.Snapshot
	if err != nil || !claim.Acquired() || snapshot.Status != "refreshing" {
		t.Fatalf("claim=%#v err=%v", claim, err)
	}
	runningClaim, err := repository.BeginReportSnapshot(t.Context(), begin)
	running := runningClaim.Snapshot
	if err != nil || runningClaim.Disposition != reportcontract.ReportSnapshotClaimRunning || running.ID != snapshot.ID || running.Status != "refreshing" {
		t.Fatalf("running claim=%#v err=%v", runningClaim, err)
	}
	takeoverRequest := begin
	takeoverRequest.StartedAt = "2026-07-21T10:03:00Z"
	takeoverRequest.LeaseOwner = "worker-b"
	takeoverRequest.LeaseExpiresAt = "2026-07-21T10:05:00Z"
	takeoverClaim, err := repository.BeginReportSnapshot(t.Context(), takeoverRequest)
	takeover := takeoverClaim.Snapshot
	if err != nil || !takeoverClaim.Acquired() || takeover.LeaseOwner != "worker-b" || takeover.FencingToken != snapshot.FencingToken+1 {
		t.Fatalf("takeover claim=%#v err=%v", takeoverClaim, err)
	}
	stale := snapshot
	stale.Status, stale.RefreshedAt = "succeeded", "2026-07-21T10:03:01Z"
	if err := repository.CompleteReportSnapshot(t.Context(), reportcontract.ReportSnapshotCompleteRequest{Snapshot: stale, ExpectedStatus: "refreshing", LeaseOwner: stale.LeaseOwner, FencingToken: stale.FencingToken}); err == nil {
		t.Fatal("expired snapshot lease completed after takeover")
	}
	snapshot = takeover
	snapshot.Status = "succeeded"
	snapshot.Summary = reportmodel.ReportSummary{Key: "operations", Rows: []reportmodel.ReportResultRow{{Measures: map[string]string{"total": "30.00"}}}, RowCount: 1, SourceRowCount: 2, ExecutionMode: "snapshot"}
	snapshot.Watermark = "2026-07-21T09:59:00Z"
	snapshot.SourceVersions = map[string]string{"entries": "2:hash"}
	snapshot.RefreshedAt = "2026-07-21T10:00:01Z"
	if err := repository.CompleteReportSnapshot(t.Context(), reportcontract.ReportSnapshotCompleteRequest{Snapshot: snapshot, ExpectedStatus: "refreshing", LeaseOwner: snapshot.LeaseOwner, FencingToken: snapshot.FencingToken}); err != nil {
		t.Fatal(err)
	}
	if err := repository.CompleteReportSnapshot(t.Context(), reportcontract.ReportSnapshotCompleteRequest{Snapshot: snapshot, ExpectedStatus: "refreshing", LeaseOwner: snapshot.LeaseOwner, FencingToken: snapshot.FencingToken}); err == nil {
		t.Fatal("stale completion was not fenced")
	}
	replayClaim, err := repository.BeginReportSnapshot(t.Context(), begin)
	replay := replayClaim.Snapshot
	if err != nil || replayClaim.Disposition != reportcontract.ReportSnapshotClaimReplay || replay.ID != snapshot.ID || replay.Summary.Rows[0].Measures["total"] != "30.00" {
		t.Fatalf("replay claim=%#v err=%v", replayClaim, err)
	}
	latest, ok, err := repository.LatestReportSnapshot(t.Context(), "workspace-a", "operations", "scope-a")
	if err != nil || !ok || latest.Watermark != snapshot.Watermark || latest.SourceVersions["entries"] != "2:hash" {
		t.Fatalf("latest=%#v ok=%v err=%v", latest, ok, err)
	}
	if _, ok, err := repository.LatestReportSnapshot(t.Context(), "workspace-a", "operations", "scope-b"); err != nil || ok {
		t.Fatalf("cross-scope ok=%v err=%v", ok, err)
	}
	failedClaim, err := repository.BeginReportSnapshot(t.Context(), reportcontract.ReportSnapshotBeginRequest{WorkspaceID: "workspace-a", ReportKey: "operations", AccessScopeHash: "scope-a", IdempotencyKey: "window-2", StartedAt: "2026-07-21T11:00:00Z", LeaseOwner: "worker-a", LeaseExpiresAt: "2026-07-21T11:02:00Z"})
	failed := failedClaim.Snapshot
	if err != nil || !failedClaim.Acquired() {
		t.Fatalf("failed claim=%#v err=%v", failedClaim, err)
	}
	if err := repository.FailReportSnapshot(t.Context(), reportcontract.ReportSnapshotFailRequest{WorkspaceID: failed.WorkspaceID, ID: failed.ID, ExpectedStatus: "refreshing", ErrorCode: "backend.report.snapshot_source_changed", LeaseOwner: failed.LeaseOwner, FencingToken: failed.FencingToken}); err != nil {
		t.Fatal(err)
	}
	recoveredClaim, err := repository.BeginReportSnapshot(t.Context(), reportcontract.ReportSnapshotBeginRequest{WorkspaceID: "workspace-a", ReportKey: "operations", AccessScopeHash: "scope-a", IdempotencyKey: "window-2", StartedAt: "2026-07-21T11:01:00Z", LeaseOwner: "worker-b", LeaseExpiresAt: "2026-07-21T11:03:00Z"})
	recovered := recoveredClaim.Snapshot
	if err != nil || !recoveredClaim.Acquired() || recovered.Status != "refreshing" || recovered.ErrorCode != "" {
		t.Fatalf("recovered claim=%#v err=%v", recoveredClaim, err)
	}
}
