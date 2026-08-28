package report

import (
	"context"
	"fmt"
	"strings"
	"time"

	notificationmodel "github.com/domainry/domainry-notification-sdk/contract"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	auditcontract "github.com/domainry/domainry-runtime/runtime/domain/audit/contract"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	reportservice "github.com/domainry/domainry-runtime/runtime/domain/report/service"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
	"github.com/domainry/domainry-runtime/runtime/platform/productbrand"
)

type ReportRecordExporter interface {
	ExportReportRecords(context.Context, string, principalmodel.Principal) ([]byte, string, error)
}

var _ reportcontract.ReportRecordAccess = (*ReportRecordAdapter)(nil)
var _ reportcontract.ReportRecordReader = (*ReportRecordAdapter)(nil)
var _ reportcontract.ReportDatasetPushdownAuthorizer = (*ReportRecordAdapter)(nil)
var _ reportcontract.ReportObjectSQLFieldAuthorizer = (*ReportRecordAdapter)(nil)

// ReportRecordAdapter is the Report-owned anti-corruption layer over Record use cases.
type ReportRecordAdapter struct {
	application *recordapplication.RecordApplicationService
	repository  recordrepository.RecordRepository
	schemaMap   func() map[string]definitionmodel.ObjectSchema
}

func NewReportRecordAdapter(application *recordapplication.RecordApplicationService, repository recordrepository.RecordRepository, schemaMaps ...func() map[string]definitionmodel.ObjectSchema) *ReportRecordAdapter {
	var schemaMap func() map[string]definitionmodel.ObjectSchema
	if len(schemaMaps) > 0 {
		schemaMap = schemaMaps[0]
	}
	return &ReportRecordAdapter{application: application, repository: repository, schemaMap: schemaMap}
}

func (a *ReportRecordAdapter) ReportObjectForAction(_ context.Context, principal principalmodel.Principal, objectKey, action string) (definitionmodel.ObjectSchema, error) {
	return a.application.ObjectForAction(principal, objectKey, action)
}

func (a *ReportRecordAdapter) NormalizeReportListQuery(_ context.Context, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery, principal principalmodel.Principal) recordmodel.RecordListQuery {
	return a.application.NormalizeListQuery(object, query, principal)
}

func (a *ReportRecordAdapter) CanAccessReportRecord(_ context.Context, principal principalmodel.Principal, object definitionmodel.ObjectSchema, record recordmodel.Record) bool {
	return a.application.CanAccessRecord(principal, object, record)
}

func (a *ReportRecordAdapter) ProjectReportRecordFields(ctx context.Context, principal principalmodel.Principal, object definitionmodel.ObjectSchema, records []recordmodel.Record) ([]recordmodel.Record, error) {
	return a.application.ProjectRecordFields(ctx, principal, object, records, "report")
}

func (a *ReportRecordAdapter) CanPushdownReportDataset(_ context.Context, principal principalmodel.Principal, objects []definitionmodel.ObjectSchema) bool {
	for _, object := range objects {
		for _, field := range object.Fields {
			if !recordpolicy.RecordCanReadObjectFieldForPrincipal(principal, object, field) ||
				recordpolicy.RecordFieldReadMaskedForPrincipal(principal, object.Key, field.Key) ||
				recordpolicy.RecordFieldRequiresPolicyEvaluation(principal, object.Key, field.Key, "read") {
				return false
			}
		}
	}
	return true
}

func (a *ReportRecordAdapter) AuthorizeReportExportField(_ context.Context, principal principalmodel.Principal, objectKey, fieldKey string) (bool, error) {
	object, err := a.application.ObjectForAction(principal, objectKey, "export")
	if err != nil {
		return false, err
	}
	if fieldKey == "id" || fieldKey == "created_at" || fieldKey == "updated_at" {
		if !recordpolicy.RecordCanExportFieldForPrincipal(principal, object.Key, fieldKey) {
			return false, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.report.export_field_denied"}
		}
		return recordpolicy.RecordFieldExportMaskedForPrincipal(principal, object.Key, fieldKey), nil
	}
	for _, field := range object.Fields {
		if field.Key != fieldKey || field.DisabledAt != "" {
			continue
		}
		if !recordpolicy.RecordCanExportObjectFieldForPrincipal(principal, object, field) {
			return false, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.report.export_field_denied"}
		}
		return recordpolicy.RecordFieldExportMaskedForPrincipal(principal, object.Key, field.Key), nil
	}
	return false, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.report.export_field_not_found"}
}

