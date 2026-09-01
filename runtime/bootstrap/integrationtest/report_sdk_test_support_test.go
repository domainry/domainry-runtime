package integrationtest

import (
	"context"
	"fmt"

	reportmodel "github.com/domainry/domainry-report-sdk/model"
	runtimecomposition "github.com/domainry/domainry-runtime/runtime/bootstrap/composition"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	reportadapter "github.com/domainry/domainry-runtime/runtime/modulehost/report"
)

func integrationReportSummary(ctx context.Context, services *runtimecomposition.RuntimeServices, reportKey, mode string, principal principalmodel.Principal) (reportmodel.ReportSummary, error) {
	application := services.Applications().Reports
	if application == nil || application.Queries() == nil {
		return reportmodel.ReportSummary{}, fmt.Errorf("Report application binding is unavailable")
	}
	authority, err := reportadapter.ReportAuthorityFromRuntimePrincipal(principal)
	if err != nil {
		return reportmodel.ReportSummary{}, err
	}
	return application.Queries().Summary(ctx, reportmodel.ReportSummaryRequest{ReportKey: reportKey, Mode: mode}, authority)
}

func integrationReportSnapshot(ctx context.Context, services *runtimecomposition.RuntimeServices, reportKey, idempotencyKey string, principal principalmodel.Principal) (reportmodel.ReportSnapshot, error) {
	application := services.Applications().Reports
	if application == nil || application.SnapshotCommands() == nil {
		return reportmodel.ReportSnapshot{}, fmt.Errorf("Report snapshot application binding is unavailable")
	}
	authority, err := reportadapter.ReportAuthorityFromRuntimePrincipal(principal)
	if err != nil {
		return reportmodel.ReportSnapshot{}, err
	}
	return application.SnapshotCommands().Refresh(ctx, reportmodel.ReportSnapshotRefreshRequest{ReportKey: reportKey, IdempotencyKey: idempotencyKey}, authority)
}
