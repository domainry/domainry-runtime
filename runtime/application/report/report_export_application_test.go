package report

import accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	reportexport "github.com/domainry/domainry-runtime/runtime/application/report/export"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordpolicy "github.com/domainry/domainry-runtime/runtime/domain/record/policy"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	reportservice "github.com/domainry/domainry-runtime/runtime/domain/report/service"
)

type reportExportArtifactStoreStub struct {
	mu              sync.Mutex
	artifact        reportmodel.ReportExportArtifact
	createErr       error
	linkErr         error
	readErr         error
	ignoreWorkspace bool
}

func (s *reportExportArtifactStoreStub) CreateOrGetReportExportArtifact(_ context.Context, artifact reportmodel.ReportExportArtifact) (reportmodel.ReportExportArtifact, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.createErr != nil {
		return reportmodel.ReportExportArtifact{}, false, s.createErr
	}
	if s.artifact.ID != "" {
		if s.artifact.ScopeSHA256 != artifact.ScopeSHA256 {
			return reportmodel.ReportExportArtifact{}, false, reportcontract.ErrReportExportIdempotencyConflict
		}
		return s.artifact, false, nil
	}
	artifact.ID = "artifact-1"
	s.artifact = artifact
	return artifact, true, nil
}

func (s *reportExportArtifactStoreStub) LinkReportExportBusinessDownload(_ context.Context, _, artifactID, businessDownloadID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.linkErr != nil {
		return s.linkErr
	}
	if s.artifact.ID != artifactID {
		return errors.New("artifact not found")
	}
	s.artifact.BusinessDownloadID = businessDownloadID
	return nil
}

func (s *reportExportArtifactStoreStub) ReportExportArtifactByToken(_ context.Context, workspaceID, token string) (reportmodel.ReportExportArtifact, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readErr != nil {
		return reportmodel.ReportExportArtifact{}, false, s.readErr
	}
	if (s.ignoreWorkspace || s.artifact.WorkspaceID == workspaceID) && s.artifact.Token == token {
		return s.artifact, true, nil
	}
	return reportmodel.ReportExportArtifact{}, false, nil
}

func (s *reportExportArtifactStoreStub) ReportExportArtifactByIdempotency(_ context.Context, workspaceID, requesterUserID, reportKey, key string) (reportmodel.ReportExportArtifact, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.readErr != nil {
		return reportmodel.ReportExportArtifact{}, false, s.readErr
	}
	artifact := s.artifact
	if artifact.WorkspaceID == workspaceID && artifact.RequesterUserID == requesterUserID && artifact.ReportKey == reportKey && artifact.IdempotencyKey == key {
		return artifact, true, nil
	}
	return reportmodel.ReportExportArtifact{}, false, nil
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
	if request.PageSize > 0 {
		start := request.PageOffset
		if start > len(rows) {
			start = len(rows)
		}
		end := start + request.PageSize
		hasMore := end < len(rows)
		if end > len(rows) {
			end = len(rows)
		}
		return reportcontract.ReportObjectSQLExecutionResult{Rows: append([]map[string]string(nil), rows[start:end]...), HasMore: hasMore, Total: len(rows), TotalKnown: true}, nil
	}
	return reportcontract.ReportObjectSQLExecutionResult{Rows: append([]map[string]string(nil), rows...)}, nil
}

type reportExportFailingAccessStub struct {
	reportExportDatasetAccessStub
	err error
}

func (s reportExportFailingAccessStub) AuthorizeReportExportField(context.Context, principalmodel.Principal, string, string) (bool, error) {
	return false, s.err
}

type reportExportDatasetRecordsStub struct {
	items    []recordmodel.Record
	byObject map[string][]recordmodel.Record
	err      error
}

func (s reportExportDatasetRecordsStub) ListReportRecords(_ context.Context, _ string, object definitionmodel.ObjectSchema, _ recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	if s.err != nil {
		return recordmodel.RecordPageResult{}, s.err
	}
	if s.items != nil {
		return recordmodel.RecordPageResult{Items: s.items}, nil
	}
	if s.byObject != nil {
		return recordmodel.RecordPageResult{Items: s.byObject[object.Key]}, nil
	}
	return recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "customer-1", Data: map[string]any{}}}}, nil
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