func (a *ReportRecordAdapter) AuthorizeReportObjectSQLField(_ context.Context, principal principalmodel.Principal, object definitionmodel.ObjectSchema, fieldKey string) error {
	fieldKey = strings.TrimSpace(fieldKey)
	if _, err := a.application.ObjectForAction(principal, object.Key, "read"); err != nil {
		return err
	}
	if fieldKey != "id" && fieldKey != "created_at" && fieldKey != "updated_at" {
		found := false
		for _, field := range object.Fields {
			if field.Key == fieldKey && strings.TrimSpace(field.DisabledAt) == "" {
				found = true
				if !recordpolicy.RecordCanReadObjectFieldForPrincipal(principal, object, field) {
					return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.report.object_sql_field_denied"}
				}
				break
			}
		}
		if !found {
			return &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.report.object_sql_field_not_found"}
		}
	} else if !recordpolicy.RecordCanReadFieldForPrincipal(principal, object.Key, fieldKey) {
		return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.report.object_sql_field_denied"}
	}
	if recordpolicy.RecordFieldReadMaskedForPrincipal(principal, object.Key, fieldKey) {
		return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.report.object_sql_field_masked"}
	}
	if recordpolicy.RecordFieldRequiresPolicyEvaluation(principal, object.Key, fieldKey, "read") {
		return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.report.object_sql_field_contextual"}
	}
	return nil
}

func (a *ReportRecordAdapter) ExportReportRecords(ctx context.Context, objectKey string, principal principalmodel.Principal) ([]byte, string, error) {
	return a.application.ExportRecords(ctx, objectKey, principal)
}

func (a *ReportRecordAdapter) GetReportRecord(ctx context.Context, objectKey, recordID string, principal principalmodel.Principal) (recordmodel.Record, error) {
	return a.application.GetRecord(ctx, objectKey, recordID, principal)
}

func (a *ReportRecordAdapter) ListReportRecordsForPrincipal(ctx context.Context, objectKey string, query recordmodel.RecordListQuery, principal principalmodel.Principal) (recordmodel.RecordPageResult, error) {
	return a.application.ListRecords(ctx, objectKey, query, principal)
}

func (a *ReportRecordAdapter) CreateReportRecord(ctx context.Context, objectKey string, data map[string]any, idempotencyKey string, principal principalmodel.Principal) (recordmodel.Record, error) {
	return a.application.CreateRecordIdempotent(ctx, objectKey, data, idempotencyKey, principal)
}

func (a *ReportRecordAdapter) UpdateReportRecord(ctx context.Context, objectKey, recordID string, patch map[string]any, idempotencyKey string, principal principalmodel.Principal) (recordmodel.Record, error) {
	return a.application.UpdateRecordIdempotent(ctx, objectKey, recordID, patch, idempotencyKey, principal)
}

