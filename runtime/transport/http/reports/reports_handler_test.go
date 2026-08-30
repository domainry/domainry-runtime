package reports

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	identitysdk "github.com/domainry/domainry-identity-sdk"

	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	reportapplication "github.com/domainry/domainry-runtime/runtime/application/report"
	auditcontract "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	reportservice "github.com/domainry/domainry-runtime/runtime/domain/report/service"
)

type reportsAuditStub struct{ err error }

func (s *reportsAuditStub) AppendAudit(context.Context, auditcontract.AuditAppendRequest) error {
	return s.err
}

type reportsHandlerCapture struct{ serviceErr error }

type reportsEmptyAccess struct{}

func (reportsEmptyAccess) ReportObjectForAction(_ context.Context, _ principalmodel.Principal, objectKey, _ string) (definitionmodel.ObjectSchema, error) {
	return definitionmodel.ObjectSchema{Key: objectKey}, nil
}
func (reportsEmptyAccess) NormalizeReportListQuery(_ context.Context, _ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery, _ principalmodel.Principal) recordmodel.RecordListQuery {
	return query
}
func (reportsEmptyAccess) CanAccessReportRecord(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool {
	return true
}
func (reportsEmptyAccess) AuthorizeReportExportField(context.Context, principalmodel.Principal, string, string) (bool, error) {
	return false, nil
}
func (reportsEmptyAccess) CanPushdownReportDataset(context.Context, principalmodel.Principal, []definitionmodel.ObjectSchema) bool {
	return true
}

type reportsEmptyRecords struct{}

func (reportsEmptyRecords) ListReportRecords(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	return recordmodel.RecordPageResult{}, nil
}

type reportsOneRecords struct{}

func (reportsOneRecords) ListReportRecords(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	return recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "customer-1", Data: map[string]any{}}}}, nil
}

type reportsObjectSQLAccess struct{ object definitionmodel.ObjectSchema }

func (a reportsObjectSQLAccess) ReportObjectForAction(context.Context, principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
	return a.object, nil
}
func (reportsObjectSQLAccess) NormalizeReportListQuery(_ context.Context, _ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery, _ principalmodel.Principal) recordmodel.RecordListQuery {
	return query
}
func (reportsObjectSQLAccess) CanAccessReportRecord(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool {
	return true
}
func (reportsObjectSQLAccess) AuthorizeReportObjectSQLField(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, string) error {
	return nil
}

type reportsObjectSQLExecutor struct {
	request reportcontract.ReportObjectSQLExecutionRequest
	rows    []map[string]string
}

func (e *reportsObjectSQLExecutor) ExecuteReportObjectSQL(_ context.Context, request reportcontract.ReportObjectSQLExecutionRequest) (reportcontract.ReportObjectSQLExecutionResult, error) {
	e.request = request
	return reportcontract.ReportObjectSQLExecutionResult{Rows: e.rows}, nil
}

type reportsDataExchangeBinding struct{ job dataexchange.Job }

func (*reportsDataExchangeBinding) Descriptor() dataexchange.Descriptor {
	return dataexchange.Descriptor{ProtocolVersion: dataexchange.ProtocolVersionV1, Mode: dataexchange.DeploymentModeModule}
}
func (*reportsDataExchangeBinding) SubmitImport(context.Context, dataexchange.ImportRequest) (dataexchange.Job, bool, error) {
	return dataexchange.Job{}, false, nil
}
func (b *reportsDataExchangeBinding) SubmitExport(_ context.Context, request dataexchange.ExportRequest) (dataexchange.Job, bool, error) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	b.job = dataexchange.Job{ID: "data_exchange:report-1", Provider: request.Provider, Operation: "export", Status: "queued", WorkspaceID: request.Scope.WorkspaceID, ObjectKey: request.ObjectKey, ActorID: request.Scope.ActorID, RoleKey: request.Scope.RoleKey, ReferenceID: request.ReferenceID, Options: append([]byte(nil), request.Options...), CreatedAt: now, UpdatedAt: now}
	return b.job, false, nil
}
func (b *reportsDataExchangeBinding) Job(context.Context, dataexchange.JobRequest) (dataexchange.Job, error) {
	return b.job, nil
}
func (b *reportsDataExchangeBinding) Cancel(context.Context, dataexchange.JobRequest) (dataexchange.Job, error) {
	b.job.Status = "cancelled"
	return b.job, nil
}
func (*reportsDataExchangeBinding) Download(context.Context, dataexchange.JobRequest) (dataexchange.Artifact, error) {
	return dataexchange.Artifact{}, nil
}
func (*reportsDataExchangeBinding) Start(context.Context, dataexchange.WorkerConfig) <-chan struct{} {
	return workerplatform.Stopped()
}
func (*reportsDataExchangeBinding) Close(context.Context) error { return nil }

