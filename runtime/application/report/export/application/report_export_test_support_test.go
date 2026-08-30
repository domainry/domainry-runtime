package application

import (
	"context"
	"fmt"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	auditcontract "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

type reportAuditAppenderStub struct{}

func (*reportAuditAppenderStub) AppendAudit(context.Context, auditcontract.AuditAppendRequest) error {
	return nil
}

type reportSnapshotSourceStub struct{}

func (reportSnapshotSourceStub) ReadReportSnapshotSourceVersion(context.Context, reportcontract.ReportSnapshotSourceVersionRequest) (reportmodel.ReportSnapshotSourceVersion, error) {
	return reportmodel.ReportSnapshotSourceVersion{Watermark: "stable", SourceVersions: map[string]string{"customer": "v1"}}, nil
}

func reportPrincipal() principalmodel.Principal {
	bundle := accessfixture.Bundle{Key: "report-export", RecordScope: "all_records", Permissions: []string{"customer.read", "customer.export"}, FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: "customer", FieldKey: "id", Read: true, Export: true}}}
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "operator-1", WorkspaceID: "workspace-a"}}, bundle)
}

type reportExportDatasetAccessStub struct {
	object  definitionmodel.ObjectSchema
	objects map[string]definitionmodel.ObjectSchema
}

func (s reportExportDatasetAccessStub) ReportObjectForAction(_ context.Context, _ principalmodel.Principal, objectKey, _ string) (definitionmodel.ObjectSchema, error) {
	if s.objects != nil {
		return s.objects[objectKey], nil
	}
	if s.object.Key != "" {
		return s.object, nil
	}
	return definitionmodel.ObjectSchema{Key: objectKey}, nil
}

func (reportExportDatasetAccessStub) NormalizeReportListQuery(_ context.Context, _ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery, _ principalmodel.Principal) recordmodel.RecordListQuery {
	return query
}

