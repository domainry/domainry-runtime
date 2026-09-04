package application

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	"github.com/domainry/domainry-foundation/modulecapability"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	reportsdk "github.com/domainry/domainry-report-sdk"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	reportexport "github.com/domainry/domainry-runtime/runtime/application/report/export"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

type reportDataExchangeBindingProbe struct {
	modulecapability.Binding
	job        dataexchange.Job
	submission dataexchange.ExportRequest
	artifact   dataexchange.Artifact
	content    string
	jobCalls   int
	downloads  int
}

type reportOwnerExportsStub struct {
	definition                            reportmodel.ReportExportDefinition
	resolveCalls, readCalls, versionCalls int
}

func (*reportOwnerExportsStub) Prepare(context.Context, reportmodel.ReportExportPrepareRequest, reportmodel.ReportAuthority) (reportmodel.ReportExportJob, error) {
	return reportmodel.ReportExportJob{}, nil
}

func (s *reportOwnerExportsStub) ResolveExecution(_ context.Context, request reportmodel.ReportExportExecutionRequest, authority reportmodel.ReportAuthority) (reportmodel.ReportExportExecution, error) {
	s.resolveCalls++
	return reportmodel.ReportExportExecution{Definition: s.definition, Scope: request.Scope}, nil
}

func (s *reportOwnerExportsStub) ReadPage(context.Context, reportmodel.ReportExportExecutionRequest, reportmodel.ReportAuthority) (reportmodel.ReportSummary, error) {
	s.readCalls++
	return reportmodel.ReportSummary{Rows: []reportmodel.ReportResultRow{{Dimensions: map[string]string{"id": "customer-1"}}}, RowCount: 1, Total: 1, TotalSemantics: reportmodel.ReportTotalExact}, nil
}

func (s *reportOwnerExportsStub) SourceVersion(context.Context, reportmodel.ReportExportExecutionRequest, reportmodel.ReportAuthority) (reportmodel.ReportSnapshotSourceVersion, error) {
	s.versionCalls++
	return reportmodel.ReportSnapshotSourceVersion{Watermark: "version-1", SourceVersions: map[string]string{"customer": "version-1"}}, nil
}

func (*reportDataExchangeBindingProbe) Descriptor() dataexchange.Descriptor {
	return dataexchange.Descriptor{ProtocolVersion: dataexchange.ProtocolVersionV1, Mode: dataexchange.DeploymentModeModule}
}
func (*reportDataExchangeBindingProbe) SubmitImport(context.Context, dataexchange.ImportRequest) (dataexchange.Job, bool, error) {
	return dataexchange.Job{}, false, nil
}
func (p *reportDataExchangeBindingProbe) SubmitExport(_ context.Context, request dataexchange.ExportRequest) (dataexchange.Job, bool, error) {
	p.submission = request
	now := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	p.job = dataexchange.Job{ID: "data_exchange:report", Provider: request.Provider, Operation: "export", Status: "queued", WorkspaceID: request.Scope.WorkspaceID, ObjectKey: request.ObjectKey, ActorID: request.Scope.ActorID, RoleKey: request.Scope.RoleKey, ReferenceID: request.ReferenceID, Options: append([]byte(nil), request.Options...), CreatedAt: now, UpdatedAt: now}
	return p.job, false, nil
}
func (p *reportDataExchangeBindingProbe) Job(context.Context, dataexchange.JobRequest) (dataexchange.Job, error) {
	p.jobCalls++
	return p.job, nil
}
func (p *reportDataExchangeBindingProbe) Cancel(context.Context, dataexchange.JobRequest) (dataexchange.Job, error) {
	p.job.Status = "cancelled"
	return p.job, nil
}
func (p *reportDataExchangeBindingProbe) Download(context.Context, dataexchange.JobRequest) (dataexchange.Artifact, error) {
	p.downloads++
	artifact := p.artifact
	artifact.Content = io.NopCloser(strings.NewReader(p.content))
	return artifact, nil
}
func (*reportDataExchangeBindingProbe) Start(context.Context, dataexchange.WorkerConfig) <-chan struct{} {
	return workerplatform.Stopped()
}
func (*reportDataExchangeBindingProbe) Close(context.Context) error { return nil }

func TestNewReportExportJobWritesOnlyDataExchange(t *testing.T) {
	principal := reportPrincipal()
	exchange := &reportDataExchangeBindingProbe{}
	service := &ReportExportApplicationService{dataExchange: exchange, dataExchangeProvider: reportexport.NewDataExchangeProvider(reportexport.DataExchangeDependencies{Binding: exchange}), clock: time.Now}
	payload := reportexport.ExportPayload{WorkspaceID: principal.WorkspaceID, RequesterUserID: principal.UserID, ReportKey: "revenue", ObjectKey: "customer", AuditID: "audit-1", Format: "csv", ExactTotal: 1001}
	job, err := service.prepareExportJobFromPayload(t.Context(), payload, reportExportRequestFingerprint(payload), principal)
	if err != nil {
		t.Fatal(err)
	}
	if job.ID != "data_exchange:report" || exchange.submission.Provider != reportexport.DataExchangeProviderKey || exchange.submission.ReferenceID != "audit-1" || exchange.submission.ObjectKey != "customer" {
		t.Fatalf("job=%+v submission=%+v", job, exchange.submission)
	}
	if strings.Contains(string(exchange.submission.Options), `"audit_id":"audit-1"`) {
		t.Fatalf("audit reference leaked into canonical options: %s", exchange.submission.Options)
	}
}