func (s *reportExportStoreStub) CreateReportRecord(_ context.Context, objectKey string, data map[string]any, _ string, _ principalmodel.Principal) (recordmodel.Record, error) {
	if s.createErr != nil {
		return recordmodel.Record{}, s.createErr
	}
	s.download = recordmodel.Record{ID: "download-1", Data: data}
	return s.download, nil
}

func (s *reportExportStoreStub) UpdateReportRecord(_ context.Context, objectKey, recordID string, patch map[string]any, _ string, _ principalmodel.Principal) (recordmodel.Record, error) {
	if s.updateErr != nil {
		return recordmodel.Record{}, s.updateErr
	}
	target := &s.download
	if objectKey == "report_export_audit" {
		target = &s.audit
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

func TestGovernedReportExportRequiresApprovalAndProducesExpiringWatermarkedDownload(t *testing.T) {
	now := time.Date(2026, 7, 30, 12, 0, 0, 0, time.UTC)
	principal := reportPrincipal()
	store := &reportExportStoreStub{audit: recordmodel.Record{ID: "audit-1", Data: map[string]any{
		"report_key": "revenue", "requested_by": principal.UserID, "status": "approved",
	}}}
	records := &reportRecordExporterStub{}
	domain := reportservice.NewReportDomainService(reportservice.ReportDependencies{Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
		return []reportmodel.ReportSchema{{Key: "revenue", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "customer", Alias: "customer"}, Dimensions: []reportmodel.ReportDatasetDimension{{Key: "id", Field: reportmodel.ReportDatasetField{SourceAlias: "customer", FieldKey: "id"}}}}}}
	}, Access: reportExportDatasetAccessStub{}, Records: reportExportDatasetRecordsStub{}})
	mapping := reportExportTestRecordMapping()
	mapping.AuditRequesterField = "requested_by"
	control := reportmodel.ReportExportControlSchema{
		Key: "revenue-export", ReportKey: "revenue", SourceObjects: []string{"customer"}, ApprovalRequired: true,
		Watermark: true, AuditObject: "report_export_audit", DownloadObject: "report_export_download",
		RecordMapping: mapping,
	}
	service := NewReportApplicationService(ReportApplicationDependencies{
		ProductBrandName: "Acme",
		Domain:           domain, Records: records, ExportRecords: store, Audit: &reportAuditAppenderStub{},
		ExportArtifacts: &reportExportArtifactStoreStub{},
		ExportControls: func(context.Context, principalmodel.Principal) []reportmodel.ReportExportControlSchema {
			return []reportmodel.ReportExportControlSchema{control}
		},
		Clock: func() time.Time { return now },
	})

	if _, _, err := service.ExportObject(t.Context(), "revenue", "customer", principal); apperror.CodeOf(err) != "backend.report.export_approval_required" {
		t.Fatalf("governed direct export error=%v", err)
	}
	prepared, err := service.PrepareExport(t.Context(), "revenue", "customer", "audit-1", "prepare-1", principal)
	if err != nil || len(prepared.Token) != 64 || prepared.ExpiresAt != now.Add(15*time.Minute).Format(time.RFC3339Nano) || !prepared.Watermark {
		t.Fatalf("prepared=%#v error=%v", prepared, err)
	}
	content, filename, err := service.DownloadExport(t.Context(), prepared.Token, principal)
	if err != nil || filename == "" || !strings.Contains(string(content), "export_watermark,download_expires_at") || !strings.Contains(string(content), principal.UserID) || !strings.Contains(string(content), "Acme governed export") || strings.Contains(string(content), "Domainry governed export") {
		t.Fatalf("filename=%q content=%q error=%v", filename, content, err)
	}
	if store.audit.Data["status"] != "completed" {
		t.Fatalf("audit=%#v download=%#v", store.audit.Data, store.download.Data)
	}

	now = now.Add(16 * time.Minute)
	if _, _, err := service.DownloadExport(t.Context(), prepared.Token, principal); apperror.CodeOf(err) != "backend.report.export_download_expired" {
		t.Fatalf("expired download error=%v", err)
	}
}

