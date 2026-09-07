package contract

import (
	"context"

	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

// ReportExportPrepareReceiptStore owns only Runtime coordination state. Report
// definitions and business audit/download records remain source-owned.
type ReportExportPrepareReceiptStore interface {
	GetReportExportPrepareReceipt(context.Context, string, string) (reportmodel.ReportExportPrepareReceipt, bool, error)
	TryBeginReportExportPrepare(context.Context, reportmodel.ReportExportPrepareClaimRequest) (reportmodel.ReportExportPrepareClaimResult, error)
	SaveReportExportPreparePayload(context.Context, reportmodel.ReportExportPreparePayload) (reportmodel.ReportExportPrepareReceipt, error)
	CompleteReportExportPrepare(context.Context, reportmodel.ReportExportPrepareCompletion) error
	FailReportExportPrepareRetryable(context.Context, reportmodel.ReportExportPrepareFailure) error
	FailReportExportPrepareTerminal(context.Context, reportmodel.ReportExportPrepareFailure) error
	ReleaseReportExportPrepare(context.Context, reportmodel.ReportExportPrepareFailure) error
	BindReportExportCompletion(context.Context, reportmodel.ReportExportCompletionBinding) (reportmodel.ReportExportCompletionBindingResult, error)
}
