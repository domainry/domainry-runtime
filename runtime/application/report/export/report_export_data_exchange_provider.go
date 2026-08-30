package export

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	"github.com/domainry/domainry-data-exchange-sdk/modulehost"
	"github.com/domainry/domainry-foundation/apperror"
	auditcontract "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	reportservice "github.com/domainry/domainry-runtime/runtime/domain/report/service"
)

const DataExchangeProviderKey = "reports"
const dataExchangeDownloadTTL = 15 * time.Minute

type ExportPayload struct {
	WorkspaceID              string                                  `json:"workspace_id"`
	RequesterUserID          string                                  `json:"requester_user_id"`
	ReportKey                string                                  `json:"report_key"`
	ObjectKey                string                                  `json:"object_key"`
	AuditID                  string                                  `json:"audit_id"`
	ArtifactIdempotencyKey   string                                  `json:"artifact_idempotency_key"`
	Scope                    reportmodel.ReportExportScopeRequest    `json:"scope"`
	ReportDefinitionSHA256   string                                  `json:"report_definition_sha256"`
	ReportSourceSHA256       string                                  `json:"report_source_sha256"`
	AuthorizationScopeSHA256 string                                  `json:"authorization_scope_sha256"`
	ControlDefinitionSHA256  string                                  `json:"control_definition_sha256"`
	SourceVersion            reportmodel.ReportSnapshotSourceVersion `json:"source_version"`
	SourceVersionSHA256      string                                  `json:"source_version_sha256"`
	ResultSHA256             string                                  `json:"result_sha256"`
	CSVContentSHA256         string                                  `json:"csv_content_sha256"`
	ExactTotal               int                                     `json:"exact_total"`
	MaxRows                  int                                     `json:"max_rows"`
	Format                   string                                  `json:"format"`
}

type DataExchangeRecordStore interface {
	GetReportRecord(context.Context, string, string, principalmodel.Principal) (recordmodel.Record, error)
	CreateReportRecord(context.Context, string, map[string]any, string, principalmodel.Principal) (recordmodel.Record, error)
	UpdateReportRecord(context.Context, string, string, map[string]any, string, principalmodel.Principal) (recordmodel.Record, error)
	TransitionReportExportAuditStatus(context.Context, string, string, string, string, string, string) error
}

type DataExchangeDependencies struct {
	Binding          dataexchange.Binding
	Domain           *reportservice.ReportDomainService
	Records          DataExchangeRecordStore
	Audit            auditcontract.AuditAppender
	ResolvePrincipal func(context.Context, dataexchange.Scope) principalmodel.Principal
	ExportControl    func(context.Context, string, string, principalmodel.Principal) (reportmodel.ReportExportControlSchema, bool)
	Watermark        func(string, string, string) string
	Clock            func() time.Time
}

type DataExchangeProvider struct{ dependencies DataExchangeDependencies }

type dataExchangeCursor struct {
	ExecutionCursor string `json:"execution_cursor"`
	Position        int    `json:"position"`
}

type preparedDataExchangeExport struct {
	control          reportmodel.ReportExportControlSchema
	normalizedScope  reportmodel.ReportExportScopeRequest
	scopedReport     reportmodel.ReportSchema
	maskedDimensions map[string]bool
}

func NewDataExchangeProvider(dependencies DataExchangeDependencies) *DataExchangeProvider {
	if dependencies.Clock == nil {
		dependencies.Clock = time.Now
	}
	return &DataExchangeProvider{dependencies: dependencies}
}

func Scope(principal principalmodel.Principal) dataexchange.Scope {
	return dataexchange.Scope{WorkspaceID: strings.TrimSpace(principal.WorkspaceID), ActorID: strings.TrimSpace(principal.UserID), RoleKey: strings.TrimSpace(principal.RoleKey), RequestID: strings.TrimSpace(principal.RequestID)}
}

func exchangePayload(options []byte, referenceID string) (ExportPayload, error) {
	var payload ExportPayload
	if err := json.Unmarshal(options, &payload); err != nil {
		return payload, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.report.export_job_payload_invalid", Err: err}
	}
	referenceID = strings.TrimSpace(referenceID)
	if referenceID == "" {
		return payload, &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_audit_binding_invalid"}
	}
	if strings.TrimSpace(payload.AuditID) != "" && strings.TrimSpace(payload.AuditID) != referenceID {
		return payload, &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_audit_binding_changed"}
	}
	payload.AuditID = referenceID
	return payload, nil
}