func TestGovernedReportExportRejectsUnapprovedOrDifferentRequester(t *testing.T) {
	principal := reportPrincipal()
	for _, test := range []struct {
		name, requester, status, code string
	}{
		{"unapproved", principal.UserID, "requested", "backend.report.export_approval_required"},
		{"different requester", "operator-2", "approved", "backend.report.export_requester_mismatch"},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &reportExportStoreStub{audit: recordmodel.Record{ID: "audit-1", Data: map[string]any{"report_key": "revenue", "requested_by_identity_user_id": test.requester, "status": test.status}}}
			domain := reportservice.NewReportDomainService(reportservice.ReportDependencies{Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
				return []reportmodel.ReportSchema{{Key: "revenue", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "customer", Alias: "customer"}, Dimensions: []reportmodel.ReportDatasetDimension{{Key: "id", Field: reportmodel.ReportDatasetField{SourceAlias: "customer", FieldKey: "id"}}}}}}
			}, Access: reportExportDatasetAccessStub{}, Records: reportExportDatasetRecordsStub{}})
			service := NewReportApplicationService(ReportApplicationDependencies{
				Domain: domain, Records: &reportRecordExporterStub{}, ExportRecords: store, Audit: &reportAuditAppenderStub{},
				ExportArtifacts: &reportExportArtifactStoreStub{},
				ExportControls: func(context.Context, principalmodel.Principal) []reportmodel.ReportExportControlSchema {
					return []reportmodel.ReportExportControlSchema{{ReportKey: "revenue", SourceObjects: []string{"customer"}, ApprovalRequired: true, AuditObject: "report_export_audit", DownloadObject: "report_export_download", RecordMapping: reportExportTestRecordMapping()}}
				},
			})
			if _, err := service.PrepareExport(t.Context(), "revenue", "customer", "audit-1", "prepare-1", principal); apperror.CodeOf(err) != test.code {
				t.Fatalf("error=%v", err)
			}
		})
	}
}

