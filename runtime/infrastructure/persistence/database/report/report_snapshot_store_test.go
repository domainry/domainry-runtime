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
	begin := reportcontract.ReportSnapshotBeginRequest{WorkspaceID: "workspace-a", ReportKey: "operations", AccessScopeHash: "scope-a", IdempotencyKey: "window-1", StartedAt: "2026-07-21T10:00:00Z"}
	snapshot, execute, err := repository.BeginReportSnapshot(t.Context(), begin)
	if err != nil || !execute || snapshot.Status != "refreshing" {
		t.Fatalf("snapshot=%#v execute=%v err=%v", snapshot, execute, err)
	}
	snapshot.Status = "succeeded"
	snapshot.Summary = reportmodel.ReportSummary{Key: "operations", Rows: []reportmodel.ReportResultRow{{Measures: map[string]string{"total": "30.00"}}}, RowCount: 1, SourceRowCount: 2, ExecutionMode: "snapshot"}
	snapshot.Watermark = "2026-07-21T09:59:00Z"
	snapshot.SourceVersions = map[string]string{"entries": "2:hash"}
	snapshot.RefreshedAt = "2026-07-21T10:00:01Z"
	if err := repository.CompleteReportSnapshot(t.Context(), reportcontract.ReportSnapshotCompleteRequest{Snapshot: snapshot, ExpectedStatus: "refreshing"}); err != nil {
		t.Fatal(err)
	}
	if err := repository.CompleteReportSnapshot(t.Context(), reportcontract.ReportSnapshotCompleteRequest{Snapshot: snapshot, ExpectedStatus: "refreshing"}); err == nil {
		t.Fatal("stale completion was not fenced")
	}
	replay, execute, err := repository.BeginReportSnapshot(t.Context(), begin)
	if err != nil || execute || replay.ID != snapshot.ID || replay.Summary.Rows[0].Measures["total"] != "30.00" {
		t.Fatalf("replay=%#v execute=%v err=%v", replay, execute, err)
	}
	latest, ok, err := repository.LatestReportSnapshot(t.Context(), "workspace-a", "operations", "scope-a")
	if err != nil || !ok || latest.Watermark != snapshot.Watermark || latest.SourceVersions["entries"] != "2:hash" {
		t.Fatalf("latest=%#v ok=%v err=%v", latest, ok, err)
	}
	if _, ok, err := repository.LatestReportSnapshot(t.Context(), "workspace-a", "operations", "scope-b"); err != nil || ok {
		t.Fatalf("cross-scope ok=%v err=%v", ok, err)
	}
	failed, execute, err := repository.BeginReportSnapshot(t.Context(), reportcontract.ReportSnapshotBeginRequest{WorkspaceID: "workspace-a", ReportKey: "operations", AccessScopeHash: "scope-a", IdempotencyKey: "window-2", StartedAt: "2026-07-21T11:00:00Z"})
	if err != nil || !execute {
		t.Fatalf("failed begin=%#v execute=%v err=%v", failed, execute, err)
	}
	if err := repository.FailReportSnapshot(t.Context(), failed.ID, "refreshing", "backend.report.snapshot_source_changed"); err != nil {
		t.Fatal(err)
	}
	recovered, execute, err := repository.BeginReportSnapshot(t.Context(), reportcontract.ReportSnapshotBeginRequest{WorkspaceID: "workspace-a", ReportKey: "operations", AccessScopeHash: "scope-a", IdempotencyKey: "window-2", StartedAt: "2026-07-21T11:01:00Z"})
	if err != nil || !execute || recovered.Status != "refreshing" || recovered.ErrorCode != "" {
		t.Fatalf("recovered=%#v execute=%v err=%v", recovered, execute, err)
	}
}
