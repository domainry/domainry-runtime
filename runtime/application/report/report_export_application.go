package report

import (
	"context"
	"fmt"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	reportexport "github.com/domainry/domainry-runtime/runtime/application/report/export"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

var reportExportRows = reportexport.Rows

func reportApplicationError(err error) error {
	return &apperror.AppError{Kind: apperror.KindInternal, Code: "backend.internal_error", Err: err}
}

func reportWorkspaceError(err error) error {
	return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required", Err: err}
}

func (s *ReportApplicationService) reportExportWatermark(reportKey, requester, expiresAt string) string {
	return fmt.Sprintf("%s governed export | report=%s | requester=%s | expires_at=%s", s.productBrandName, reportKey, requester, expiresAt)
}

func (s *ReportApplicationService) DownloadExport(ctx context.Context, jobID string, principal principalmodel.Principal) ([]byte, string, error) {
	if _, err := principalmodel.QueryScopeForPrincipal(principal); err != nil {
		return nil, "", reportWorkspaceError(err)
	}
	if s == nil || s.dataExchangeProvider == nil {
		return nil, "", reportApplicationError(nil)
	}
	return s.dataExchangeProvider.Download(ctx, strings.TrimSpace(jobID), principal)
}

func reportExportStatusAllowed(status string, allowed []string) bool {
	status = strings.TrimSpace(status)
	for _, value := range allowed {
		if status == strings.TrimSpace(value) {
			return true
		}
	}
	return false
}

func (s *ReportApplicationService) executeScopedExport(ctx context.Context, original, scoped reportmodel.ReportSchema, scope reportmodel.ReportExportScopeRequest, principal principalmodel.Principal) (reportmodel.ReportSummary, error) {
	if scope.Freshness.Mode == "snapshot" {
		summary, err := s.domain.SummaryMode(ctx, original.Key, "snapshot", principal)
		if err != nil {
			return reportmodel.ReportSummary{}, err
		}
		if !reportExportSnapshotFreshnessSatisfied(summary, scope.Freshness) {
			return reportmodel.ReportSummary{}, &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_freshness_not_satisfied"}
		}
		return summary, nil
	}
	return s.domain.ExecuteExportReport(ctx, scoped, scope.Parameters, principal)
}

func reportExportSnapshotFreshnessSatisfied(summary reportmodel.ReportSummary, freshness reportmodel.ReportExportFreshness) bool {
	return summary.Snapshot != nil &&
		(freshness.SnapshotID == "" || freshness.SnapshotID == summary.Snapshot.SnapshotID) &&
		(freshness.MaximumLagSeconds <= 0 || summary.Snapshot.LagSeconds <= freshness.MaximumLagSeconds)
}

func (s *ReportApplicationService) exportControl(ctx context.Context, reportKey, objectKey string, principal principalmodel.Principal) (reportmodel.ReportExportControlSchema, bool) {
	if s == nil || s.exportControls == nil {
		return reportmodel.ReportExportControlSchema{}, false
	}
	for _, control := range s.exportControls(ctx, principal) {
		if strings.TrimSpace(control.ReportKey) != strings.TrimSpace(reportKey) {
			continue
		}
		for _, source := range control.SourceObjects {
			if strings.TrimSpace(source) == strings.TrimSpace(objectKey) {
				return control, true
			}
		}
	}
	return reportmodel.ReportExportControlSchema{}, false
}