func TestGovernedReportExportBindsScopeDatasetBytesHashRowsIdempotencyAndZeroResults(t *testing.T) {
	now := time.Date(2026, 8, 10, 9, 0, 0, 0, time.UTC)
	principal := reportPrincipal()
	report := reportmodel.ReportSchema{Key: "orders", Dataset: reportmodel.ReportDatasetSchema{
		Source: reportmodel.ReportDatasetSource{ObjectKey: "order", Alias: "order"},
		Dimensions: []reportmodel.ReportDatasetDimension{
			{Key: "status", Field: reportmodel.ReportDatasetField{SourceAlias: "order", FieldKey: "status"}},
			{Key: "day", Field: reportmodel.ReportDatasetField{SourceAlias: "order", FieldKey: "occurred_at"}, TimeGrain: "day"},
		},
		Measures: []reportmodel.ReportDatasetMeasure{{Key: "orders", Operation: "count", SourceAlias: "order"}}, TimeZone: "UTC",
	}, ExportScope: &reportmodel.ReportExportScopeSchema{
		Query: &reportmodel.ReportExportQueryScopeSchema{Mode: "any", Predicates: []reportmodel.ReportExportQueryPredicate{
			{Field: reportmodel.ReportDatasetField{SourceAlias: "order", FieldKey: "order_no"}, Operator: "contains"},
			{Field: reportmodel.ReportDatasetField{SourceAlias: "order", FieldKey: "customer_name"}, Operator: "starts_with"},
		}},
		Tags: &reportmodel.ReportExportTagScopeSchema{
			Join:        reportmodel.ReportDatasetJoin{Alias: "export_tags", ObjectKey: "tag_assignment", Type: "inner", LeftAlias: "order", LeftField: "id", RightField: "target_id", Cardinality: "one_to_many"},
			FamilyJoin:  &reportmodel.ReportDatasetJoin{Alias: "export_tag_definitions", ObjectKey: "tag_definition", Type: "inner", LeftAlias: "export_tags", LeftField: "tag_definition_id", RightField: "id", Cardinality: "many_to_one"},
			TargetField: reportmodel.ReportDatasetField{SourceAlias: "order", FieldKey: "id"}, TagField: reportmodel.ReportDatasetField{SourceAlias: "export_tag_definitions", FieldKey: "stable_key"}, AllowedMatchModes: []string{"any", "all"}, DefaultMatchMode: "all",
			FixedFilters: []reportmodel.ReportDatasetFilter{{Field: reportmodel.ReportDatasetField{SourceAlias: "export_tags", FieldKey: "active"}, Operator: "eq", Value: true}},
		},
	}}
	objects := map[string]definitionmodel.ObjectSchema{
		"order":          {Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}, {Key: "occurred_at", Type: "datetime"}, {Key: "order_no", Type: "text"}, {Key: "customer_name", Type: "text"}}},
		"tag_assignment": {Key: "tag_assignment", Fields: []definitionmodel.FieldSchema{{Key: "target_id", Type: "text"}, {Key: "tag_definition_id", Type: "text"}, {Key: "active", Type: "boolean"}}},
		"tag_definition": {Key: "tag_definition", Fields: []definitionmodel.FieldSchema{{Key: "stable_key", Type: "text"}}},
	}
	records := reportExportDatasetRecordsStub{byObject: map[string][]recordmodel.Record{
		"order": {
			{ID: "order-1", Data: map[string]any{"status": "paid", "occurred_at": "2026-08-09T03:00:00Z", "order_no": "PT-188", "customer_name": "Acme Import"}},
			{ID: "order-2", Data: map[string]any{"status": "cancelled", "occurred_at": "2026-08-09T04:00:00Z", "order_no": "PT-200", "customer_name": "Other"}},
			{ID: "order-3", Data: map[string]any{"status": "paid", "occurred_at": "2026-08-01T04:00:00Z", "order_no": "PT-300", "customer_name": "Acme Old"}},
			{ID: "order-4", Data: map[string]any{"status": "paid", "occurred_at": "2026-08-10T04:00:00Z", "order_no": "PT-400", "customer_name": "Acme No Tag"}},
		},
		"tag_assignment": {
			{ID: "tag-1", Data: map[string]any{"target_id": "order-1", "tag_definition_id": "priority-v1", "active": true}},
			{ID: "tag-2", Data: map[string]any{"target_id": "order-1", "tag_definition_id": "customer-v1", "active": true}},
			{ID: "tag-3", Data: map[string]any{"target_id": "order-4", "tag_definition_id": "priority-v1", "active": true}},
		},
		"tag_definition": {{ID: "priority-v1", Data: map[string]any{"stable_key": "priority"}}, {ID: "priority-v2-current", Data: map[string]any{"stable_key": "priority"}}, {ID: "customer-v1", Data: map[string]any{"stable_key": "customer"}}, {ID: "customer-v2-current", Data: map[string]any{"stable_key": "customer"}}},
	}}
	domain := reportservice.NewReportDomainService(reportservice.ReportDependencies{
		Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
			return []reportmodel.ReportSchema{report}
		},
		Access: reportExportDatasetAccessStub{objects: objects}, Records: records,
	})
	legacy := &reportExportStoreStub{audit: recordmodel.Record{ID: "audit-1", Data: map[string]any{"report_key": "orders", "requested_by_identity_user_id": principal.UserID, "status": "approved"}}}
	artifacts := &reportExportArtifactStoreStub{}
	control := reportmodel.ReportExportControlSchema{Key: "orders-export", ReportKey: "orders", SourceObjects: []string{"order", "tag_assignment", "tag_definition"}, AuditObject: "report_export_audit", DownloadObject: "report_export_download", RecordMapping: reportExportTestRecordMapping(), MaxRows: 100}
	audit := &reportAuditAppenderStub{}
	service := NewReportApplicationService(ReportApplicationDependencies{
		Domain: domain, Records: &reportRecordExporterStub{}, ExportRecords: legacy, ExportArtifacts: artifacts, Audit: audit,
		ExportControls: func(context.Context, principalmodel.Principal) []reportmodel.ReportExportControlSchema {
			return []reportmodel.ReportExportControlSchema{control}
		}, Clock: func() time.Time { return now },
	})
	scope := reportmodel.ReportExportScopeRequest{
		QueryKey: "  acME ", Tags: []string{"priority", " customer ", "priority"}, TagMatch: "all",
		Filters:   []reportmodel.ReportExportFilter{{DimensionKey: "status", Operator: "eq", Values: []string{"paid"}}},
		DateRange: &reportmodel.ReportExportDateRange{DimensionKey: "day", From: "2026-08-09", To: "2026-08-10"}, TimeZone: "Asia/Shanghai",
		FieldProjection: []string{"status", "orders"}, Purpose: "month close evidence", Freshness: reportmodel.ReportExportFreshness{Mode: "realtime"},
	}
	prepared, err := service.PrepareExportScoped(t.Context(), "orders", "order", "audit-1", "prepare-1", scope, principal)
	if err != nil {
		t.Fatal(err)
	}
	if prepared.RowCount != 1 || prepared.Scope.QueryKey != "acme" || strings.Join(prepared.Scope.Tags, ",") != "customer,priority" || prepared.Scope.TagMatch != "all" || len(prepared.ContentSHA256) != 64 || prepared.Scope.RoleKey != principal.RoleKey || prepared.Scope.DataScopes["order"] != "identity_policy" || prepared.Scope.DataScopes["tag_assignment"] != "identity_policy" || prepared.Scope.DataScopes["tag_definition"] != "identity_policy" || len(prepared.Scope.MetricDefinitions) != 1 {
		t.Fatalf("prepared=%#v", prepared)
	}
	if legacy.audit.Data["row_count"] != 1 || legacy.audit.Data["filters_hash"] != "sha256:"+artifacts.artifact.ScopeSHA256 || legacy.audit.Data["status"] != "completed" {
		t.Fatalf("prepared audit=%#v artifact=%#v", legacy.audit.Data, artifacts.artifact)
	}
	content, _, err := service.DownloadExport(t.Context(), prepared.Token, principal)
	if err != nil || string(content) != "status,orders\npaid,1\n" || reportexport.SHA256Hex(content) != prepared.ContentSHA256 {
		t.Fatalf("content=%q hash=%s err=%v", content, prepared.ContentSHA256, err)
	}
	if len(audit.requests) < 2 || audit.requests[0].Event != "report_export_download_prepared" || audit.requests[1].Event != "report_export_downloaded" {
		t.Fatalf("audit events=%#v", audit.requests)
	}
	report.ExportScope.Query.Mode = "all"
	if _, _, err := service.DownloadExport(t.Context(), prepared.Token, principal); apperror.CodeOf(err) != "backend.report.export_scope_changed" {
		t.Fatalf("report-owned scope definition drift err=%v", err)
	}
	report.ExportScope.Query.Mode = "any"
	legacy.audit.Data["status"] = "completed"
	changedAuthorization := principal
	changedAuthorization.AuthorizationRevision = "revision-2"
	if _, _, err := service.DownloadExport(t.Context(), prepared.Token, changedAuthorization); apperror.CodeOf(err) != "backend.report.export_scope_changed" {
		t.Fatalf("permission change err=%v", err)
	}
	if legacy.audit.Data["status"] != "denied" {
		t.Fatalf("permission denial audit=%#v", legacy.audit.Data)
	}
	legacy.audit.Data["status"] = "completed"
	artifacts.artifact.Content = []byte("tampered")
	if _, _, err := service.DownloadExport(t.Context(), prepared.Token, principal); apperror.CodeOf(err) != "backend.report.export_integrity_failed" {
		t.Fatalf("integrity err=%v", err)
	}
	artifacts.artifact.Content = content
	legacy.audit.Data["status"] = "completed"
	replay, err := service.PrepareExportScoped(t.Context(), "orders", "order", "audit-1", "prepare-1", scope, principal)
	if err != nil || replay.Token != prepared.Token || replay.ContentSHA256 != prepared.ContentSHA256 {
		t.Fatalf("replay=%#v err=%v", replay, err)
	}
	changed := scope
	changed.TagMatch = "any"
	if _, err := service.PrepareExportScoped(t.Context(), "orders", "order", "audit-1", "prepare-1", changed, principal); apperror.CodeOf(err) != "backend.idempotency.key_conflict" {
		t.Fatalf("scope conflict err=%v", err)
	}
	empty := scope
	empty.Filters = []reportmodel.ReportExportFilter{{DimensionKey: "status", Operator: "eq", Values: []string{"missing"}}}
	emptyLegacy := &reportExportStoreStub{audit: recordmodel.Record{ID: "audit-1", Data: map[string]any{"report_key": report.Key, "requested_by_identity_user_id": principal.UserID, "status": "approved"}}}
	emptyService := NewReportApplicationService(ReportApplicationDependencies{
		Domain: service.domain, Records: &reportRecordExporterStub{}, ExportRecords: emptyLegacy, ExportArtifacts: &reportExportArtifactStoreStub{}, Audit: &reportAuditAppenderStub{},
		ExportControls: service.exportControls, Clock: service.clock,
	})
	if prepared, err := emptyService.PrepareExportScoped(t.Context(), "orders", "order", "audit-1", "prepare-empty", empty, principal); err != nil || prepared.RowCount != 0 {
		t.Fatalf("empty prepared=%#v err=%v", prepared, err)
	}
	legacy.audit.Data["status"] = "completed"
	countOnly := report
	countOnly.Dataset.Dimensions = nil
	service.domain = reportservice.NewReportDomainService(reportservice.ReportDependencies{
		Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
			return []reportmodel.ReportSchema{countOnly}
		},
		Access: reportExportDatasetAccessStub{objects: objects}, Records: reportExportDatasetRecordsStub{items: []recordmodel.Record{}},
	})
	countLegacy := &reportExportStoreStub{audit: recordmodel.Record{ID: "audit-1", Data: map[string]any{"report_key": report.Key, "requested_by_identity_user_id": principal.UserID, "status": "approved"}}}
	countService := NewReportApplicationService(ReportApplicationDependencies{
		Domain: service.domain, Records: &reportRecordExporterStub{}, ExportRecords: countLegacy, ExportArtifacts: &reportExportArtifactStoreStub{}, Audit: &reportAuditAppenderStub{},
		ExportControls: service.exportControls, Clock: service.clock,
	})
	if prepared, err := countService.PrepareExportScoped(t.Context(), "orders", "order", "audit-1", "prepare-empty-count", reportmodel.ReportExportScopeRequest{Purpose: "count empty source", FieldProjection: []string{"orders"}, Freshness: reportmodel.ReportExportFreshness{Mode: "realtime"}}, principal); err != nil || prepared.RowCount != 1 {
		t.Fatalf("empty count-only source prepared=%#v err=%v", prepared, err)
	}
}