type reportsSnapshotReplayStore struct {
	snapshot reportmodel.ReportSnapshot
	err      error
}

type reportsExportRecordStore struct {
	audit    recordmodel.Record
	download recordmodel.Record
}

func (s *reportsExportRecordStore) GetReportRecord(_ context.Context, objectKey, _ string, _ principalmodel.Principal) (recordmodel.Record, error) {
	if objectKey == "report_export_audit" {
		return s.audit, nil
	}
	return s.download, nil
}
func (s *reportsExportRecordStore) ListReportRecordsForPrincipal(context.Context, string, recordmodel.RecordListQuery, principalmodel.Principal) (recordmodel.RecordPageResult, error) {
	if s.download.ID == "" {
		return recordmodel.RecordPageResult{}, nil
	}
	return recordmodel.RecordPageResult{Items: []recordmodel.Record{s.download}}, nil
}
func (s *reportsExportRecordStore) CreateReportRecord(_ context.Context, _ string, data map[string]any, _ string, _ principalmodel.Principal) (recordmodel.Record, error) {
	s.download = recordmodel.Record{ID: "download-1", Data: data}
	return s.download, nil
}
func (s *reportsExportRecordStore) UpdateReportRecord(_ context.Context, _ string, _ string, patch map[string]any, _ string, _ principalmodel.Principal) (recordmodel.Record, error) {
	for key, value := range patch {
		s.audit.Data[key] = value
	}
	return s.audit, nil
}
func (s *reportsExportRecordStore) TransitionReportExportAuditStatus(_ context.Context, _, _, recordID, statusField, fromStatus, toStatus string) error {
	if s.audit.ID == recordID && fmt.Sprint(s.audit.Data[statusField]) == fromStatus {
		s.audit.Data[statusField] = toStatus
	}
	return nil
}

func (s reportsSnapshotReplayStore) BeginReportSnapshot(context.Context, reportcontract.ReportSnapshotBeginRequest) (reportmodel.ReportSnapshot, bool, error) {
	return s.snapshot, false, s.err
}
func (reportsSnapshotReplayStore) CompleteReportSnapshot(context.Context, reportcontract.ReportSnapshotCompleteRequest) error {
	return nil
}
func (reportsSnapshotReplayStore) FailReportSnapshot(context.Context, reportcontract.ReportSnapshotFailRequest) error {
	return nil
}
func (reportsSnapshotReplayStore) LatestReportSnapshot(context.Context, string, string, string) (reportmodel.ReportSnapshot, bool, error) {
	return reportmodel.ReportSnapshot{}, false, nil
}
func (reportsSnapshotReplayStore) ReadReportSnapshotSourceVersion(context.Context, reportcontract.ReportSnapshotSourceVersionRequest) (reportmodel.ReportSnapshotSourceVersion, error) {
	return reportmodel.ReportSnapshotSourceVersion{}, nil
}

