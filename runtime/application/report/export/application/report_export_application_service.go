package application

import (
	"context"
	"fmt"
	"strings"
	"time"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	"github.com/domainry/domainry-foundation/apperror"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	auditcontract "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	reportexport "github.com/domainry/domainry-runtime/runtime/application/report/export"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportservice "github.com/domainry/domainry-runtime/runtime/domain/report/query"
	"github.com/domainry/domainry-runtime/runtime/platform/productbrand"
)

var reportExportRows = reportexport.Rows

type ReportExportRecordStore interface {
	GetReportRecord(context.Context, string, string, principalmodel.Principal) (recordmodel.Record, error)
	ListReportRecordsForPrincipal(context.Context, string, recordmodel.RecordListQuery, principalmodel.Principal) (recordmodel.RecordPageResult, error)
	CreateReportRecord(context.Context, string, map[string]any, string, principalmodel.Principal) (recordmodel.Record, error)
	UpdateReportRecord(context.Context, string, string, map[string]any, string, principalmodel.Principal) (recordmodel.Record, error)
	TransitionReportExportAuditStatus(context.Context, string, string, string, string, string, string) error
}

func reportApplicationError(err error) error {
	return &apperror.AppError{Kind: apperror.KindInternal, Code: "backend.internal_error", Err: err}
}

func reportWorkspaceError(err error) error {
	return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required", Err: err}
}

type ReportExportApplicationDependencies struct {
	ProductBrandName      string
	Domain                *reportservice.ReportDomainService
	Records               ReportExportRecordStore
	Audit                 auditcontract.AuditAppender
	Controls              func(context.Context, principalmodel.Principal) []reportmodel.ReportExportControlSchema
	Clock                 func() time.Time
	DataExchange          dataexchange.Binding
	DataExchangeProviders *recordapplication.DataExchangeProviders
}

// ReportExportApplicationService owns governed export preparation, audit
// sequencing, Data Exchange job lifecycle, and download authorization.
type ReportExportApplicationService struct {
	productBrandName     string
	domain               *reportservice.ReportDomainService
	exportRecords        ReportExportRecordStore
	exportControls       func(context.Context, principalmodel.Principal) []reportmodel.ReportExportControlSchema
	clock                func() time.Time
	dataExchange         dataexchange.Binding
	dataExchangeProvider *reportexport.DataExchangeProvider
}

func NewReportExportApplicationService(dependencies ReportExportApplicationDependencies) *ReportExportApplicationService {
	clock := dependencies.Clock
	if clock == nil {
		clock = time.Now
	}
	service := &ReportExportApplicationService{
		productBrandName: productbrand.ResolveName(dependencies.ProductBrandName), domain: dependencies.Domain,
		exportRecords: dependencies.Records, exportControls: dependencies.Controls, clock: clock, dataExchange: dependencies.DataExchange,
	}
	if dependencies.DataExchangeProviders != nil {
		service.dataExchangeProvider = reportexport.NewDataExchangeProvider(reportexport.DataExchangeDependencies{
			Binding: dependencies.DataExchange, Domain: dependencies.Domain, Records: dependencies.Records, Audit: dependencies.Audit,
			ResolvePrincipal: dependencies.DataExchangeProviders.ResolvePrincipal, ExportControl: service.exportControl,
			Watermark: service.reportExportWatermark, Clock: clock,
		})
		dependencies.DataExchangeProviders.RegisterExportProvider(reportexport.DataExchangeProviderKey, service.dataExchangeProvider)
	}
	return service
}

func (s *ReportExportApplicationService) reportExportWatermark(reportKey, requester, expiresAt string) string {
	return fmt.Sprintf("%s governed export | report=%s | requester=%s | expires_at=%s", s.productBrandName, reportKey, requester, expiresAt)
}

