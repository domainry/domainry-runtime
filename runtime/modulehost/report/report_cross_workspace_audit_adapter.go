package reportmodulehost

import (
	"context"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
	auditcontract "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type ReportCrossWorkspaceAuditAdapter struct {
	appender auditcontract.AuditAppender
}

func NewReportCrossWorkspaceAuditAdapter(appender auditcontract.AuditAppender) *ReportCrossWorkspaceAuditAdapter {
	return &ReportCrossWorkspaceAuditAdapter{appender: appender}
}

func (a *ReportCrossWorkspaceAuditAdapter) AppendCrossWorkspaceExecution(ctx context.Context, report reportmodel.ReportSchema, summary reportmodel.ReportSummary, principal principalmodel.Principal) error {
	return a.appender.AppendAudit(ctx, auditcontract.AuditAppendRequest{
		Event: "report_cross_workspace_aggregate_executed", ObjectKey: "report", RecordID: report.Key, Principal: principal,
		Summary:  "Executed governed cross-workspace aggregate report",
		Metadata: map[string]any{"report_key": report.Key, "execution_scope": report.ExecutionScope.Mode, "result_row_count": summary.RowCount, "execution_mode": summary.ExecutionMode},
	})
}