func TestReportsObjectSQLQueryRouteBindsTypedParameters(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "sale", Fields: []definitionmodel.FieldSchema{{Key: "amount", Type: "currency", Config: map[string]any{"precision": 19, "scale": 2}}}}
	report := reportmodel.ReportSchema{Key: "sales", ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{
		SQL: `SELECT s.amount AS amount FROM sale s WHERE s.amount >= :minimum LIMIT 1`, SourceObjects: []string{"sale"},
		Parameters: []reportmodel.ReportObjectSQLParameter{{Key: "minimum", Type: "decimal", Required: true}}, ResultSchema: []reportmodel.ReportResultColumnSchema{{Key: "amount", Type: "currency", Kind: "measure", Precision: 19, Scale: 2}},
	}}
	executor := &reportsObjectSQLExecutor{rows: []map[string]string{{"amount": "10.10"}}}
	domain := reportservice.NewReportDomainService(reportservice.ReportDependencies{Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
		return []reportmodel.ReportSchema{report}
	}, Access: reportsObjectSQLAccess{object: object}, ObjectSQL: executor, SnapshotSources: reportsSnapshotReplayStore{}})
	service := reportapplication.NewReportApplicationService(reportapplication.ReportApplicationDependencies{Domain: domain})
	handler := NewReportsHandler(ReportsDependencies{Service: service, Principal: func(*http.Request) principalmodel.Principal {
		return principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}
	}, WriteJSON: func(w http.ResponseWriter, status int, value any) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(value)
	}, WriteServiceError: func(w http.ResponseWriter, _ *http.Request, err error) {
		t.Errorf("service error: %v", err)
		w.WriteHeader(http.StatusUnprocessableEntity)
	}})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	request := httptest.NewRequest(http.MethodPost, "/reports/sales/query", strings.NewReader(`{"parameters":{"minimum":10.10}}`))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || executor.request.WorkspaceID != "workspace-a" || fmt.Sprint(executor.request.Parameters["minimum"]) != "10.1" {
		t.Fatalf("status=%d request=%#v body=%s", response.Code, executor.request, response.Body.String())
	}
}

