package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	"github.com/domainry/domainry-foundation/apperror"
	reportsdk "github.com/domainry/domainry-report-sdk"
	reportcontract "github.com/domainry/domainry-report-sdk/contract"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	auditcontract "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	reportexport "github.com/domainry/domainry-runtime/runtime/application/report/export"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	runtimereportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	reportadapter "github.com/domainry/domainry-runtime/runtime/modulehost/report"
	"github.com/domainry/domainry-runtime/runtime/platform/productbrand"
)

type ReportExportRecordStore interface {
	GetReportRecord(context.Context, string, string, principalmodel.Principal) (recordmodel.Record, error)
	CreateReportRecord(context.Context, string, map[string]any, string, principalmodel.Principal) (recordmodel.Record, error)
	TransitionReportExportAuditStatus(context.Context, string, string, string, string, string, string) error
	TransitionReportExportAudit(context.Context, string, string, string, string, string, map[string]any) (bool, error)
}

func reportApplicationError(err error) error {
	return &apperror.AppError{Kind: apperror.KindInternal, Code: "backend.internal_error", Err: err}
}

func reportWorkspaceError(err error) error {
	return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required", Err: err}
}

type ReportExportApplicationDependencies struct {
	ProductBrandName      string
	Records               ReportExportRecordStore
	Audit                 auditcontract.AuditAppender
	Clock                 func() time.Time
	DataExchange          dataexchange.Binding
	DataExchangeProviders *recordapplication.DataExchangeProviders
	PrepareReceipts       runtimereportcontract.ReportExportPrepareReceiptStore
}

// ReportExportApplicationService owns governed export preparation, audit
// sequencing, Data Exchange job lifecycle, and download authorization.
type ReportExportApplicationService struct {
	productBrandName     string
	exportRecords        ReportExportRecordStore
	reportExports        reportsdk.Exports
	clock                func() time.Time
	dataExchange         dataexchange.Binding
	dataExchangeProvider *reportexport.DataExchangeProvider
	prepareReceipts      runtimereportcontract.ReportExportPrepareReceiptStore
}

func NewReportExportApplicationService(dependencies ReportExportApplicationDependencies) *ReportExportApplicationService {
	clock := dependencies.Clock
	if clock == nil {
		clock = time.Now
	}
	service := &ReportExportApplicationService{
		productBrandName: productbrand.ResolveName(dependencies.ProductBrandName),
		exportRecords:    dependencies.Records, clock: clock, dataExchange: dependencies.DataExchange, prepareReceipts: dependencies.PrepareReceipts,
	}
	if dependencies.DataExchangeProviders != nil {
		service.dataExchangeProvider = reportexport.NewDataExchangeProvider(reportexport.DataExchangeDependencies{
			Binding: dependencies.DataExchange, Records: dependencies.Records, Receipts: dependencies.PrepareReceipts, Audit: dependencies.Audit,
			ResolvePrincipal: dependencies.DataExchangeProviders.ResolvePrincipal, ResolveExecution: service.resolveReportExportExecution,
			ReadPage: service.readReportExportPage, SourceVersion: service.readReportExportSourceVersion,
			Watermark: service.reportExportWatermark, Clock: clock,
		})
		if err := dependencies.DataExchangeProviders.RegisterExportProvider(reportexport.DataExchangeProviderKey, service.dataExchangeProvider); err != nil {
			panic(fmt.Errorf("register Report Data Exchange provider: %w", err))
		}
	}
	return service
}

func (s *ReportExportApplicationService) BindReportExports(exports reportsdk.Exports) error {
	if s == nil || exports == nil {
		return fmt.Errorf("Report export application capability is required")
	}
	s.reportExports = exports
	return nil
}

func (s *ReportExportApplicationService) reportExportAuthority(principal principalmodel.Principal) (reportmodel.ReportAuthority, error) {
	return reportadapter.ReportAuthorityFromRuntimePrincipal(principal)
}

func (s *ReportExportApplicationService) resolveReportExportExecution(ctx context.Context, request reportmodel.ReportExportExecutionRequest, principal principalmodel.Principal) (reportmodel.ReportExportExecution, error) {
	if s == nil || s.reportExports == nil {
		return reportmodel.ReportExportExecution{}, reportApplicationError(nil)
	}
	authority, err := s.reportExportAuthority(principal)
	if err != nil {
		return reportmodel.ReportExportExecution{}, err
	}
	resolved, err := s.reportExports.ResolveExecution(ctx, request, authority)
	if err == nil {
		return resolved, nil
	}
	return reportmodel.ReportExportExecution{}, reportExportOwnerError(err)
}

