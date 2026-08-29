package report

import (
	"database/sql"
	"database/sql/driver"
	"errors"
	"testing"

	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

func reportSnapshotColumns() []string {
	columns := make([]string, 12)
	for index := range columns {
		columns[index] = "column"
	}
	return columns
}

func reportSnapshotQueryStep(status, summary, versions string) reportDatasetSQLStep {
	return reportDatasetSQLStep{columns: reportSnapshotColumns(), rows: [][]driver.Value{{
		"snapshot", "workspace-a", "operations", "scope-a", "window-1", status,
		summary, "watermark", versions, "started", "refreshed", "",
	}}}
}

func reportSnapshotEmptyQueryStep() reportDatasetSQLStep {
	return reportDatasetSQLStep{columns: reportSnapshotColumns()}
}

func reportSnapshotScriptedRepository(t *testing.T, state *reportDatasetSQLState) *ReportSnapshotStore {
	t.Helper()
	runtimeStore := reportDatasetEdgeStore(t)
	db := sql.OpenDB(reportDatasetConnector{state: state})
	t.Cleanup(func() { _ = db.Close() })
	repository := NewReportSnapshotStore(runtimeStore)
	repository.db = db
	return repository
}

func reportSnapshotValidBeginRequest() reportcontract.ReportSnapshotBeginRequest {
	return reportcontract.ReportSnapshotBeginRequest{WorkspaceID: "workspace-a", ReportKey: "operations", AccessScopeHash: "scope-a", IdempotencyKey: "window-1", StartedAt: "started"}
}

func TestReportSnapshotBeginValidationAndSQLFailureConditions(t *testing.T) {
	base := reportSnapshotValidBeginRequest()
	invalid := []reportcontract.ReportSnapshotBeginRequest{base, base, base, base, base}
	invalid[0].WorkspaceID = ""
	invalid[1].ReportKey = ""
	invalid[2].AccessScopeHash = ""
	invalid[3].IdempotencyKey = ""
	invalid[4].StartedAt = ""
	for index, request := range invalid {
		if _, _, err := reportSnapshotScriptedRepository(t, &reportDatasetSQLState{}).BeginReportSnapshot(t.Context(), request); err == nil {
			t.Fatalf("invalid request %d accepted", index)
		}
	}

	wantErr := errors.New("snapshot SQL failure")
	repository := reportSnapshotScriptedRepository(t, &reportDatasetSQLState{steps: []reportDatasetSQLStep{{err: wantErr}}})
	if _, _, err := repository.BeginReportSnapshot(t.Context(), base); !errors.Is(err, wantErr) {
		t.Fatalf("initial read error=%v", err)
	}
	repository = reportSnapshotScriptedRepository(t, &reportDatasetSQLState{
		steps:     []reportDatasetSQLStep{reportSnapshotQueryStep("failed", `{}`, `{}`)},
		execSteps: []reportDatasetSQLExecStep{{err: wantErr}},
	})
	if _, _, err := repository.BeginReportSnapshot(t.Context(), base); !errors.Is(err, wantErr) {
		t.Fatalf("refresh update error=%v", err)
	}

	for _, reread := range []reportDatasetSQLStep{
		reportSnapshotQueryStep("succeeded", `{}`, `{}`),
		{err: wantErr},
		reportSnapshotEmptyQueryStep(),
	} {
		repository = reportSnapshotScriptedRepository(t, &reportDatasetSQLState{
			steps:     []reportDatasetSQLStep{reportSnapshotEmptyQueryStep(), reread},
			execSteps: []reportDatasetSQLExecStep{{err: wantErr}},
		})
		snapshot, execute, err := repository.BeginReportSnapshot(t.Context(), base)
		if reread.rows != nil {
			if err != nil || execute || snapshot.Status != "succeeded" {
				t.Fatalf("race reread snapshot=%#v execute=%v err=%v", snapshot, execute, err)
			}
		} else if !errors.Is(err, wantErr) {
			t.Fatalf("failed insert reread error=%v", err)
		}
	}
}

func TestReportSnapshotCompleteFailAndScanFailureConditions(t *testing.T) {
	wantErr := errors.New("snapshot mutation failure")
	snapshot := reportmodel.ReportSnapshot{ID: "snapshot", WorkspaceID: "workspace", Summary: reportmodel.ReportSummary{}, SourceVersions: map[string]string{}}
	request := reportcontract.ReportSnapshotCompleteRequest{Snapshot: snapshot, ExpectedStatus: "refreshing"}
	for _, call := range []func(*ReportSnapshotStore) error{
		func(repository *ReportSnapshotStore) error {
			return repository.CompleteReportSnapshot(t.Context(), request)
		},
		func(repository *ReportSnapshotStore) error {
			return repository.FailReportSnapshot(t.Context(), reportcontract.ReportSnapshotFailRequest{WorkspaceID: "workspace", ID: "snapshot", ExpectedStatus: "refreshing", ErrorCode: "failed"})
		},
	} {
		repository := reportSnapshotScriptedRepository(t, &reportDatasetSQLState{execSteps: []reportDatasetSQLExecStep{{err: wantErr}}})
		if err := call(repository); !errors.Is(err, wantErr) {
			t.Fatalf("mutation error=%v", err)
		}
	}
	if err := reportSnapshotRequireAffected(reportDatasetResult{err: wantErr}); !errors.Is(err, wantErr) {
		t.Fatalf("rows affected error=%v", err)
	}

	for _, step := range []reportDatasetSQLStep{
		{columns: []string{"bad"}, rows: [][]driver.Value{{"bad"}}},
		reportSnapshotQueryStep("succeeded", `{`, `{}`),
		reportSnapshotQueryStep("succeeded", `{}`, `{`),
	} {
		repository := reportSnapshotScriptedRepository(t, &reportDatasetSQLState{steps: []reportDatasetSQLStep{step}})
		if _, _, err := repository.LatestReportSnapshot(t.Context(), "workspace-a", "operations", "scope-a"); err == nil {
			t.Fatalf("invalid snapshot row accepted: %+v", step)
		}
	}
}