func (p *DataExchangeProvider) principal(ctx context.Context, scope dataexchange.Scope) (principalmodel.Principal, error) {
	if p == nil || p.dependencies.ResolvePrincipal == nil {
		return principalmodel.Principal{}, internalError(nil)
	}
	principal := p.dependencies.ResolvePrincipal(ctx, scope)
	principal.WorkspaceID, principal.UserID, principal.RequestID = strings.TrimSpace(scope.WorkspaceID), strings.TrimSpace(scope.ActorID), strings.TrimSpace(scope.RequestID)
	if _, err := principalmodel.QueryScopeForPrincipal(principal); err != nil {
		return principalmodel.Principal{}, workspaceError(err)
	}
	return principal, nil
}

func (p *DataExchangeProvider) prepare(ctx context.Context, payload ExportPayload, principal principalmodel.Principal, verifySourceVersion bool) (preparedDataExchangeExport, error) {
	if payload.WorkspaceID != strings.TrimSpace(principal.WorkspaceID) || payload.RequesterUserID != strings.TrimSpace(principal.UserID) {
		return preparedDataExchangeExport{}, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.report.export_requester_mismatch"}
	}
	authorizationHash, err := reportservice.ReportAccessScopeHash(principal)
	if err != nil || authorizationHash != payload.AuthorizationScopeSHA256 {
		return preparedDataExchangeExport{}, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.report.export_scope_changed", Err: err}
	}
	report, err := p.dependencies.Domain.ReportForExport(ctx, payload.ReportKey, payload.ObjectKey, principal)
	if err != nil {
		return preparedDataExchangeExport{}, err
	}
	reportHash, _ := CanonicalJSONSHA256(report)
	sourceHash := reportHash
	if report.ObjectSQLV1 != nil {
		sourceHash, _ = CanonicalJSONSHA256(report.ObjectSQLV1)
	}
	if reportHash != payload.ReportDefinitionSHA256 || sourceHash != payload.ReportSourceSHA256 {
		return preparedDataExchangeExport{}, &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_source_changed"}
	}
	control, ok := p.dependencies.ExportControl(ctx, payload.ReportKey, payload.ObjectKey, principal)
	if !ok {
		return preparedDataExchangeExport{}, &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_scope_changed"}
	}
	controlHash, _ := CanonicalJSONSHA256(control)
	if strings.TrimSpace(payload.ControlDefinitionSHA256) == "" || controlHash != payload.ControlDefinitionSHA256 {
		return preparedDataExchangeExport{}, &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_scope_changed"}
	}
	normalized, scoped, masked, err := NormalizeScope(report, payload.ObjectKey, control, payload.Scope, principal)
	if err != nil {
		return preparedDataExchangeExport{}, err
	}
	masked, err = ValidateFieldAccess(ctx, p.dependencies.Domain, scoped, control, principal)
	if err != nil {
		return preparedDataExchangeExport{}, err
	}
	if verifySourceVersion && strings.TrimSpace(payload.SourceVersionSHA256) != "" {
		version, versionErr := p.dependencies.Domain.ExportSourceVersion(ctx, scoped, principal)
		if versionErr != nil {
			return preparedDataExchangeExport{}, versionErr
		}
		versionHash, hashErr := CanonicalJSONSHA256(version)
		if hashErr != nil {
			return preparedDataExchangeExport{}, hashErr
		}
		if versionHash != payload.SourceVersionSHA256 || !reportservice.ReportSourceVersionsEqual(version, payload.SourceVersion) {
			return preparedDataExchangeExport{}, &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_source_changed"}
		}
	}
	return preparedDataExchangeExport{control: control, normalizedScope: normalized, scopedReport: scoped, maskedDimensions: masked}, nil
}

func (p *DataExchangeProvider) PlanExport(ctx context.Context, request dataexchange.ExportPlanRequest) (dataexchange.ExportPlan, error) {
	payload, err := exchangePayload(request.Options, request.ReferenceID)
	if err != nil {
		return dataexchange.ExportPlan{}, err
	}
	principal, err := p.principal(ctx, request.Scope)
	if err != nil {
		return dataexchange.ExportPlan{}, err
	}
	prepared, err := p.prepare(ctx, payload, principal, true)
	if err != nil {
		return dataexchange.ExportPlan{}, err
	}
	createdAt := request.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = p.dependencies.Clock().UTC()
	}
	return dataexchange.ExportPlan{Filename: SafeFilename(payload.ReportKey, payload.ObjectKey), ContentType: "text/csv; charset=utf-8", ExpiresAt: createdAt.Add(dataExchangeTTL(prepared.control))}, nil
}