func (s *ReportExportApplicationService) DownloadExport(ctx context.Context, jobID string, principal principalmodel.Principal) ([]byte, string, error) {
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

func (s *ReportExportApplicationService) executeScopedExport(ctx context.Context, original, scoped reportmodel.ReportSchema, scope reportmodel.ReportExportScopeRequest, principal principalmodel.Principal) (reportmodel.ReportSummary, error) {
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

func (s *ReportExportApplicationService) exportControl(ctx context.Context, reportKey, objectKey string, principal principalmodel.Principal) (reportmodel.ReportExportControlSchema, bool) {
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

// probeReportExport freezes a bounded result/version preflight without
// materializing an unbounded export.
func (s *ReportExportApplicationService) probeReportExport(ctx context.Context, report, scopedReport reportmodel.ReportSchema, scope reportmodel.ReportExportScopeRequest, maxRows int, principal principalmodel.Principal) ([]reportmodel.ReportResultRow, reportmodel.ReportSnapshotSourceVersion, int, error) {
	const consistencyRetries = 3
	var lastErr error
	for attempt := 0; attempt < consistencyRetries; attempt++ {
		before, err := s.domain.ExportSourceVersion(ctx, scopedReport, principal)
		if err != nil {
			return nil, reportmodel.ReportSnapshotSourceVersion{}, 0, err
		}
		rows, exact, err := s.probeReportExportRows(ctx, report, scopedReport, scope, maxRows, principal)
		if err != nil {
			return nil, reportmodel.ReportSnapshotSourceVersion{}, 0, err
		}
		after, err := s.domain.ExportSourceVersion(ctx, scopedReport, principal)
		if err != nil {
			return nil, reportmodel.ReportSnapshotSourceVersion{}, 0, err
		}
		if reportservice.ReportSourceVersionsEqual(before, after) {
			return rows, after, exact, nil
		}
		lastErr = fmt.Errorf("report source changed during export routing")
	}
	return nil, reportmodel.ReportSnapshotSourceVersion{}, 0, &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_source_changed", Err: lastErr}
}

func (s *ReportExportApplicationService) probeReportExportRows(ctx context.Context, report, scopedReport reportmodel.ReportSchema, scope reportmodel.ReportExportScopeRequest, maxRows int, principal principalmodel.Principal) ([]reportmodel.ReportResultRow, int, error) {
	rows := make([]reportmodel.ReportResultRow, 0, reportExportProbeRowLimit+1)
	pageCursor := ""
	for len(rows) <= reportExportProbeRowLimit {
		pageSize := reportmodel.ReportPageMaximumSize
		if remaining := reportExportProbeRowLimit + 1 - len(rows); remaining < pageSize {
			pageSize = remaining
		}
		summary, err := s.domain.ExecuteExportReportPage(ctx, scopedReport, scope.Parameters, pageCursor, len(rows), pageSize, principal)
		if err != nil {
			if apperror.CodeOf(err) != "backend.report.bounded_page_unavailable" || maxRows <= 0 || maxRows > reportExportProbeRowLimit {
				return nil, 0, err
			}
			full, executeErr := s.executeScopedExport(ctx, report, scopedReport, scope, principal)
			if executeErr != nil {
				return nil, 0, executeErr
			}
			fullRows, rowsErr := reportExportRows(full, scope.AnalysisKey)
			if rowsErr != nil {
				return nil, 0, rowsErr
			}
			if len(fullRows) > maxRows {
				return nil, 0, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.report.export_too_many_rows", Params: map[string]string{"limit": fmt.Sprint(maxRows)}}
			}
			return fullRows, len(fullRows), nil
		}
		pageRows, err := reportExportRows(summary, scope.AnalysisKey)
		if err != nil {
			return nil, 0, err
		}
		if len(pageRows) == 0 && summary.Truncated {
			return nil, 0, &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_result_changed"}
		}
		rows = append(rows, pageRows...)
		pageCursor = summary.ExecutionCursor
		if maxRows > 0 && len(rows) > maxRows {
			return nil, 0, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.report.export_too_many_rows", Params: map[string]string{"limit": fmt.Sprint(maxRows)}}
		}
		if !summary.Truncated {
			return rows, len(rows), nil
		}
	}
	return rows, -1, nil
}