func TestGovernedObjectSQLExportUsesCanonicalTypedParametersAndReauthorizesDownload(t *testing.T) {
	now := time.Date(2026, 8, 15, 9, 0, 0, 0, time.UTC)
	principal := reportPrincipal()
	report := reportmodel.ReportSchema{Key: "sales.sql", ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{
		SQL:           `SELECT s.status AS status, SUM(s.amount) AS revenue FROM sale s WHERE s.sold_at >= :from_time AND s.amount >= :minimum GROUP BY s.status LIMIT 20`,
		SourceObjects: []string{"sale"},
		Parameters:    []reportmodel.ReportObjectSQLParameter{{Key: "from_time", Type: "datetime", Required: true}, {Key: "minimum", Type: "decimal", Required: true}},
		ResultSchema:  []reportmodel.ReportResultColumnSchema{{Key: "status", Type: "text", Kind: "dimension"}, {Key: "revenue", Type: "currency", Kind: "measure", Precision: 19, Scale: 2}},
	}}
	objects := map[string]definitionmodel.ObjectSchema{"sale": {Key: "sale", Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "text"}, {Key: "amount", Type: "currency", Config: map[string]any{"precision": 19, "scale": 2}}, {Key: "sold_at", Type: "datetime"}}}}
	executor := &reportExportObjectSQLExecutorStub{rows: []map[string]string{{"status": "paid", "revenue": "12.50"}}}
	domain := reportservice.NewReportDomainService(reportservice.ReportDependencies{
		Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
			return []reportmodel.ReportSchema{report}
		},
		Access: reportExportDatasetAccessStub{objects: objects}, ObjectSQL: executor,
	})
	legacy := &reportExportStoreStub{audit: recordmodel.Record{ID: "audit-1", Data: map[string]any{"report_key": report.Key, "requested_by_identity_user_id": principal.UserID, "status": "approved"}}}
	artifacts := &reportExportArtifactStoreStub{}
	control := reportmodel.ReportExportControlSchema{Key: "sales-sql-export", ReportKey: report.Key, SourceObjects: []string{"sale"}, AuditObject: "report_export_audit", DownloadObject: "report_export_download", RecordMapping: reportExportTestRecordMapping(), MaxRows: 10}
	service := NewReportApplicationService(ReportApplicationDependencies{
		Domain: domain, Records: &reportRecordExporterStub{}, ExportRecords: legacy, ExportArtifacts: artifacts, Audit: &reportAuditAppenderStub{},
		ExportControls: func(context.Context, principalmodel.Principal) []reportmodel.ReportExportControlSchema {
			return []reportmodel.ReportExportControlSchema{control}
		},
		Clock: func() time.Time { return now },
	})
	rawParameters := map[string]any{"from_time": "2026-08-15T08:00:00+08:00", "minimum": "12.500"}
	page, err := domain.QueryObjectSQL(t.Context(), report.Key, rawParameters, principal)
	if err != nil || page.ExecutionMode != "object_sql_v1" || len(page.ResultSchema) != 2 {
		t.Fatalf("page=%#v err=%v", page, err)
	}
	scope := reportmodel.ReportExportScopeRequest{Parameters: rawParameters, FieldProjection: []string{"status", "revenue"}, Purpose: "month close SQL evidence", Freshness: reportmodel.ReportExportFreshness{Mode: "realtime"}}
	prepared, err := service.PrepareExportScoped(t.Context(), report.Key, "sale", "audit-1", "prepare-sql-1", scope, principal)
	if err != nil {
		t.Fatal(err)
	}
	if len(executor.requests) != 2 || fmt.Sprint(executor.requests[0].Parameters) != fmt.Sprint(executor.requests[1].Parameters) {
		t.Fatalf("page/export parameters differ: %#v", executor.requests)
	}
	if prepared.Scope.Parameters["from_time"] != "2026-08-15T00:00:00Z" || prepared.Scope.Parameters["minimum"] != "12.5" || prepared.RowCount != 1 || len(prepared.ContentSHA256) != 64 {
		t.Fatalf("prepared=%#v", prepared)
	}
	if legacy.audit.Data["row_count"] != 1 || legacy.audit.Data["filters_hash"] != "sha256:"+artifacts.artifact.ScopeSHA256 || artifacts.artifact.RowCount != 1 {
		t.Fatalf("audit=%#v artifact=%#v", legacy.audit.Data, artifacts.artifact)
	}
	parameterHash, _ := reportexport.CanonicalJSONSHA256(prepared.Scope.Parameters)
	if metadata := service.audit.(*reportAuditAppenderStub).requests[0].Metadata; metadata["parameters_sha256"] != parameterHash || metadata["scope_sha256"] != artifacts.artifact.ScopeSHA256 {
		t.Fatalf("prepared audit metadata=%#v", metadata)
	}
	content, _, err := service.DownloadExport(t.Context(), prepared.Token, principal)
	if err != nil || string(content) != "status,revenue\npaid,12.50\n" || reportexport.SHA256Hex(content) != prepared.ContentSHA256 {
		t.Fatalf("content=%q err=%v", content, err)
	}

	legacy.audit.Data["status"] = "completed"
	changedParameters := scope
	changedParameters.Parameters = map[string]any{"from_time": "2026-08-15T08:00:00+08:00", "minimum": "13"}
	if _, err := service.PrepareExportScoped(t.Context(), report.Key, "sale", "audit-1", "prepare-sql-1", changedParameters, principal); apperror.CodeOf(err) != "backend.idempotency.key_conflict" {
		t.Fatalf("parameter change err=%v", err)
	}
	legacy.audit.Data["status"] = "completed"
	changedPermission := principal
	changedPermission.AuthorizationRevision = "revision-2"
	if _, _, err := service.DownloadExport(t.Context(), prepared.Token, changedPermission); apperror.CodeOf(err) != "backend.report.export_scope_changed" {
		t.Fatalf("permission change err=%v", err)
	}
	legacy.audit.Data["status"] = "completed"
	if _, err := service.PrepareExportScoped(t.Context(), report.Key, "sale", "audit-1", "prepare-unknown", reportmodel.ReportExportScopeRequest{Parameters: map[string]any{"arbitrary_sql": "DROP TABLE sale"}, Purpose: "invalid", Freshness: reportmodel.ReportExportFreshness{Mode: "realtime"}}, principal); apperror.CodeOf(err) != "backend.report.object_sql_parameter_unknown" {
		t.Fatalf("unknown parameter err=%v", err)
	}

	legacy.audit.Data["status"] = "completed"
	executor.rows = nil
	emptyLegacy := &reportExportStoreStub{audit: recordmodel.Record{ID: "audit-1", Data: map[string]any{"report_key": report.Key, "requested_by_identity_user_id": principal.UserID, "status": "approved"}}}
	emptyService := NewReportApplicationService(ReportApplicationDependencies{
		Domain: domain, Records: &reportRecordExporterStub{}, ExportRecords: emptyLegacy, ExportArtifacts: &reportExportArtifactStoreStub{}, Audit: &reportAuditAppenderStub{},
		ExportControls: service.exportControls, Clock: service.clock,
	})
	if prepared, err := emptyService.PrepareExportScoped(t.Context(), report.Key, "sale", "audit-1", "prepare-empty-sql", scope, principal); err != nil || prepared.RowCount != 0 {
		t.Fatalf("zero-row SQL export prepared=%#v err=%v", prepared, err)
	}
}