func TestReportDataExchangeProviderPagesAndFinalizesBusinessProjection(t *testing.T) {
	now := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	principal := reportPrincipal()
	store := &reportExportStoreStub{audit: recordmodel.Record{ID: "audit-1", Data: map[string]any{
		"report_key": "revenue", "requested_by_identity_user_id": principal.UserID, "status": "completed",
	}}}
	control := reportExportEdgeControl()
	reportDefinition := reportmodel.ReportSchema{Key: "revenue", ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{
		SQL: "SELECT c.id AS id FROM customer c ORDER BY c.id LIMIT 2000", SourceObjects: []string{"customer"},
		ResultSchema: []reportmodel.ReportResultColumnSchema{{Key: "id", Type: "text", Kind: "dimension"}},
	}}
	exchange := &reportDataExchangeBindingProbe{}
	providers := recordapplication.NewDataExchangeProviders(func(context.Context, string, string) principalmodel.Principal { return principal })
	service := NewReportExportApplicationService(ReportExportApplicationDependencies{
		Records: store, Audit: &reportAuditAppenderStub{}, DataExchange: exchange, DataExchangeProviders: providers,
		Clock: func() time.Time { return now },
	})
	owner := &reportOwnerExportsStub{definition: reportmodel.ReportExportDefinition{Report: reportDefinition, Control: control}}
	if err := service.BindReportExports(owner); err != nil {
		t.Fatal(err)
	}
	payload, err := service.prepareReportExportPayloadResolved(t.Context(), reportDefinition, control, "customer", "audit-1", "request-1", reportmodel.ReportExportScopeRequest{FieldProjection: []string{"id"}, Purpose: "provider test", Freshness: reportmodel.ReportExportFreshness{Mode: "realtime"}}, principal)
	if err != nil {
		t.Fatal(err)
	}
	payload.ArtifactIdempotencyKey = "report-export-business:test"
	payload.AuditID = ""
	options, _ := json.Marshal(payload)
	scope := reportexport.Scope(principal)
	plan, err := service.dataExchangeProvider.PlanExport(t.Context(), dataexchange.ExportPlanRequest{Scope: scope, ObjectKey: "customer", ReferenceID: "audit-1", Options: options, JobID: "data_exchange:report", CreatedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	page, err := service.dataExchangeProvider.ReadExportPage(t.Context(), dataexchange.ExportPageRequest{Scope: scope, ObjectKey: "customer", ReferenceID: "audit-1", Options: options, JobID: "data_exchange:report", PageSize: 500, ArtifactExpiresAt: plan.ExpiresAt})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Columns) != 1 || page.Columns[0] != "id" || len(page.Rows) != 1 || page.Rows[0][0] != "customer-1" || page.NextCursor != "" {
		t.Fatalf("page=%+v", page)
	}
	if err = service.dataExchangeProvider.CompleteExport(t.Context(), dataexchange.ExportCompletion{
		Scope: scope, ObjectKey: "customer", ReferenceID: "audit-1", Options: options, JobID: "data_exchange:report",
		Artifact: dataexchange.Artifact{ID: "data_exchange:report:artifact", Filename: plan.Filename, ContentType: plan.ContentType, SHA256: payload.CSVContentSHA256, Size: 14, ExpiresAt: plan.ExpiresAt},
		Rows:     1, ResultChunks: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if store.download.Data[control.RecordMapping.DownloadJobIDField] != "data_exchange:report" || store.download.Data[control.RecordMapping.DownloadContentHashField] != "sha256:"+payload.CSVContentSHA256 {
		t.Fatalf("download=%+v", store.download)
	}
	exchange.job = dataexchange.Job{
		ID: "data_exchange:report", Provider: reportexport.DataExchangeProviderKey, Operation: "export", Status: "completed",
		WorkspaceID: principal.WorkspaceID, ActorID: principal.UserID, ObjectKey: "customer", ReferenceID: "audit-1", Options: options,
		ArtifactID: "data_exchange:report:artifact", Checkpoint: 1, Total: 1, CreatedAt: now, UpdatedAt: now,
	}
	exchange.artifact = dataexchange.Artifact{ID: exchange.job.ArtifactID, Filename: plan.Filename, ContentType: plan.ContentType, SHA256: payload.CSVContentSHA256, ExpiresAt: plan.ExpiresAt}
	exchange.content = "id\ncustomer-1\n"
	opened, err := service.dataExchangeProvider.OpenDataExchangeArtifact(t.Context(), exchange.job, scope)
	if err != nil {
		t.Fatal(err)
	}
	content, err := io.ReadAll(opened.Content)
	if err != nil {
		t.Fatal(err)
	}
	_ = opened.Content.Close()
	if string(content) != exchange.content || opened.ID != exchange.artifact.ID || opened.SHA256 != exchange.artifact.SHA256 || opened.ExpiresAt != exchange.artifact.ExpiresAt {
		t.Fatalf("opened=%+v content=%q", opened, content)
	}
	if exchange.jobCalls != 0 || exchange.downloads != 1 {
		t.Fatalf("job lookups=%d artifact downloads=%d", exchange.jobCalls, exchange.downloads)
	}
	if owner.resolveCalls < 4 || owner.readCalls < 2 || owner.versionCalls < 4 {
		t.Fatalf("owner calls resolve=%d read=%d source-version=%d", owner.resolveCalls, owner.readCalls, owner.versionCalls)
	}
}

var _ dataexchange.Binding = (*reportDataExchangeBindingProbe)(nil)
var _ reportsdk.Exports = (*reportOwnerExportsStub)(nil)
