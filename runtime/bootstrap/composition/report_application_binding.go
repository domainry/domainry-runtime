package composition

import (
	"context"
	"fmt"

	reportsdk "github.com/domainry/domainry-report-sdk"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	reportadapter "github.com/domainry/domainry-runtime/runtime/application/report/adapter"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// BindReportApplication installs the Report owner's application boundary only
// after its host ports have been assembled and bound. Runtime keeps no second
// query/snapshot application service; internal orchestration calls the SDK.
func (s *RuntimeServices) BindReportApplication(binding reportsdk.Binding) error {
	if s == nil || binding == nil {
		return fmt.Errorf("Report application binding is required")
	}
	application, ok := binding.(reportsdk.ApplicationBinding)
	if !ok || application.Queries() == nil || application.SnapshotCommands() == nil || application.Exports() == nil {
		return fmt.Errorf("Report Binding returned incomplete application capabilities")
	}
	exportHost, ok := s.reportModule.Exports.(reportModuleExportHost)
	if !ok || exportHost.service == nil {
		return fmt.Errorf("Report export host is unavailable")
	}
	if err := exportHost.service.BindReportExports(application.Exports()); err != nil {
		return err
	}
	s.applications.Reports = application
	if s.applications.Scheduler != nil {
		s.applications.Scheduler.UseReportSnapshotRuntime(reportSDKSnapshotRuntime{commands: application.SnapshotCommands()})
	}
	return nil
}

type reportSDKSnapshotRuntime struct{ commands reportsdk.SnapshotCommands }

func (r reportSDKSnapshotRuntime) RefreshSnapshot(ctx context.Context, reportKey, idempotencyKey string, principal principalmodel.Principal) (reportmodel.ReportSnapshot, error) {
	if r.commands == nil {
		return reportmodel.ReportSnapshot{}, fmt.Errorf("Report snapshot application capability is unavailable")
	}
	authority, err := reportadapter.ReportAuthorityFromRuntimePrincipal(principal)
	if err != nil {
		return reportmodel.ReportSnapshot{}, err
	}
	return r.commands.Refresh(ctx, reportmodel.ReportSnapshotRefreshRequest{ReportKey: reportKey, IdempotencyKey: idempotencyKey}, authority)
}
