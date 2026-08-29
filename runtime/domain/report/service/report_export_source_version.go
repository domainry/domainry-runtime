package service

import (
	"context"

	"github.com/domainry/domainry-foundation/apperror"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

// ExportSourceVersion captures the authorized database sources behind one
// already-scoped report. Async keyset pagination is permitted only while this
// version remains unchanged, otherwise a retry could silently duplicate or
// omit rows.
func (s *ReportDomainService) ExportSourceVersion(ctx context.Context, report reportmodel.ReportSchema, principal principalmodel.Principal) (reportmodel.ReportSnapshotSourceVersion, error) {
	if s == nil || s.dependencies.SnapshotSources == nil {
		return reportmodel.ReportSnapshotSourceVersion{}, reportAppError(apperror.KindInternal, "backend.report.export_source_version_unavailable", nil)
	}
	var request reportcontract.ReportSnapshotSourceVersionRequest
	request.WorkspaceID = principal.WorkspaceID
	if report.ObjectSQLV1 != nil {
		_, objects, queries, err := s.reportObjectSQLExecutionInputs(ctx, report, principal)
		if err != nil {
			return reportmodel.ReportSnapshotSourceVersion{}, err
		}
		request.Objects, request.Queries = objects, queries
	} else {
		plan, err := reportcontract.BuildReportDatasetPlan(report)
		if err != nil {
			planErr := err.(*reportmodel.ReportDatasetPlanError)
			return reportmodel.ReportSnapshotSourceVersion{}, &apperror.AppError{Kind: apperror.KindBadRequest, Code: planErr.Code, Params: planErr.Params, Err: planErr}
		}
		objects, queries, err := s.reportSnapshotSources(ctx, report, plan, principal)
		if err != nil {
			return reportmodel.ReportSnapshotSourceVersion{}, err
		}
		request.Objects, request.Queries = objects, queries
	}
	version, err := s.dependencies.SnapshotSources.ReadReportSnapshotSourceVersion(ctx, request)
	if err != nil {
		return reportmodel.ReportSnapshotSourceVersion{}, reportAppError(apperror.KindInternal, "backend.report.export_source_version_failed", err)
	}
	return version, nil
}

// ReportSourceVersionsEqual is intentionally shared with the application
// worker so every page is fenced by the same canonical comparison used by
// materialized report refreshes.
func ReportSourceVersionsEqual(left, right reportmodel.ReportSnapshotSourceVersion) bool {
	return reportSnapshotVersionsEqual(left, right)
}
