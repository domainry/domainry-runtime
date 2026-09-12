package runtime

import (
	"context"
	"testing"

	reportsdk "github.com/domainry/domainry-report-sdk"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	reportmodulehost "github.com/domainry/domainry-report-sdk/modulehost"
	reportmodule "github.com/domainry/domainry-report/module"
	"github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

type runtimeReportAnalysisTableSourceStub struct{}

func (*runtimeReportAnalysisTableSourceStub) ReportAnalysisSources(context.Context, reportmodel.ReportSubject) ([]reportmodel.AnalysisDataset, error) {
	return nil, nil
}
func (*runtimeReportAnalysisTableSourceStub) ReadReportAnalysisTableVersion(context.Context, string, []string, reportmodel.ReportSubject) (reportmodulehost.AnalysisTableVersion, error) {
	return reportmodulehost.AnalysisTableVersion{}, nil
}
func (*runtimeReportAnalysisTableSourceStub) StreamReportAnalysisTable(context.Context, reportmodulehost.AnalysisTableVersion, []string, reportmodel.ReportSubject, func(reportmodel.AnalysisTableRow) error) (reportmodulehost.AnalysisTableVersion, error) {
	return reportmodulehost.AnalysisTableVersion{}, nil
}

func TestRuntimeReportApplicationHostExposesComposedAnalysisTableSource(t *testing.T) {
	source := &runtimeReportAnalysisTableSourceStub{}
	host := runtimeReportApplicationHost{ports: composition.ReportModuleApplicationPorts{Tables: source}}
	if host.ReportAnalysisTables() != source {
		t.Fatal("Report host replaced the project-owned table source")
	}
}

func TestReportModuleAdoptsRuntimeSnapshotTableAndOwnsDefinitions(t *testing.T) {
	store := openAgentBindingRuntimeStore(t)
	if err := store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	_, err := store.DB().ExecContext(t.Context(), `CREATE TABLE _report_snapshots (
		id TEXT PRIMARY KEY, workspace_id TEXT NOT NULL, report_key TEXT NOT NULL,
		access_scope_hash TEXT NOT NULL, idempotency_key TEXT NOT NULL, status TEXT NOT NULL,
		summary_json TEXT NOT NULL, watermark TEXT NOT NULL, source_versions_json TEXT NOT NULL,
		row_count BIGINT NOT NULL, source_row_count BIGINT NOT NULL, started_at TEXT NOT NULL,
		refreshed_at TEXT NOT NULL, error_code TEXT NOT NULL, lease_owner TEXT NOT NULL,
		lease_expires_at TEXT NOT NULL, fencing_token BIGINT NOT NULL,
		UNIQUE (workspace_id, id), UNIQUE (workspace_id, report_key, access_scope_hash, idempotency_key)
	)`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `CREATE INDEX idx_report_snapshot_latest ON _report_snapshots (workspace_id, report_key, access_scope_hash, status, refreshed_at)`); err != nil {
		t.Fatal(err)
	}
	if _, err := store.DB().ExecContext(t.Context(), `INSERT INTO _report_snapshots VALUES ('snapshot-1','workspace-primary','summary','scope','request','succeeded','{}','','{}',1,1,'now','now','','','',0)`); err != nil {
		t.Fatal(err)
	}
	binding, err := reportmodule.NewFactory().Open(t.Context(), reportsdk.ApplicationRef{RuntimeID: "runtime-a"}, runtimeReportModuleHost{store: store})
	if err != nil {
		t.Fatal(err)
	}
	manifest := manifestmodel.ManifestSchema{TemplateID: "template", Version: "7", Reports: []reportmodel.ReportSchema{{Key: "summary", Name: "Summary"}}}
	if err := SynchronizeReportDefinitions(t.Context(), binding, manifest); err != nil {
		t.Fatal(err)
	}
	var snapshots, definitions, migrations int
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _report_snapshots WHERE id='snapshot-1'`).Scan(&snapshots); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _report_definitions WHERE resource_key='summary' AND schema_version='7'`).Scan(&definitions); err != nil {
		t.Fatal(err)
	}
	if err := store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _schema_migrations WHERE kind='module:report' AND dirty=FALSE`).Scan(&migrations); err != nil {
		t.Fatal(err)
	}
	if snapshots != 1 || definitions != 1 || migrations != 1 {
		t.Fatalf("snapshots=%d definitions=%d migrations=%d", snapshots, definitions, migrations)
	}
}