func (reportExportDatasetAccessStub) CanAccessReportRecord(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool {
	return true
}

func (reportExportDatasetAccessStub) AuthorizeReportExportField(_ context.Context, principal principalmodel.Principal, objectKey, fieldKey string) (bool, error) {
	if !recordpolicy.RecordCanExportFieldForPrincipal(principal, objectKey, fieldKey) {
		return false, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.report.export_field_denied"}
	}
	return recordpolicy.RecordFieldExportMaskedForPrincipal(principal, objectKey, fieldKey), nil
}

func (reportExportDatasetAccessStub) AuthorizeReportObjectSQLField(_ context.Context, principal principalmodel.Principal, object definitionmodel.ObjectSchema, fieldKey string) error {
	if !recordpolicy.RecordCanReadFieldForPrincipal(principal, object.Key, fieldKey) {
		return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.report.object_sql_field_denied"}
	}
	return nil
}

type reportExportObjectSQLExecutorStub struct {
	requests []reportcontract.ReportObjectSQLExecutionRequest
	rows     []map[string]string
}

func (s *reportExportObjectSQLExecutorStub) ExecuteReportObjectSQL(_ context.Context, request reportcontract.ReportObjectSQLExecutionRequest) (reportcontract.ReportObjectSQLExecutionResult, error) {
	s.requests = append(s.requests, request)
	rows := s.rows
	if request.PageSize <= 0 {
		return reportcontract.ReportObjectSQLExecutionResult{Rows: append([]map[string]string(nil), rows...)}, nil
	}
	start := request.PagePosition
	if start > len(rows) {
		start = len(rows)
	}
	end := start + request.PageSize
	hasMore := end < len(rows)
	if end > len(rows) {
		end = len(rows)
	}
	nextCursor := ""
	if hasMore {
		nextCursor = fmt.Sprintf("cursor:%d", end)
	}
	return reportcontract.ReportObjectSQLExecutionResult{Rows: append([]map[string]string(nil), rows[start:end]...), HasMore: hasMore, Total: len(rows), TotalKnown: true, NextCursor: nextCursor}, nil
}

type reportExportStoreStub struct {
	audit                                 recordmodel.Record
	download                              recordmodel.Record
	getErr, listErr, createErr, updateErr error
	transitionErr                         error
}

func (s *reportExportStoreStub) TransitionReportExportAuditStatus(_ context.Context, _, _, recordID, statusField, fromStatus, toStatus string) error {
	if s.transitionErr != nil {
		return s.transitionErr
	}
	if s.audit.ID == recordID && strings.TrimSpace(fmt.Sprint(s.audit.Data[statusField])) == fromStatus {
		s.audit.Data[statusField] = toStatus
	}
	return nil
}

func (s *reportExportStoreStub) GetReportRecord(_ context.Context, objectKey, recordID string, _ principalmodel.Principal) (recordmodel.Record, error) {
	if s.getErr != nil {
		return recordmodel.Record{}, s.getErr
	}
	if objectKey == "report_export_audit" && recordID == s.audit.ID {
		return s.audit, nil
	}
	if objectKey == "report_export_download" && recordID == s.download.ID {
		return s.download, nil
	}
	return recordmodel.Record{}, &apperror.AppError{Kind: apperror.KindNotFound, Code: "backend.record.not_found"}
}

func (s *reportExportStoreStub) ListReportRecordsForPrincipal(_ context.Context, objectKey string, query recordmodel.RecordListQuery, _ principalmodel.Principal) (recordmodel.RecordPageResult, error) {
	if s.listErr != nil {
		return recordmodel.RecordPageResult{}, s.listErr
	}
	if objectKey == "report_export_download" && s.download.ID != "" && query.Filters["file_reference"] == s.download.Data["file_reference"] {
		return recordmodel.RecordPageResult{Items: []recordmodel.Record{s.download}, Total: 1}, nil
	}
	return recordmodel.RecordPageResult{}, nil
}

func (s *reportExportStoreStub) CreateReportRecord(_ context.Context, _ string, data map[string]any, _ string, _ principalmodel.Principal) (recordmodel.Record, error) {
	if s.createErr != nil {
		return recordmodel.Record{}, s.createErr
	}
	s.download = recordmodel.Record{ID: "download-1", Data: data}
	return s.download, nil
}

func (s *reportExportStoreStub) UpdateReportRecord(_ context.Context, objectKey, _ string, patch map[string]any, _ string, _ principalmodel.Principal) (recordmodel.Record, error) {
	if s.updateErr != nil {
		return recordmodel.Record{}, s.updateErr
	}
	target := &s.download
	if objectKey == "report_export_audit" {
		target = &s.audit
	}
	if target.Data == nil {
		target.Data = map[string]any{}
	}
	for key, value := range patch {
		target.Data[key] = value
	}
	return *target, nil
}

func reportExportTestRecordMapping() reportmodel.ReportExportRecordMappingSchema {
	return reportmodel.ReportExportRecordMappingSchema{
		AuditReportKeyField: "report_key", AuditRequesterField: "requested_by_identity_user_id", AuditStatusField: "status",
		AuditPreparedStatuses: []string{"approved", "completed"}, AuditPreparedStatus: "completed", AuditDownloadedStatus: "completed", AuditDeniedStatus: "denied", AuditExpiredStatus: "expired",
		AuditRowCountField: "row_count", AuditScopeHashField: "filters_hash",
		DownloadAuditField: "audit_id", DownloadFilenameField: "file_name", DownloadContentHashField: "content_hash", DownloadExpiresAtField: "expires_at",
		DownloadTokenField: "file_reference", DownloadWatermarkedField: "watermarked", DownloadNumberField: "download_no",
	}
}

func reportExportEdgeControl() reportmodel.ReportExportControlSchema {
	return reportmodel.ReportExportControlSchema{ReportKey: "revenue", SourceObjects: []string{"customer"}, AuditObject: "report_export_audit", DownloadObject: "report_export_download", MaxRows: 1000, RecordMapping: reportExportTestRecordMapping()}
}