// TransitionReportExportAuditStatus is the narrow Runtime-owned write used
// after a download permission recheck fails. The requester may no longer have
// permission to update the audit object at that point, but the governed audit
// state must still leave prepared. Only the compiler-validated status field is
// patched, and the conditional update cannot overwrite a concurrent terminal
// transition.
func (a *ReportRecordAdapter) TransitionReportExportAuditStatus(ctx context.Context, workspaceID, objectKey, recordID, statusField, fromStatus, toStatus string) error {
	if a == nil || a.repository == nil || a.schemaMap == nil {
		return reportApplicationError(nil)
	}
	object, ok := a.schemaMap()[strings.TrimSpace(objectKey)]
	if !ok {
		return &apperror.AppError{Kind: apperror.KindNotFound, Code: "backend.report.export_audit_not_found"}
	}
	statusField = strings.TrimSpace(statusField)
	validStatusField := false
	for _, field := range object.Fields {
		if field.Key == statusField && field.DisabledAt == "" {
			validStatusField = true
			break
		}
	}
	if !validStatusField || strings.TrimSpace(recordID) == "" || strings.TrimSpace(fromStatus) == "" || strings.TrimSpace(toStatus) == "" {
		return &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.report.export_control_invalid"}
	}
	_, err := a.repository.UpdateRecordWhere(ctx, workspaceID, object, recordmodel.Record{
		ID:        strings.TrimSpace(recordID),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339Nano),
		Data:      map[string]any{statusField: strings.TrimSpace(toStatus)},
	}, map[string]any{statusField: strings.TrimSpace(fromStatus)})
	return err
}

func (a *ReportRecordAdapter) ListReportRecords(ctx context.Context, workspaceID string, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	return a.repository.ListRecords(ctx, workspaceID, object, query)
}

type ReportExportRecordStore interface {
	GetReportRecord(context.Context, string, string, principalmodel.Principal) (recordmodel.Record, error)
	ListReportRecordsForPrincipal(context.Context, string, recordmodel.RecordListQuery, principalmodel.Principal) (recordmodel.RecordPageResult, error)
	CreateReportRecord(context.Context, string, map[string]any, string, principalmodel.Principal) (recordmodel.Record, error)
	UpdateReportRecord(context.Context, string, string, map[string]any, string, principalmodel.Principal) (recordmodel.Record, error)
	TransitionReportExportAuditStatus(context.Context, string, string, string, string, string, string) error
}

type ReportApplicationDependencies struct {
	ProductBrandName      string
	Domain                *reportservice.ReportDomainService
	Records               ReportRecordExporter
	ExportRecords         ReportExportRecordStore
	ExportArtifacts       reportcontract.ReportExportArtifactStore
	Audit                 auditcontract.AuditAppender
	NotificationCompiler  func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
	NotificationCommitter ReportSnapshotNotificationCommitter
	ExportControls        func(context.Context, principalmodel.Principal) []reportmodel.ReportExportControlSchema
	Clock                 func() time.Time
	BatchJobs             *recordapplication.RecordApplicationService
	CursorKey             []byte
}

type ReportSnapshotNotificationCommitter interface {
	CompleteReportSnapshotWithNotification(context.Context, reportcontract.ReportSnapshotCompleteRequest, notificationmodel.NotificationEvent) error
	FailReportSnapshotWithNotification(context.Context, string, string, string, notificationmodel.NotificationEvent) error
}

// ReportApplicationService owns Report use-case sequencing across Record and Audit.
type ReportApplicationService struct {
	productBrandName    string
	domain              *reportservice.ReportDomainService
	records             ReportRecordExporter
	exportRecords       ReportExportRecordStore
	exportArtifacts     reportcontract.ReportExportArtifactStore
	audit               auditcontract.AuditAppender
	compileNotification func(notificationmodel.NotificationIntent) (notificationmodel.NotificationEvent, error)
	commitNotification  ReportSnapshotNotificationCommitter
	exportControls      func(context.Context, principalmodel.Principal) []reportmodel.ReportExportControlSchema
	clock               func() time.Time
	batchJobs           *recordapplication.RecordApplicationService
	cursorKey           []byte
}

func NewReportApplicationService(dependencies ReportApplicationDependencies) *ReportApplicationService {
	clock := dependencies.Clock
	if clock == nil {
		clock = time.Now
	}
	cursorKey := append([]byte(nil), dependencies.CursorKey...)
	if len(cursorKey) == 0 {
		cursorKey = []byte("report-pagination-test-key")
	}
	service := &ReportApplicationService{productBrandName: productbrand.ResolveName(dependencies.ProductBrandName), domain: dependencies.Domain, records: dependencies.Records, exportRecords: dependencies.ExportRecords, exportArtifacts: dependencies.ExportArtifacts, audit: dependencies.Audit, compileNotification: dependencies.NotificationCompiler, commitNotification: dependencies.NotificationCommitter, exportControls: dependencies.ExportControls, clock: clock, batchJobs: dependencies.BatchJobs, cursorKey: cursorKey}
	if service.batchJobs != nil {
		_ = service.batchJobs.RegisterOwnedBatchProcessor(reportExportBatchKind, service.processReportExportBatch)
	}
	return service
}

