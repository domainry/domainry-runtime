package adapter

import (
	"context"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
)

func reportApplicationError(err error) error {
	return &apperror.AppError{Kind: apperror.KindInternal, Code: "backend.internal_error", Err: err}
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
