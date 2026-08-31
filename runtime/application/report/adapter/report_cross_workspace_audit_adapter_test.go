package adapter

import (
	"context"
	"testing"

	auditcontract "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

type reportAuditAppenderProbe struct {
	request auditcontract.AuditAppendRequest
}

func (p *reportAuditAppenderProbe) AppendAudit(_ context.Context, request auditcontract.AuditAppendRequest) error {
	p.request = request
	return nil
}

func TestCrossWorkspaceAuditAdapterRedactsResultRows(t *testing.T) {
	probe := &reportAuditAppenderProbe{}
	adapter := NewReportCrossWorkspaceAuditAdapter(probe)
	report := reportmodel.ReportSchema{Key: "hq", ExecutionScope: &reportmodel.ReportExecutionScopeSchema{Mode: reportmodel.ReportExecutionScopeCrossWorkspaceAggregateV1}}
	summary := reportmodel.ReportSummary{RowCount: 2, ExecutionMode: "object_sql_v1", Rows: []reportmodel.ReportResultRow{{Dimensions: map[string]string{"workspace_id": "secret"}}}}
	if err := adapter.AppendCrossWorkspaceExecution(t.Context(), report, summary, principalmodel.Principal{}); err != nil {
		t.Fatal(err)
	}
	if probe.request.Event != "report_cross_workspace_aggregate_executed" || probe.request.Metadata["result_row_count"] != 2 {
		t.Fatalf("audit=%#v", probe.request)
	}
	if _, exists := probe.request.Metadata["rows"]; exists {
		t.Fatalf("audit leaked result rows: %#v", probe.request.Metadata)
	}
}