func (s *ReportApplicationService) Summary(ctx context.Context, reportKey string, principal principalmodel.Principal) (reportmodel.ReportSummary, error) {
	return s.SummaryMode(ctx, reportKey, "realtime", principal)
}

func (s *ReportApplicationService) SummaryMode(ctx context.Context, reportKey, mode string, principal principalmodel.Principal) (reportmodel.ReportSummary, error) {
	if _, err := principalmodel.QueryScopeForPrincipal(principal); err != nil {
		return reportmodel.ReportSummary{}, reportWorkspaceError(err)
	}
	return s.domain.SummaryMode(ctx, reportKey, mode, principal)
}

func (s *ReportApplicationService) QueryObjectSQL(ctx context.Context, reportKey string, parameters map[string]any, principal principalmodel.Principal) (reportmodel.ReportSummary, error) {
	if _, err := principalmodel.QueryScopeForPrincipal(principal); err != nil {
		return reportmodel.ReportSummary{}, reportWorkspaceError(err)
	}
	return s.domain.QueryObjectSQL(ctx, reportKey, parameters, principal)
}

func (s *ReportApplicationService) QueryObjectSQLPage(ctx context.Context, reportKey string, parameters map[string]any, page reportmodel.ReportPageRequest, principal principalmodel.Principal) (reportmodel.ReportSummary, error) {
	if _, err := principalmodel.QueryScopeForPrincipal(principal); err != nil {
		return reportmodel.ReportSummary{}, reportWorkspaceError(err)
	}
	report, err := s.domain.ReportForSummary(ctx, reportKey, principal)
	if err != nil {
		return reportmodel.ReportSummary{}, err
	}
	normalized, err := reportservice.NormalizeReportObjectSQLParameters(report.ObjectSQLV1.Parameters, parameters)
	if err != nil {
		return reportmodel.ReportSummary{}, err
	}
	pageSize := page.PageSize
	if pageSize == 0 {
		pageSize = reportmodel.ReportPageDefaultSize
	}
	if pageSize < 1 || pageSize > reportmodel.ReportPageMaximumSize {
		return reportmodel.ReportSummary{}, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.report.page_size_invalid", Params: map[string]string{"maximum": fmt.Sprint(reportmodel.ReportPageMaximumSize)}}
	}
	fingerprint, err := reportPageFingerprint(report, map[string]any{"parameters": normalized}, principal)
	if err != nil {
		return reportmodel.ReportSummary{}, reportApplicationError(err)
	}
	return s.executeStableReportOffsetPage(ctx, report, fingerprint, page, pageSize, principal, func(offset, size int) (reportmodel.ReportSummary, error) {
		return s.domain.QueryObjectSQLPage(ctx, reportKey, normalized, offset, size, principal)
	})
}