func (s *ReportExportApplicationService) readReportExportPage(ctx context.Context, request reportmodel.ReportExportExecutionRequest, principal principalmodel.Principal) (reportmodel.ReportSummary, error) {
	if s == nil || s.reportExports == nil {
		return reportmodel.ReportSummary{}, reportApplicationError(nil)
	}
	authority, err := s.reportExportAuthority(principal)
	if err != nil {
		return reportmodel.ReportSummary{}, err
	}
	summary, err := s.reportExports.ReadPage(ctx, request, authority)
	if err != nil {
		return reportmodel.ReportSummary{}, reportExportOwnerError(err)
	}
	return summary, nil
}

func (s *ReportExportApplicationService) readReportExportSourceVersion(ctx context.Context, request reportmodel.ReportExportExecutionRequest, principal principalmodel.Principal) (reportmodel.ReportSnapshotSourceVersion, error) {
	if s == nil || s.reportExports == nil {
		return reportmodel.ReportSnapshotSourceVersion{}, reportApplicationError(nil)
	}
	authority, err := s.reportExportAuthority(principal)
	if err != nil {
		return reportmodel.ReportSnapshotSourceVersion{}, err
	}
	version, err := s.reportExports.SourceVersion(ctx, request, authority)
	if err != nil {
		return reportmodel.ReportSnapshotSourceVersion{}, reportExportOwnerError(err)
	}
	return version, nil
}

func reportExportOwnerError(err error) error {
	var stable *reportsdk.Error
	if !errors.As(err, &stable) {
		return err
	}
	kind := apperror.KindInternal
	switch stable.StatusCode {
	case 400:
		kind = apperror.KindBadRequest
	case 401, 403:
		kind = apperror.KindForbidden
	case 404:
		kind = apperror.KindNotFound
	case 409:
		kind = apperror.KindConflict
	case 429:
		kind = apperror.KindRateLimited
	case 503:
		kind = apperror.KindUnavailable
	}
	return &apperror.AppError{Kind: kind, Code: stable.Code, Params: stable.Params, Err: err}
}

func (s *ReportExportApplicationService) reportExportWatermark(reportKey, requester, expiresAt string) string {
	return fmt.Sprintf("%s governed export | report=%s | requester=%s | expires_at=%s", s.productBrandName, reportKey, requester, expiresAt)
}

// probeReportExport freezes a bounded result/version preflight without
// materializing an unbounded export.
func (s *ReportExportApplicationService) probeReportExport(ctx context.Context, request reportmodel.ReportExportExecutionRequest, maxRows int, principal principalmodel.Principal) ([]reportmodel.ReportResultRow, reportmodel.ReportSnapshotSourceVersion, int, error) {
	const consistencyRetries = 3
	var lastErr error
	for attempt := 0; attempt < consistencyRetries; attempt++ {
		before, err := s.readReportExportSourceVersion(ctx, request, principal)
		if err != nil {
			return nil, reportmodel.ReportSnapshotSourceVersion{}, 0, err
		}
		rows, exact, err := s.probeReportExportRows(ctx, request, maxRows, principal)
		if err != nil {
			return nil, reportmodel.ReportSnapshotSourceVersion{}, 0, err
		}
		after, err := s.readReportExportSourceVersion(ctx, request, principal)
		if err != nil {
			return nil, reportmodel.ReportSnapshotSourceVersion{}, 0, err
		}
		beforeHash, beforeHashErr := reportcontract.CanonicalJSONSHA256(before)
		afterHash, afterHashErr := reportcontract.CanonicalJSONSHA256(after)
		if beforeHashErr == nil && afterHashErr == nil && beforeHash == afterHash {
			return rows, after, exact, nil
		}
		lastErr = fmt.Errorf("report source changed during export routing")
	}
	return nil, reportmodel.ReportSnapshotSourceVersion{}, 0, &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_source_changed", Err: lastErr}
}

func (s *ReportExportApplicationService) probeReportExportRows(ctx context.Context, request reportmodel.ReportExportExecutionRequest, maxRows int, principal principalmodel.Principal) ([]reportmodel.ReportResultRow, int, error) {
	rows := make([]reportmodel.ReportResultRow, 0, reportExportProbeRowLimit+1)
	request.Page = reportmodel.ReportPageRequest{}
	for len(rows) <= reportExportProbeRowLimit {
		pageSize := reportmodel.ReportPageMaximumSize
		if remaining := reportExportProbeRowLimit + 1 - len(rows); remaining < pageSize {
			pageSize = remaining
		}
		request.Page.PageSize = pageSize
		summary, err := s.readReportExportPage(ctx, request, principal)
		if err != nil {
			return nil, 0, err
		}
		pageRows := summary.Rows
		if len(pageRows) == 0 && summary.Truncated {
			return nil, 0, &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_result_changed"}
		}
		rows = append(rows, pageRows...)
		request.Page.Cursor = summary.NextCursor
		if maxRows > 0 && len(rows) > maxRows {
			return nil, 0, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.report.export_too_many_rows", Params: map[string]string{"limit": fmt.Sprint(maxRows)}}
		}
		if !summary.Truncated {
			return rows, len(rows), nil
		}
	}
	return rows, -1, nil
}