func TestReportExportScopeMasksDimensionsAndRejectsMaskedMeasures(t *testing.T) {
	principal := reportPrincipal()
	accessfixture.Set(&principal, accessfixture.Bundle{
		Key: "report-exporter", Permissions: []string{"order.export"}, RecordScope: "all_records",
		FieldPolicies: []accessfixture.FieldPolicyFixture{
			{ObjectKey: "order", FieldKey: "status", Read: true, Export: true, Masked: true},
			{ObjectKey: "order", FieldKey: "amount", Read: true, Export: true, Masked: true},
		},
	})
	report := reportmodel.ReportSchema{Key: "orders", Dataset: reportmodel.ReportDatasetSchema{
		Source:     reportmodel.ReportDatasetSource{ObjectKey: "order", Alias: "order"},
		Dimensions: []reportmodel.ReportDatasetDimension{{Key: "status", Field: reportmodel.ReportDatasetField{SourceAlias: "order", FieldKey: "status"}}},
		Measures:   []reportmodel.ReportDatasetMeasure{{Key: "orders", Operation: "count", SourceAlias: "order"}},
	}}
	request := reportmodel.ReportExportScopeRequest{Purpose: "masked dimension proof", Freshness: reportmodel.ReportExportFreshness{Mode: "realtime"}}
	_, _, masked, err := reportexport.NormalizeScope(report, "order", reportmodel.ReportExportControlSchema{MaskingRequired: true}, request, principal)
	if err != nil || !masked["status"] {
		t.Fatalf("masked=%v err=%v", masked, err)
	}
	report.Dataset.Measures = append(report.Dataset.Measures, reportmodel.ReportDatasetMeasure{Key: "amount", Operation: "sum", Field: &reportmodel.ReportDatasetField{SourceAlias: "order", FieldKey: "amount"}})
	if _, _, _, err := reportexport.NormalizeScope(report, "order", reportmodel.ReportExportControlSchema{MaskingRequired: true}, request, principal); apperror.CodeOf(err) != "backend.report.export_sensitive_measure_denied" {
		t.Fatalf("masked measure err=%v", err)
	}
	if _, _, _, err := reportexport.NormalizeScope(report, "order", reportmodel.ReportExportControlSchema{MaskingRequired: false}, request, principal); apperror.CodeOf(err) != "backend.report.export_field_denied" {
		t.Fatalf("unmasked control err=%v", err)
	}
}