func (s *ReportApplicationService) RefreshSnapshot(ctx context.Context, reportKey, idempotencyKey string, principal principalmodel.Principal) (reportmodel.ReportSnapshot, error) {
	if _, err := principalmodel.CommandScopeForPrincipal(principal); err != nil {
		return reportmodel.ReportSnapshot{}, reportWorkspaceError(err)
	}
	refresh, terminalErr := s.domain.PrepareSnapshotRefresh(ctx, reportKey, idempotencyKey, principal)
	if refresh.TerminalStatus == "" {
		return refresh.Snapshot, terminalErr
	}
	event, notify, err := s.snapshotTerminalNotification(reportKey, idempotencyKey, refresh, principal)
	if err != nil {
		return reportmodel.ReportSnapshot{}, reportApplicationError(err)
	}
	if notify {
		if s.commitNotification == nil {
			return reportmodel.ReportSnapshot{}, &apperror.AppError{Kind: apperror.KindInternal, Code: "backend.report.snapshot_notification_committer_unavailable"}
		}
		if refresh.TerminalStatus == "succeeded" {
			err = s.commitNotification.CompleteReportSnapshotWithNotification(ctx, reportcontract.ReportSnapshotCompleteRequest{Snapshot: refresh.Snapshot, ExpectedStatus: "refreshing"}, event)
		} else {
			err = s.commitNotification.FailReportSnapshotWithNotification(ctx, refresh.Snapshot.ID, "refreshing", refresh.ErrorCode, event)
		}
	} else {
		err = s.domain.CommitSnapshotRefresh(ctx, refresh)
	}
	if err != nil {
		return reportmodel.ReportSnapshot{}, &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.snapshot_terminal_commit_failed", Err: err}
	}
	if terminalErr != nil {
		return reportmodel.ReportSnapshot{}, terminalErr
	}
	return refresh.Snapshot, nil
}

func (s *ReportApplicationService) snapshotTerminalNotification(reportKey, idempotencyKey string, refresh reportservice.ReportSnapshotRefresh, principal principalmodel.Principal) (notificationmodel.NotificationEvent, bool, error) {
	if s.compileNotification == nil || strings.TrimSpace(principal.UserID) == "" {
		return notificationmodel.NotificationEvent{}, false, nil
	}
	status := "completed"
	if refresh.TerminalStatus == "failed" {
		status = "failed"
	}
	sourceID := "report-snapshot:" + strings.TrimSpace(reportKey) + ":" + strings.TrimSpace(idempotencyKey) + ":" + status
	intent := notificationmodel.NotificationIntent{
		WorkspaceID: principal.WorkspaceID, SourceEventID: sourceID, EventType: "report.snapshot." + status, Surface: "business_workspace",
		RecipientUserIDs: []string{principal.UserID}, SubjectType: "report", SubjectID: strings.TrimSpace(reportKey), DedupeKey: sourceID,
		OccurredAt: reportSnapshotOccurredAt(refresh.Snapshot), Variables: map[string]any{"report_key": strings.TrimSpace(reportKey), "status": status, "error_code": refresh.ErrorCode},
	}
	event, err := s.compileNotification(intent)
	return event, true, err
}

func reportSnapshotOccurredAt(snapshot reportmodel.ReportSnapshot) string {
	if value := strings.TrimSpace(snapshot.RefreshedAt); value != "" {
		return value
	}
	return time.Now().UTC().Format(time.RFC3339Nano)
}

func (s *ReportApplicationService) ExportObject(ctx context.Context, reportKey, objectKey string, principal principalmodel.Principal) ([]byte, string, error) {
	if _, err := principalmodel.CommandScopeForPrincipal(principal); err != nil {
		return nil, "", reportWorkspaceError(err)
	}
	report, err := s.domain.ReportForExport(ctx, reportKey, objectKey, principal)
	if err != nil {
		return nil, "", err
	}
	if control, ok := s.exportControl(ctx, report.Key, objectKey, principal); ok && control.ApprovalRequired {
		return nil, "", &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.report.export_approval_required"}
	}
	content, filename, err := s.records.ExportReportRecords(ctx, objectKey, principal)
	if err != nil {
		return nil, "", err
	}
	if s.audit == nil {
		return nil, "", reportApplicationError(nil)
	}
	if err := s.audit.AppendAudit(ctx, auditcontract.AuditAppendRequest{
		Event: "report_object_exported", ObjectKey: objectKey, Principal: principal,
		Summary:  fmt.Sprintf("Exported %s from report %s", objectKey, report.Key),
		Metadata: map[string]any{"report_key": report.Key, "object_key": objectKey},
	}); err != nil {
		return nil, "", reportApplicationError(err)
	}
	return content, report.Key + "-" + filename, nil
}
