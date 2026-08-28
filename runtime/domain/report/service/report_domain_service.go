package service

import (
	"context"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

type ReportDependencies struct {
	Reports         func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema
	Access          reportcontract.ReportRecordAccess
	Records         reportcontract.ReportRecordReader
	DatasetRows     reportcontract.ReportDatasetRowReader
	ObjectSQL       reportcontract.ReportObjectSQLExecutor
	Snapshots       reportcontract.ReportSnapshotStore
	SnapshotSources reportcontract.ReportSnapshotSourceVersionReader
	Clock           func() time.Time
}

// ReportDomainService evaluates report definitions against runtime records.
type ReportDomainService struct{ dependencies ReportDependencies }

func NewReportDomainService(dependencies ReportDependencies) *ReportDomainService {
	if dependencies.Clock == nil {
		dependencies.Clock = time.Now
	}
	return &ReportDomainService{dependencies: dependencies}
}

func (s *ReportDomainService) Summary(ctx context.Context, reportKey string, principal principalmodel.Principal) (reportmodel.ReportSummary, error) {
	return s.SummaryMode(ctx, reportKey, "realtime", principal)
}

func (s *ReportDomainService) SummaryMode(ctx context.Context, reportKey, mode string, principal principalmodel.Principal) (reportmodel.ReportSummary, error) {
	report, ok := s.reportForPrincipal(ctx, reportKey, principal)
	if !ok {
		return reportmodel.ReportSummary{}, reportAppError(apperror.KindNotFound, "backend.report.not_found", nil)
	}
	if report.ObjectSQLV1 != nil {
		if strings.TrimSpace(mode) != "" && strings.TrimSpace(mode) != "realtime" {
			return reportmodel.ReportSummary{}, reportAppError(apperror.KindBadRequest, "backend.report.object_sql_execution_mode_invalid", nil)
		}
		return s.executeReportObjectSQL(ctx, report, nil, principal)
	}
	plan, err := reportcontract.BuildReportDatasetPlan(report)
	if err != nil {
		// BuildReportDatasetPlan owns a closed ReportDatasetPlanError contract.
		planErr := err.(*reportmodel.ReportDatasetPlanError)
		return reportmodel.ReportSummary{}, &apperror.AppError{Kind: apperror.KindBadRequest, Code: planErr.Code, Params: planErr.Params, Err: planErr}
	}
	switch strings.TrimSpace(mode) {
	case "", "realtime":
		summary, executeErr := s.executeReportDataset(ctx, report, plan, principal)
		summary.ExecutionMode = "realtime"
		return summary, executeErr
	case "snapshot":
		return s.readReportSnapshot(ctx, report, principal)
	default:
		return reportmodel.ReportSummary{}, reportAppError(apperror.KindBadRequest, "backend.report.execution_mode_invalid", nil)
	}
}

func (s *ReportDomainService) QueryObjectSQL(ctx context.Context, reportKey string, parameters map[string]any, principal principalmodel.Principal) (reportmodel.ReportSummary, error) {
	report, ok := s.reportForPrincipal(ctx, reportKey, principal)
	if !ok {
		return reportmodel.ReportSummary{}, reportAppError(apperror.KindNotFound, "backend.report.not_found", nil)
	}
	if report.ObjectSQLV1 == nil {
		return reportmodel.ReportSummary{}, reportAppError(apperror.KindBadRequest, "backend.report.object_sql_not_enabled", nil)
	}
	return s.executeReportObjectSQL(ctx, report, parameters, principal)
}

// QueryObjectSQLPage executes the authored Object SQL with Runtime-owned
// LIMIT/OFFSET controls in the persistence query itself. It is intentionally
// separate from QueryObjectSQL so exports and legacy internal callers retain
// their existing complete-result contract.
func (s *ReportDomainService) QueryObjectSQLPage(ctx context.Context, reportKey string, parameters map[string]any, pageOffset, pageSize int, principal principalmodel.Principal) (reportmodel.ReportSummary, error) {
	report, ok := s.reportForPrincipal(ctx, reportKey, principal)
	if !ok {
		return reportmodel.ReportSummary{}, reportAppError(apperror.KindNotFound, "backend.report.not_found", nil)
	}
	if report.ObjectSQLV1 == nil {
		return reportmodel.ReportSummary{}, reportAppError(apperror.KindBadRequest, "backend.report.object_sql_not_enabled", nil)
	}
	if pageOffset < 0 || pageSize < 1 || pageSize > reportmodel.ReportPageMaximumSize {
		return reportmodel.ReportSummary{}, reportAppError(apperror.KindBadRequest, "backend.report.page_size_invalid", nil)
	}
	return s.executeReportObjectSQLPage(ctx, report, parameters, pageOffset, pageSize, principal)
}

func (s *ReportDomainService) ReportForExport(ctx context.Context, reportKey, objectKey string, principal principalmodel.Principal) (reportmodel.ReportSchema, error) {
	report, ok := s.reportForPrincipal(ctx, reportKey, principal)
	if !ok {
		return reportmodel.ReportSchema{}, reportAppError(apperror.KindNotFound, "backend.report.not_found", nil)
	}
	objectKey = strings.TrimSpace(objectKey)
	if !reportIncludesObject(report, objectKey) {
		return reportmodel.ReportSchema{}, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.report.object_not_in_report", Params: map[string]string{"report": report.Key, "object": objectKey}}
	}
	return report, nil
}

func (s *ReportDomainService) ReportForSummary(ctx context.Context, reportKey string, principal principalmodel.Principal) (reportmodel.ReportSchema, error) {
	report, ok := s.reportForPrincipal(ctx, reportKey, principal)
	if !ok {
		return reportmodel.ReportSchema{}, reportAppError(apperror.KindNotFound, "backend.report.not_found", nil)
	}
	return report, nil
}

// ExecuteExportReport executes an already-authorized, server-derived report
// definition. Callers must derive it from ReportForExport and may only apply
// validated narrowing changes such as declared filters or timezone.
func (s *ReportDomainService) ExecuteExportReport(ctx context.Context, report reportmodel.ReportSchema, parameters map[string]any, principal principalmodel.Principal) (reportmodel.ReportSummary, error) {
	if report.ObjectSQLV1 != nil {
		return s.executeReportObjectSQL(ctx, report, parameters, principal)
	}
	plan, err := reportcontract.BuildReportDatasetPlan(report)
	if err != nil {
		planErr := err.(*reportmodel.ReportDatasetPlanError)
		return reportmodel.ReportSummary{}, &apperror.AppError{Kind: apperror.KindBadRequest, Code: planErr.Code, Params: planErr.Params, Err: planErr}
	}
	summary, err := s.executeReportDataset(ctx, report, plan, principal)
	summary.ExecutionMode = "realtime"
	return summary, err
}

// ExecuteExportReportPage is the bounded worker path for object_sql_v1. The
// caller supplies only a server-owned offset and bounded size; the same parsed,
// authorized Report definition remains the query authority.
func (s *ReportDomainService) ExecuteExportReportPage(ctx context.Context, report reportmodel.ReportSchema, parameters map[string]any, pageOffset, pageSize int, principal principalmodel.Principal) (reportmodel.ReportSummary, error) {
	if pageOffset < 0 || pageSize < 1 || pageSize > reportmodel.ReportPageMaximumSize {
		return reportmodel.ReportSummary{}, reportAppError(apperror.KindBadRequest, "backend.report.page_size_invalid", nil)
	}
	if report.ObjectSQLV1 != nil {
		return s.executeReportObjectSQLPage(ctx, report, parameters, pageOffset, pageSize, principal)
	}
	plan, err := reportcontract.BuildReportDatasetPlan(report)
	if err != nil {
		planErr := err.(*reportmodel.ReportDatasetPlanError)
		return reportmodel.ReportSummary{}, &apperror.AppError{Kind: apperror.KindBadRequest, Code: planErr.Code, Params: planErr.Params, Err: planErr}
	}
	return s.executeReportDatasetPage(ctx, report, plan, pageOffset, pageSize, principal)
}

// AuthorizeObjectSQLExportFields reuses the canonical Object SQL compiler and
// the interactive read-field policy, then applies the stricter export-field
// policy. No author SQL or field list is accepted from the export request.
func (s *ReportDomainService) AuthorizeObjectSQLExportFields(ctx context.Context, report reportmodel.ReportSchema, principal principalmodel.Principal) error {
	if s == nil || s.dependencies.Access == nil || report.ObjectSQLV1 == nil {
		return reportAppError(apperror.KindInternal, "backend.report.object_sql_execution_unavailable", nil)
	}
	objectsByKey := map[string]definitionmodel.ObjectSchema{}
	for _, rawObjectKey := range report.ObjectSQLV1.SourceObjects {
		objectKey := strings.TrimSpace(rawObjectKey)
		object, err := s.dependencies.Access.ReportObjectForAction(ctx, principal, objectKey, "read")
		if err != nil {
			return err
		}
		objectsByKey[objectKey] = object
	}
	plan, err := reportcontract.CompileReportObjectSQL(*report.ObjectSQLV1, objectsByKey)
	if err != nil {
		return reportObjectSQLAppError(err)
	}
	authorizer, ok := s.dependencies.Access.(reportcontract.ReportObjectSQLFieldAuthorizer)
	if !ok {
		return reportAppError(apperror.KindInternal, "backend.report.object_sql_field_authorization_unavailable", nil)
	}
	for _, source := range plan.Sources {
		object := objectsByKey[source.ObjectKey]
		for _, fieldKey := range source.Fields {
			if err := authorizer.AuthorizeReportObjectSQLField(ctx, principal, object, fieldKey); err != nil {
				return err
			}
			masked, err := s.AuthorizeExportField(ctx, principal, source.ObjectKey, fieldKey)
			if err != nil {
				return err
			}
			if masked {
				return reportAppError(apperror.KindForbidden, "backend.report.export_sensitive_measure_denied", nil)
			}
		}
	}
	return nil
}

func (s *ReportDomainService) AuthorizeExportField(ctx context.Context, principal principalmodel.Principal, objectKey, fieldKey string) (bool, error) {
	if s == nil || s.dependencies.Access == nil {
		return false, reportAppError(apperror.KindInternal, "backend.report.execution_unavailable", nil)
	}
	if authorizer, ok := s.dependencies.Access.(reportcontract.ReportExportFieldAuthorizer); ok {
		return authorizer.AuthorizeReportExportField(ctx, principal, objectKey, fieldKey)
	}
	return false, reportAppError(apperror.KindInternal, "backend.report.export_authorizer_unavailable", nil)
}

func (s *ReportDomainService) reportForPrincipal(ctx context.Context, reportKey string, principal principalmodel.Principal) (reportmodel.ReportSchema, bool) {
	if s == nil || s.dependencies.Reports == nil {
		return reportmodel.ReportSchema{}, false
	}
	reportKey = strings.TrimSpace(reportKey)
	for _, report := range s.dependencies.Reports(ctx, principal) {
		if report.Key == reportKey {
			return report, true
		}
	}
	return reportmodel.ReportSchema{}, false
}

func reportIncludesObject(report reportmodel.ReportSchema, objectKey string) bool {
	for _, candidate := range reportUniqueSourceObjects(report) {
		if candidate == strings.TrimSpace(objectKey) {
			return true
		}
	}
	return false
}

func reportUniqueSourceObjects(report reportmodel.ReportSchema) []string {
	out := []string{}
	seen := map[string]bool{}
	objectKeys := reportmodel.ReportDatasetObjectKeys(report.Dataset)
	if report.ObjectSQLV1 != nil {
		objectKeys = reportmodel.ReportObjectSQLObjectKeys(report.ObjectSQLV1)
	}
	for _, objectKey := range objectKeys {
		objectKey = strings.TrimSpace(objectKey)
		if objectKey == "" || seen[objectKey] {
			continue
		}
		seen[objectKey] = true
		out = append(out, objectKey)
	}
	return out
}

func reportAppError(kind apperror.ErrorKind, code string, err error) error {
	return &apperror.AppError{Kind: kind, Code: code, Err: err}
}