func TestReportsObjectSQLQueryRouteWritesEmptyRowsAsJSONArray(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "sale", Fields: []definitionmodel.FieldSchema{{Key: "amount", Type: "currency", Config: map[string]any{"precision": 19, "scale": 2}}}}
	report := reportmodel.ReportSchema{Key: "sales", ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{
		SQL: `SELECT s.amount AS amount FROM sale s WHERE s.amount >= :minimum LIMIT 1`, SourceObjects: []string{"sale"},
		Parameters: []reportmodel.ReportObjectSQLParameter{{Key: "minimum", Type: "decimal", Required: true}}, ResultSchema: []reportmodel.ReportResultColumnSchema{{Key: "amount", Type: "currency", Kind: "measure", Precision: 19, Scale: 2}},
	}}
	domain := reportservice.NewReportDomainService(reportservice.ReportDependencies{Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
		return []reportmodel.ReportSchema{report}
	}, Access: reportsObjectSQLAccess{object: object}, ObjectSQL: &reportsObjectSQLExecutor{rows: []map[string]string{}}, SnapshotSources: reportsSnapshotReplayStore{}})
	service := reportapplication.NewReportApplicationService(reportapplication.ReportApplicationDependencies{Domain: domain})
	handler := NewReportsHandler(ReportsDependencies{Service: service, Principal: func(*http.Request) principalmodel.Principal {
		return principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}
	}, WriteJSON: func(w http.ResponseWriter, status int, value any) {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(value)
	}, WriteServiceError: func(w http.ResponseWriter, _ *http.Request, err error) {
		t.Errorf("service error: %v", err)
		w.WriteHeader(http.StatusUnprocessableEntity)
	}})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)
	request := httptest.NewRequest(http.MethodPost, "/reports/sales/query", strings.NewReader(`{"parameters":{"minimum":10.10}}`))
	response := httptest.NewRecorder()
	mux.ServeHTTP(response, request)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"rows":[]`) || strings.Contains(response.Body.String(), `"rows":null`) {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
}

func newReportsSummaryHandler(reports []reportmodel.ReportSchema, capture *reportsHandlerCapture) *ReportsHandler {
	domain := reportservice.NewReportDomainService(reportservice.ReportDependencies{
		Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
			return append([]reportmodel.ReportSchema(nil), reports...)
		},
		Access: reportsEmptyAccess{}, Records: reportsEmptyRecords{}, ObjectSQL: &reportsObjectSQLExecutor{rows: []map[string]string{{}}}, SnapshotSources: reportsSnapshotReplayStore{},
	})
	service := reportapplication.NewReportApplicationService(reportapplication.ReportApplicationDependencies{Domain: domain})
	return NewReportsHandler(ReportsDependencies{
		Service: service,
		Principal: func(*http.Request) principalmodel.Principal {
			return principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "operator-1", WorkspaceID: "workspace-a"}}
		},
		WriteJSON: func(w http.ResponseWriter, status int, value any) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		},
		WriteServiceError: func(w http.ResponseWriter, _ *http.Request, err error) {
			capture.serviceErr = err
			w.WriteHeader(http.StatusUnprocessableEntity)
		},
	})
}

func newReportsSnapshotHandler(store reportsSnapshotReplayStore, capture *reportsHandlerCapture) *ReportsHandler {
	domain := reportservice.NewReportDomainService(reportservice.ReportDependencies{
		Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
			return []reportmodel.ReportSchema{{
				Key: "revenue", Name: "Revenue",
				Dataset:         reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "empty", Alias: "empty"}},
				Materialization: &reportmodel.ReportMaterializationPolicy{MaximumLagSeconds: 60},
			}}
		},
		Snapshots: store, SnapshotSources: store,
	})
	service := reportapplication.NewReportApplicationService(reportapplication.ReportApplicationDependencies{Domain: domain})
	return NewReportsHandler(ReportsDependencies{
		Service: service,
		Principal: func(*http.Request) principalmodel.Principal {
			return principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "operator-1", WorkspaceID: "workspace-a"}}
		},
		WriteJSON: func(w http.ResponseWriter, status int, value any) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		},
		WriteServiceError: func(w http.ResponseWriter, _ *http.Request, err error) {
			capture.serviceErr = err
			w.WriteHeader(http.StatusUnprocessableEntity)
		},
	})
}

func reportsSummaryRequest(reportKey string) *http.Request {
	request := httptest.NewRequest(http.MethodGet, "/reports/report/summary", nil)
	request.SetPathValue("reportKey", reportKey)
	return request
}

func TestReportsSummaryHandlerWritesSummaryAndMapsMissingReport(t *testing.T) {
	capture := &reportsHandlerCapture{}
	handler := newReportsSummaryHandler([]reportmodel.ReportSchema{{Key: "empty", Name: "Empty report", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "empty", Alias: "empty"}}}}, capture)
	success := httptest.NewRecorder()
	handler.reportSummary(success, reportsSummaryRequest(" empty "))
	if success.Code != http.StatusOK || capture.serviceErr != nil || !strings.Contains(success.Body.String(), `"key":"empty"`) || !strings.Contains(success.Body.String(), `"rows":[`) || !strings.Contains(success.Body.String(), `"page_size":100`) || !strings.Contains(success.Body.String(), `"truncated":false`) || !strings.Contains(success.Body.String(), `"total":1`) || !strings.Contains(success.Body.String(), `"total_semantics":"exact"`) {
		t.Fatalf("success status=%d error=%v body=%s", success.Code, capture.serviceErr, success.Body.String())
	}

	capture.serviceErr = nil
	invalidPage := reportsSummaryRequest("empty")
	invalidPage.URL.RawQuery = "page_size=201"
	handler.reportSummary(httptest.NewRecorder(), invalidPage)
	if apperror.CodeOf(capture.serviceErr) != "backend.report.page_size_invalid" {
		t.Fatalf("invalid page error=%v", capture.serviceErr)
	}
	capture.serviceErr = nil
	invalidSyntax := reportsSummaryRequest("empty")
	invalidSyntax.URL.RawQuery = "page_size=many"
	handler.reportSummary(httptest.NewRecorder(), invalidSyntax)
	if apperror.CodeOf(capture.serviceErr) != "backend.report.page_size_invalid" {
		t.Fatalf("invalid page syntax error=%v", capture.serviceErr)
	}

	capture.serviceErr = nil
	missing := httptest.NewRecorder()
	handler.reportSummary(missing, reportsSummaryRequest("missing"))
	if missing.Code != http.StatusUnprocessableEntity || capture.serviceErr == nil {
		t.Fatalf("missing status=%d error=%v body=%s", missing.Code, capture.serviceErr, missing.Body.String())
	}
}

func TestReportsSummaryHandlerUsesDeclaredQueryAndRepeatedTagPredicates(t *testing.T) {
	report := reportmodel.ReportSchema{Key: "scoped", Dataset: reportmodel.ReportDatasetSchema{
		Source:          reportmodel.ReportDatasetSource{ObjectKey: "empty", Alias: "empty"},
		QueryPredicates: []reportmodel.ReportDatasetPredicate{{Key: "current", Filters: []reportmodel.ReportDatasetFilter{{Field: reportmodel.ReportDatasetField{SourceAlias: "empty", FieldKey: "status"}, Operator: "eq", Value: "current"}}}},
		TagPredicates: []reportmodel.ReportDatasetPredicate{
			{Key: "reviewed", Filters: []reportmodel.ReportDatasetFilter{{Field: reportmodel.ReportDatasetField{SourceAlias: "empty", FieldKey: "reviewed_at"}, Operator: "not_null"}}},
			{Key: "high", Filters: []reportmodel.ReportDatasetFilter{{Field: reportmodel.ReportDatasetField{SourceAlias: "empty", FieldKey: "amount"}, Operator: "gte", Value: 100}}},
		},
	}}
	capture := &reportsHandlerCapture{}
	handler := newReportsSummaryHandler([]reportmodel.ReportSchema{report}, capture)
	request := httptest.NewRequest(http.MethodGet, "/reports/scoped/summary?query_key=current&tags=reviewed&tags=high", nil)
	request.SetPathValue("reportKey", "scoped")
	response := httptest.NewRecorder()
	handler.reportSummary(response, request)
	if response.Code != http.StatusOK || capture.serviceErr != nil {
		t.Fatalf("status=%d err=%v body=%s", response.Code, capture.serviceErr, response.Body.String())
	}

	request = httptest.NewRequest(http.MethodGet, "/reports/scoped/summary?query_key=unknown", nil)
	request.SetPathValue("reportKey", "scoped")
	response = httptest.NewRecorder()
	handler.reportSummary(response, request)
	if response.Code != http.StatusUnprocessableEntity || apperror.CodeOf(capture.serviceErr) != "backend.report.query_not_allowed" {
		t.Fatalf("unknown status=%d err=%v", response.Code, capture.serviceErr)
	}
	capture.serviceErr = nil
	request = httptest.NewRequest(http.MethodGet, "/reports/scoped/summary?mode=snapshot&tags=reviewed", nil)
	request.SetPathValue("reportKey", "scoped")
	handler.reportSummary(httptest.NewRecorder(), request)
	if apperror.CodeOf(capture.serviceErr) != "backend.report.snapshot_scope_unsupported" {
		t.Fatalf("snapshot err=%v", capture.serviceErr)
	}
}

func TestReportsRefreshSnapshotWritesReplayAndMapsStoreFailure(t *testing.T) {
	request := func() *http.Request {
		result := httptest.NewRequest(http.MethodPost, "/reports/revenue/snapshot/refresh", nil)
		result.SetPathValue("reportKey", " revenue ")
		result.Header.Set("Idempotency-Key", " refresh-1 ")
		return result
	}

	capture := &reportsHandlerCapture{}
	handler := newReportsSnapshotHandler(reportsSnapshotReplayStore{snapshot: reportmodel.ReportSnapshot{ID: "snapshot-1", Status: "succeeded"}}, capture)
	response := httptest.NewRecorder()
	handler.refreshReportSnapshot(response, request())
	if response.Code != http.StatusOK || capture.serviceErr != nil || !strings.Contains(response.Body.String(), `"id":"snapshot-1"`) {
		t.Fatalf("success status=%d error=%v body=%s", response.Code, capture.serviceErr, response.Body.String())
	}

	want := errors.New("snapshot store unavailable")
	capture = &reportsHandlerCapture{}
	handler = newReportsSnapshotHandler(reportsSnapshotReplayStore{err: want}, capture)
	response = httptest.NewRecorder()
	handler.refreshReportSnapshot(response, request())
	if response.Code != http.StatusUnprocessableEntity || !errors.Is(capture.serviceErr, want) || response.Body.Len() != 0 {
		t.Fatalf("failure status=%d error=%v body=%s", response.Code, capture.serviceErr, response.Body.String())
	}
}

func TestReportsGovernedPrepareAndDownloadHandlers(t *testing.T) {
	now := time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "operator-1"}}, accessfixture.Bundle{Key: "admin", Permissions: []string{"workspace.admin", "*"}})
	store := &reportsExportRecordStore{audit: recordmodel.Record{ID: "audit-1", Data: map[string]any{"report_key": "revenue", "requested_by_identity_user_id": "operator-1", "status": "completed"}}}
	exchange := &reportsDataExchangeBinding{}
	providers := recordapplication.NewDataExchangeProviders(func(context.Context, string, string) principalmodel.Principal { return principal })
	domain := reportservice.NewReportDomainService(reportservice.ReportDependencies{Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
		return []reportmodel.ReportSchema{{Key: "revenue", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "customer", Alias: "customer"}, Dimensions: []reportmodel.ReportDatasetDimension{{Key: "id", Field: reportmodel.ReportDatasetField{SourceAlias: "customer", FieldKey: "id"}}}}}}
	}, Access: reportsEmptyAccess{}, Records: reportsOneRecords{}, SnapshotSources: reportsSnapshotReplayStore{}})
	service := reportapplication.NewReportApplicationService(reportapplication.ReportApplicationDependencies{
		Domain: domain, ExportRecords: store,
		Audit: &reportsAuditStub{}, DataExchange: exchange, DataExchangeProviders: providers,
		ExportControls: func(context.Context, principalmodel.Principal) []reportmodel.ReportExportControlSchema {
			return []reportmodel.ReportExportControlSchema{{ReportKey: "revenue", SourceObjects: []string{"customer"}, AuditObject: "report_export_audit", DownloadObject: "report_export_download", MaxRows: 1000, RecordMapping: reportmodel.ReportExportRecordMappingSchema{AuditReportKeyField: "report_key", AuditRequesterField: "requested_by_identity_user_id", AuditStatusField: "status", AuditPreparedStatuses: []string{"completed"}, AuditPreparedStatus: "completed", AuditDownloadedStatus: "completed", AuditDeniedStatus: "denied", AuditExpiredStatus: "expired", AuditRowCountField: "row_count", AuditScopeHashField: "filters_hash", DownloadAuditField: "audit_id", DownloadFilenameField: "file_name", DownloadContentHashField: "content_hash", DownloadExpiresAtField: "expires_at", DownloadTokenField: "file_reference"}}}
		},
		Clock: func() time.Time { return now },
	})
	capture := &reportsHandlerCapture{}
	handler := NewReportsHandler(ReportsDependencies{Service: service,
		Principal: func(*http.Request) principalmodel.Principal { return principal },
		WriteJSON: func(w http.ResponseWriter, status int, value any) {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		},
		WriteServiceError: func(w http.ResponseWriter, _ *http.Request, err error) {
			capture.serviceErr = err
			w.WriteHeader(http.StatusUnprocessableEntity)
		},
	})
	invalid := httptest.NewRequest(http.MethodPost, "/reports/revenue/exports/customer", strings.NewReader(`{`))
	invalid.SetPathValue("reportKey", "revenue")
	invalid.SetPathValue("objectKey", "customer")
	handler.prepareReportExport(httptest.NewRecorder(), invalid)
	if capture.serviceErr == nil {
		t.Fatal("invalid export request was accepted")
	}
	capture.serviceErr = nil
	missingAudit := httptest.NewRequest(http.MethodPost, "/reports/revenue/exports/customer", strings.NewReader(`{"audit_id":"audit-1"}`))
	missingAudit.SetPathValue("reportKey", "revenue")
	missingAudit.SetPathValue("objectKey", "customer")
	handler.prepareReportExport(httptest.NewRecorder(), missingAudit)
	if capture.serviceErr == nil {
		t.Fatal("missing governed export idempotency key was accepted")
	}
	capture.serviceErr = nil
	missingScope := httptest.NewRequest(http.MethodPost, "/reports/revenue/exports/customer", strings.NewReader(`{"audit_id":"audit-1"}`))
	missingScope.SetPathValue("reportKey", "revenue")
	missingScope.SetPathValue("objectKey", "customer")
	missingScope.Header.Set("Idempotency-Key", "prepare-without-scope")
	handler.prepareReportExport(httptest.NewRecorder(), missingScope)
	if apperror.CodeOf(capture.serviceErr) != "backend.report.export_scope_invalid" {
		t.Fatalf("missing governed export scope error=%v", capture.serviceErr)
	}
	capture.serviceErr = nil
	valid := httptest.NewRequest(http.MethodPost, "/reports/revenue/exports/customer", strings.NewReader(`{"audit_id":"audit-1","scope":{"tag_match":"all","field_projection":["id"],"purpose":"browser export","freshness":{"mode":"realtime"}}}`))
	valid.SetPathValue("reportKey", "revenue")
	valid.SetPathValue("objectKey", "customer")
	valid.Header.Set("Idempotency-Key", "prepare-1")
	validResponse := httptest.NewRecorder()
	handler.prepareReportExport(validResponse, valid)
	if validResponse.Code != http.StatusAccepted || validResponse.Header().Get("Location") != "/report-exports/data_exchange:report-1" {
		t.Fatalf("prepare status=%d headers=%v body=%s error=%v", validResponse.Code, validResponse.Header(), validResponse.Body.String(), capture.serviceErr)
	}
	status := httptest.NewRequest(http.MethodGet, "/report-exports/data_exchange:report-1", nil)
	status.SetPathValue("jobID", "data_exchange:report-1")
	statusResponse := httptest.NewRecorder()
	handler.getReportExportJob(statusResponse, status)
	if statusResponse.Code != http.StatusOK || !strings.Contains(statusResponse.Body.String(), `"status":"accepted"`) {
		t.Fatalf("status=%d body=%s error=%v", statusResponse.Code, statusResponse.Body.String(), capture.serviceErr)
	}
	cancel := httptest.NewRequest(http.MethodPost, "/report-exports/data_exchange:report-1/cancel", nil)
	cancel.SetPathValue("jobID", "data_exchange:report-1")
	cancelResponse := httptest.NewRecorder()
	handler.cancelReportExportJob(cancelResponse, cancel)
	if cancelResponse.Code != http.StatusOK || !strings.Contains(cancelResponse.Body.String(), `"status":"cancelled"`) {
		t.Fatalf("cancel status=%d body=%s error=%v", cancelResponse.Code, cancelResponse.Body.String(), capture.serviceErr)
	}
}

func TestReportsRoutesBindMethodsAndPaths(t *testing.T) {
	handler := newReportsSummaryHandler([]reportmodel.ReportSchema{{Key: "revenue", Name: "Revenue", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "empty", Alias: "empty"}}}}, &reportsHandlerCapture{})
	mux := http.NewServeMux()
	handler.RegisterRoutes(mux)

	response := httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/reports/revenue/summary", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("summary route status=%d body=%s", response.Code, response.Body.String())
	}

	response = httptest.NewRecorder()
	mux.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/reports/revenue/summary", nil))
	if response.Code != http.StatusMethodNotAllowed {
		t.Fatalf("summary method status=%d", response.Code)
	}
}