func (p *DataExchangeProvider) ReadExportPage(ctx context.Context, request dataexchange.ExportPageRequest) (dataexchange.ExportPage, error) {
	payload, err := exchangePayload(request.Options, request.ReferenceID)
	if err != nil {
		return dataexchange.ExportPage{}, err
	}
	principal, err := p.principal(ctx, request.Scope)
	if err != nil {
		return dataexchange.ExportPage{}, err
	}
	prepared, err := p.prepare(ctx, payload, principal, true)
	if err != nil {
		return dataexchange.ExportPage{}, err
	}
	cursor, err := decodeDataExchangeCursor(request.Cursor)
	if err != nil {
		return dataexchange.ExportPage{}, err
	}
	pageSize := request.PageSize
	if pageSize < 1 || pageSize > reportmodel.ReportPageMaximumSize {
		pageSize = reportmodel.ReportPageMaximumSize
	}
	summary, err := p.dependencies.Domain.ExecuteExportReportPage(ctx, prepared.scopedReport, prepared.normalizedScope.Parameters, cursor.ExecutionCursor, cursor.Position, pageSize, principal)
	if err != nil {
		return dataexchange.ExportPage{}, err
	}
	pageRows, err := Rows(summary, prepared.normalizedScope.AnalysisKey)
	if err != nil {
		return dataexchange.ExportPage{}, err
	}
	processed := cursor.Position + len(pageRows)
	if payload.MaxRows > 0 && processed > payload.MaxRows {
		return dataexchange.ExportPage{}, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.report.export_too_many_rows", Params: map[string]string{"limit": fmt.Sprint(payload.MaxRows)}}
	}
	if payload.ExactTotal >= 0 && processed > payload.ExactTotal {
		return dataexchange.ExportPage{}, &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_result_changed"}
	}
	hasMore := summary.Truncated
	if payload.ExactTotal >= 0 {
		hasMore = processed < payload.ExactTotal
	}
	if len(pageRows) == 0 && hasMore {
		return dataexchange.ExportPage{}, &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_result_changed"}
	}
	next := ""
	if hasMore {
		if strings.TrimSpace(summary.ExecutionCursor) == "" {
			return dataexchange.ExportPage{}, &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_cursor_missing"}
		}
		next, err = encodeDataExchangeCursor(dataExchangeCursor{ExecutionCursor: summary.ExecutionCursor, Position: processed})
		if err != nil {
			return dataexchange.ExportPage{}, err
		}
	}
	columns := append([]string(nil), prepared.normalizedScope.FieldProjection...)
	watermark, expiresAt := "", request.ArtifactExpiresAt.UTC().Format(time.RFC3339Nano)
	if prepared.control.Watermark {
		columns = append(columns, "export_watermark", "download_expires_at")
		watermark = p.dependencies.Watermark(payload.ReportKey, payload.RequesterUserID, expiresAt)
	}
	rows := make([][]string, 0, len(pageRows))
	for _, source := range pageRows {
		row := make([]string, 0, len(columns))
		for _, key := range prepared.normalizedScope.FieldProjection {
			value, found := source.Dimensions[key]
			if !found {
				value = source.Measures[key]
			}
			if prepared.maskedDimensions[key] && value != "" {
				value = "******"
			}
			row = append(row, value)
		}
		if prepared.control.Watermark {
			row = append(row, watermark, expiresAt)
		}
		rows = append(rows, row)
	}
	total := processed
	if payload.ExactTotal >= 0 {
		total = payload.ExactTotal
	}
	return dataexchange.ExportPage{Columns: columns, Rows: rows, NextCursor: next, Total: total}, nil
}

func encodeDataExchangeCursor(cursor dataExchangeCursor) (string, error) {
	raw, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

func decodeDataExchangeCursor(value string) (dataExchangeCursor, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return dataExchangeCursor{}, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return dataExchangeCursor{}, &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_cursor_invalid", Err: err}
	}
	var cursor dataExchangeCursor
	if err = json.Unmarshal(raw, &cursor); err != nil || cursor.Position < 0 || strings.TrimSpace(cursor.ExecutionCursor) == "" {
		return dataExchangeCursor{}, &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_cursor_invalid", Err: err}
	}
	return cursor, nil
}

func dataExchangeTTL(control reportmodel.ReportExportControlSchema) time.Duration {
	if seconds, ok := control.Config["download_ttl_seconds"].(float64); ok && seconds >= 60 && seconds <= 86400 {
		return time.Duration(seconds) * time.Second
	}
	if seconds, ok := control.Config["download_ttl_seconds"].(int); ok && seconds >= 60 && seconds <= 86400 {
		return time.Duration(seconds) * time.Second
	}
	return dataExchangeDownloadTTL
}

func internalError(err error) error {
	return &apperror.AppError{Kind: apperror.KindInternal, Code: "backend.internal_error", Err: err}
}

func workspaceError(err error) error {
	return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required", Err: err}
}

var _ modulehost.ExportProvider = (*DataExchangeProvider)(nil)
var _ modulehost.ExportPlanningProvider = (*DataExchangeProvider)(nil)
var _ modulehost.ExportCompletionProvider = (*DataExchangeProvider)(nil)
