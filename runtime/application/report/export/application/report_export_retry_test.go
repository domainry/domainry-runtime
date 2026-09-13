package application

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

type retryExchange struct {
	reportDataExchangeBindingProbe
	jobsMu sync.Mutex
	jobs   map[string]dataexchange.Job
	keys   map[string]string
}

func (p *retryExchange) SubmitExport(_ context.Context, request dataexchange.ExportRequest) (dataexchange.Job, bool, error) {
	p.jobsMu.Lock()
	defer p.jobsMu.Unlock()
	if id := p.keys[request.IdempotencyKey]; id != "" {
		return p.jobs[id], true, nil
	}
	id := fmt.Sprintf("job-%d", len(p.jobs)+1)
	job := dataexchange.Job{ID: id, Provider: request.Provider, Operation: "export", Status: "queued", WorkspaceID: request.Scope.WorkspaceID, ActorID: request.Scope.ActorID, RoleKey: request.Scope.RoleKey, ObjectKey: request.ObjectKey, ReferenceID: request.ReferenceID, Options: append([]byte(nil), request.Options...)}
	p.jobs[id], p.keys[request.IdempotencyKey] = job, id
	return job, false, nil
}

func (p *retryExchange) Job(_ context.Context, request dataexchange.JobRequest) (dataexchange.Job, error) {
	p.jobsMu.Lock()
	defer p.jobsMu.Unlock()
	job, found := p.jobs[request.JobID]
	if !found || job.WorkspaceID != request.Scope.WorkspaceID || job.ActorID != request.Scope.ActorID {
		return dataexchange.Job{}, dataexchange.ErrJobNotFound
	}
	return job, nil
}

func TestReportExportRetryCreatesOneFreshAttemptAndPreservesFailedJob(t *testing.T) {
	principal := reportPrincipal()
	control := reportExportLifecycleControl()
	definition := reportmodel.ReportSchema{Key: "revenue", ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{
		SQL: "SELECT c.id AS id FROM customer c ORDER BY c.id LIMIT 2000", SourceObjects: []string{"customer"},
		ResultSchema: []reportmodel.ReportResultColumnSchema{{Key: "id", Type: "text", Kind: "dimension"}},
	}}
	records := &reportExportStoreStub{audit: recordmodel.Record{ID: "audit-1", Data: map[string]any{"report_key": "revenue", "requested_by_identity_user_id": principal.UserID, "status": "approved"}}}
	exchange := &retryExchange{jobs: map[string]dataexchange.Job{}, keys: map[string]string{}}
	providers := recordapplication.NewDataExchangeProviders(func(context.Context, string, string) principalmodel.Principal { return principal })
	service := NewReportExportApplicationService(ReportExportApplicationDependencies{Records: records, Audit: &reportAuditAppenderStub{}, DataExchange: exchange, DataExchangeProviders: providers, PrepareReceipts: newReportPrepareReceiptStore(t)})
	owner := &reportOwnerExportsStub{definition: reportmodel.ReportExportDefinition{Report: definition, Control: control}}
	if err := service.BindReportExports(owner); err != nil {
		t.Fatal(err)
	}
	request := reportmodel.ReportExportPrepareRequest{ReportKey: "revenue", ObjectKey: "customer", AuditID: "audit-1", IdempotencyKey: "initial", Scope: reportmodel.ReportExportScopeRequest{FieldProjection: []string{"id"}, Purpose: "retry test", Freshness: reportmodel.ReportExportFreshness{Mode: "realtime"}}}
	first, err := service.PrepareResolvedExport(t.Context(), request, definition, control, principal)
	if err != nil {
		t.Fatal(err)
	}
	retry := request
	retry.IdempotencyKey, retry.RetryOfJobID = "retry", first.ID
	if _, err := service.PrepareResolvedExport(t.Context(), retry, definition, control, principal); err == nil {
		t.Fatal("queued job accepted as a failed predecessor")
	}
	failed := exchange.jobs[first.ID]
	failed.Status, failed.ErrorCode = "failed", "backend.report.export_scope_changed"
	exchange.jobs[first.ID] = failed
	for _, kind := range []string{"actor", "workspace", "audit", "report", "object"} {
		t.Run(kind, func(t *testing.T) {
			other, attempted := principal, retry
			switch kind {
			case "actor":
				other.UserID = "other"
			case "workspace":
				other.WorkspaceID = "other"
			case "audit":
				attempted.AuditID = "other"
			case "report":
				attempted.ReportKey = "other"
			case "object":
				attempted.ObjectKey = "other"
			}
			if err := service.validateReportExportRetry(t.Context(), attempted, other); err == nil {
				t.Fatal("mismatched predecessor accepted")
			}
		})
	}
	owner.sourceVersion = "after-original-failure"
	second, err := service.PrepareResolvedExport(t.Context(), retry, definition, control, principal)
	if err != nil || second.ID == first.ID || len(exchange.jobs) != 2 {
		t.Fatalf("new attempt=%+v err=%v jobs=%d", second, err, len(exchange.jobs))
	}
	if !reflect.DeepEqual(exchange.jobs[first.ID], failed) {
		t.Fatal("old failure evidence was rewritten")
	}
	if reflect.DeepEqual(exchange.jobs[second.ID].Options, failed.Options) {
		t.Fatal("new attempt reused the old frozen payload")
	}
	replayed, err := service.PrepareResolvedExport(t.Context(), retry, definition, control, principal)
	if err != nil || replayed.ID != second.ID || len(exchange.jobs) != 2 {
		t.Fatalf("retry replay=%+v err=%v", replayed, err)
	}
	retry.IdempotencyKey = "competing-caller"
	if _, err := service.PrepareResolvedExport(t.Context(), retry, definition, control, principal); err == nil || len(exchange.jobs) != 2 {
		t.Fatal("second successor for one failed job was accepted")
	}
	request.IdempotencyKey = "new-key-without-lineage"
	if _, err := service.PrepareResolvedExport(t.Context(), request, definition, control, principal); err == nil {
		t.Fatal("new caller bypassed explicit retry lineage")
	}
}
