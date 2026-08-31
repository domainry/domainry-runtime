package query

import (
	"context"
	"errors"
	"testing"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type crossWorkspaceAuditProbe struct {
	report  reportmodel.ReportSchema
	summary reportmodel.ReportSummary
	err     error
}

func (p *crossWorkspaceAuditProbe) AppendCrossWorkspaceExecution(_ context.Context, report reportmodel.ReportSchema, summary reportmodel.ReportSummary, _ principalmodel.Principal) error {
	p.report, p.summary = report, summary
	return p.err
}

func TestCrossWorkspaceSummaryPersistsMandatoryRedactedAudit(t *testing.T) {
	audit := &crossWorkspaceAuditProbe{}
	service := NewReportQueryApplicationService(ReportQueryApplicationDependencies{Audit: audit})
	report := reportmodel.ReportSchema{Key: "hq", ExecutionScope: &reportmodel.ReportExecutionScopeSchema{Mode: reportmodel.ReportExecutionScopeCrossWorkspaceAggregateV1}}
	summary := reportmodel.ReportSummary{RowCount: 2, ExecutionMode: "object_sql_v1", Rows: []reportmodel.ReportResultRow{{Dimensions: map[string]string{"workspace_id": "secret"}}}}
	if err := service.auditCrossWorkspaceReport(t.Context(), report, summary, principalmodel.Principal{}); err != nil {
		t.Fatal(err)
	}
	if audit.report.Key != "hq" || audit.summary.RowCount != 2 {
		t.Fatalf("audit report=%#v summary=%#v", audit.report, audit.summary)
	}
	audit.err = errors.New("audit unavailable")
	if err := service.auditCrossWorkspaceReport(t.Context(), report, reportmodel.ReportSummary{}, principalmodel.Principal{}); err == nil {
		t.Fatal("mandatory audit failure was ignored")
	}
}
