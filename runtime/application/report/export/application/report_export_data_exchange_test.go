package application

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	reportexport "github.com/domainry/domainry-runtime/runtime/application/report/export"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportservice "github.com/domainry/domainry-runtime/runtime/domain/report/query"
)

type reportDataExchangeBindingProbe struct {
	job        dataexchange.Job
	submission dataexchange.ExportRequest
	artifact   dataexchange.Artifact
	content    string
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
	return p.job, nil
}
func (p *reportDataExchangeBindingProbe) Cancel(context.Context, dataexchange.JobRequest) (dataexchange.Job, error) {
	p.job.Status = "cancelled"
	return p.job, nil
}
func (p *reportDataExchangeBindingProbe) Download(context.Context, dataexchange.JobRequest) (dataexchange.Artifact, error) {
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
	domain := reportservice.NewReportDomainService(reportservice.ReportDependencies{
		Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
			return []reportmodel.ReportSchema{reportDefinition}
		},
		Access: reportExportDatasetAccessStub{objects: map[string]definitionmodel.ObjectSchema{
			"customer": {Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "id", Type: "text"}}},
		}},
		ObjectSQL: &reportExportObjectSQLExecutorStub{rows: []map[string]string{{"id": "customer-1"}}}, SnapshotSources: reportSnapshotSourceStub{},
	})
	exchange := &reportDataExchangeBindingProbe{}
	providers := recordapplication.NewDataExchangeProviders(func(context.Context, string, string) principalmodel.Principal { return principal })
	service := NewReportExportApplicationService(ReportExportApplicationDependencies{
		Domain: domain, Records: store, Audit: &reportAuditAppenderStub{}, DataExchange: exchange, DataExchangeProviders: providers,
		Controls: func(context.Context, principalmodel.Principal) []reportmodel.ReportExportControlSchema {
			return []reportmodel.ReportExportControlSchema{control}
		},
		Clock: func() time.Time { return now },
	})
	payload, err := service.prepareReportExportPayload(t.Context(), "revenue", "customer", "audit-1", "request-1", reportmodel.ReportExportScopeRequest{FieldProjection: []string{"id"}, Purpose: "provider test", Freshness: reportmodel.ReportExportFreshness{Mode: "realtime"}}, principal)
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
	if store.download.Data[control.RecordMapping.DownloadTokenField] != "data_exchange:report" || store.download.Data[control.RecordMapping.DownloadContentHashField] != "sha256:"+payload.CSVContentSHA256 {
		t.Fatalf("download=%+v", store.download)
	}
}

var _ dataexchange.Binding = (*reportDataExchangeBindingProbe)(nil)
