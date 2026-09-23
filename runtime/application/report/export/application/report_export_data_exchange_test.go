package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	"github.com/domainry/domainry-foundation/apperror"
	workerplatform "github.com/domainry/domainry-foundation/worker"
	reportsdk "github.com/domainry/domainry-report-sdk"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	reportexport "github.com/domainry/domainry-runtime/runtime/application/report/export"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	runtimereportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

type reportDataExchangeBindingProbe struct {
	mu         sync.Mutex
	job        dataexchange.Job
	submission dataexchange.ExportRequest
	artifact   dataexchange.Artifact
	content    string
	jobCalls   int
	downloads  int
	submits    int
	onSubmit   func(dataexchange.ExportRequest, dataexchange.Job) error
}

type reportOwnerExportsStub struct {
	mu                                    sync.Mutex
	definition                            reportmodel.ReportExportDefinition
	resolveCalls, readCalls, versionCalls int
	sourceVersion                         string
	versionStarted                        chan struct{}
	versionRelease                        <-chan struct{}
	versionStartOnce                      sync.Once
}

func (*reportOwnerExportsStub) Prepare(context.Context, reportmodel.ReportExportPrepareRequest, reportmodel.ReportAuthority) (reportmodel.ReportExportJob, error) {
	return reportmodel.ReportExportJob{}, nil
}

func (s *reportOwnerExportsStub) ResolveExecution(_ context.Context, request reportmodel.ReportExportExecutionRequest, authority reportmodel.ReportAuthority) (reportmodel.ReportExportExecution, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resolveCalls++
	return reportmodel.ReportExportExecution{Definition: s.definition, Scope: request.Scope}, nil
}

func (s *reportOwnerExportsStub) ReadPage(context.Context, reportmodel.ReportExportExecutionRequest, reportmodel.ReportAuthority) (reportmodel.ReportSummary, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.readCalls++
	return reportmodel.ReportSummary{Rows: []reportmodel.ReportResultRow{{Dimensions: map[string]string{"id": "customer-1"}}}, RowCount: 1, Total: 1, TotalSemantics: reportmodel.ReportTotalExact}, nil
}

func (s *reportOwnerExportsStub) SourceVersion(context.Context, reportmodel.ReportExportExecutionRequest, reportmodel.ReportAuthority) (reportmodel.ReportSnapshotSourceVersion, error) {
	s.mu.Lock()
	s.versionCalls++
	version := s.sourceVersion
	started, release := s.versionStarted, s.versionRelease
	s.mu.Unlock()
	if started != nil {
		s.versionStartOnce.Do(func() { close(started) })
	}
	if release != nil {
		<-release
	}
	if version == "" {
		version = "version-1"
	}
	return reportmodel.ReportSnapshotSourceVersion{Watermark: version, SourceVersions: map[string]string{"customer": version}}, nil
}

func (*reportDataExchangeBindingProbe) Descriptor() dataexchange.Descriptor {
	return dataexchange.Descriptor{ProtocolVersion: dataexchange.ProtocolVersionV1, Mode: dataexchange.DeploymentModeModule}
}
func (*reportDataExchangeBindingProbe) SubmitImport(context.Context, dataexchange.ImportRequest) (dataexchange.Job, bool, error) {
	return dataexchange.Job{}, false, nil
}
func (p *reportDataExchangeBindingProbe) SubmitExport(_ context.Context, request dataexchange.ExportRequest) (dataexchange.Job, bool, error) {
	p.mu.Lock()
	p.submits++
	if p.submits > 1 {
		if request.Provider != p.submission.Provider || request.ObjectKey != p.submission.ObjectKey || request.IdempotencyKey != p.submission.IdempotencyKey || request.ReferenceID != p.submission.ReferenceID || request.Scope != p.submission.Scope || !bytes.Equal(request.Options, p.submission.Options) {
			p.mu.Unlock()
			return dataexchange.Job{}, false, fmt.Errorf("submit report export: %w", dataexchange.ErrIdempotencyKeyReused)
		}
		job := p.job
		p.mu.Unlock()
		return job, true, nil
	}
	p.submission = request
	now := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	p.job = dataexchange.Job{ID: "data_exchange:report", Provider: request.Provider, Operation: "export", Status: "queued", WorkspaceID: request.Scope.WorkspaceID, ObjectKey: request.ObjectKey, ActorID: request.Scope.ActorID, RoleKey: request.Scope.RoleKey, ReferenceID: request.ReferenceID, Options: append([]byte(nil), request.Options...), CreatedAt: now, UpdatedAt: now}
	job, onSubmit := p.job, p.onSubmit
	p.mu.Unlock()
	if onSubmit != nil {
		if err := onSubmit(request, job); err != nil {
			return dataexchange.Job{}, false, err
		}
	}
	return job, false, nil
}
func (p *reportDataExchangeBindingProbe) Job(_ context.Context, request dataexchange.JobRequest) (dataexchange.Job, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.jobCalls++
	if request.JobID != p.job.ID || request.Scope.WorkspaceID != p.job.WorkspaceID || request.Scope.ActorID != p.job.ActorID || (request.Provider != "" && request.Provider != p.job.Provider) || (request.Operation != "" && request.Operation != p.job.Operation) {
		return dataexchange.Job{}, dataexchange.ErrJobNotFound
	}
	return p.job, nil
}
func (p *reportDataExchangeBindingProbe) Cancel(context.Context, dataexchange.JobRequest) (dataexchange.Job, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.job.Status = "cancelled"
	return p.job, nil
}
func (p *reportDataExchangeBindingProbe) Download(context.Context, dataexchange.JobRequest) (dataexchange.Artifact, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.downloads++
	artifact := p.artifact
	artifact.Content = io.NopCloser(strings.NewReader(p.content))
	return artifact, nil
}
func (*reportDataExchangeBindingProbe) Start(context.Context, dataexchange.WorkerConfig) <-chan struct{} {
	return workerplatform.Stopped()
}
func (*reportDataExchangeBindingProbe) Close(context.Context) error { return nil }

func TestConcurrentReportExportPrepareClaimsBeforeMutableProbe(t *testing.T) {
	principal := reportPrincipal()
	control := reportExportLifecycleControl()
	reportDefinition := reportmodel.ReportSchema{Key: "revenue", ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{
		SQL: "SELECT c.id AS id FROM customer c ORDER BY c.id LIMIT 2000", SourceObjects: []string{"customer"},
		ResultSchema: []reportmodel.ReportResultColumnSchema{{Key: "id", Type: "text", Kind: "dimension"}},
	}}
	store := &reportExportStoreStub{audit: recordmodel.Record{ID: "audit-1", Data: map[string]any{
		"report_key": "revenue", "requested_by_identity_user_id": principal.UserID, "status": "approved",
	}}}
	exchange := &reportDataExchangeBindingProbe{}
	providers := recordapplication.NewDataExchangeProviders(func(context.Context, string, string) principalmodel.Principal { return principal })
	service := NewReportExportApplicationService(ReportExportApplicationDependencies{
		Records: store, Audit: &reportAuditAppenderStub{}, DataExchange: exchange, DataExchangeProviders: providers, PrepareReceipts: newReportPrepareReceiptStore(t),
	})
	started, release := make(chan struct{}), make(chan struct{})
	owner := &reportOwnerExportsStub{definition: reportmodel.ReportExportDefinition{Report: reportDefinition, Control: control}, versionStarted: started, versionRelease: release}
	if err := service.BindReportExports(owner); err != nil {
		t.Fatal(err)
	}
	request := reportmodel.ReportExportPrepareRequest{
		ReportKey: "revenue", ObjectKey: "customer", AuditID: "audit-1", IdempotencyKey: "request-1",
		Scope: reportmodel.ReportExportScopeRequest{FieldProjection: []string{"id"}, Purpose: "concurrent test", Freshness: reportmodel.ReportExportFreshness{Mode: "realtime"}},
	}
	type outcome struct {
		job reportexport.ExchangeJob
		err error
	}
	first := make(chan outcome, 1)
	go func() {
		job, err := service.PrepareResolvedExport(t.Context(), request, reportDefinition, control, principal)
		first <- outcome{job: job, err: err}
	}()
	<-started
	secondJob, secondErr := service.PrepareResolvedExport(t.Context(), request, reportDefinition, control, principal)
	if apperror.CodeOf(secondErr) != "backend.idempotency.in_progress" || secondJob.ID != "" {
		t.Fatalf("concurrent loser job=%+v err=%v code=%q", secondJob, secondErr, apperror.CodeOf(secondErr))
	}
	close(release)
	winner := <-first
	if winner.err != nil || winner.job.ID == "" {
		t.Fatalf("winner=%+v err=%v", winner.job, winner.err)
	}
	exchange.mu.Lock()
	submits := exchange.submits
	exchange.mu.Unlock()
	owner.mu.Lock()
	reads, versions := owner.readCalls, owner.versionCalls
	owner.mu.Unlock()
	if submits != 1 || reads != 1 || versions != 2 {
		t.Fatalf("submits=%d reads=%d versions=%d", submits, reads, versions)
	}
}

func TestConcurrentReportExportPrepareDifferentCallerHasStableAuditLoser(t *testing.T) {
	principal := reportPrincipal()
	control := reportExportLifecycleControl()
	reportDefinition := reportmodel.ReportSchema{Key: "revenue", ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{
		SQL: "SELECT c.id AS id FROM customer c ORDER BY c.id LIMIT 2000", SourceObjects: []string{"customer"},
		ResultSchema: []reportmodel.ReportResultColumnSchema{{Key: "id", Type: "text", Kind: "dimension"}},
	}}
	store := &reportExportStoreStub{audit: recordmodel.Record{ID: "audit-1", Data: map[string]any{
		"report_key": "revenue", "requested_by_identity_user_id": principal.UserID, "status": "approved",
	}}}
	exchange := &reportDataExchangeBindingProbe{}
	providers := recordapplication.NewDataExchangeProviders(func(context.Context, string, string) principalmodel.Principal { return principal })
	service := NewReportExportApplicationService(ReportExportApplicationDependencies{
		Records: store, Audit: &reportAuditAppenderStub{}, DataExchange: exchange, DataExchangeProviders: providers, PrepareReceipts: newReportPrepareReceiptStore(t),
	})
	started, release := make(chan struct{}), make(chan struct{})
	owner := &reportOwnerExportsStub{definition: reportmodel.ReportExportDefinition{Report: reportDefinition, Control: control}, versionStarted: started, versionRelease: release}
	if err := service.BindReportExports(owner); err != nil {
		t.Fatal(err)
	}
	request := reportmodel.ReportExportPrepareRequest{
		ReportKey: "revenue", ObjectKey: "customer", AuditID: "audit-1", IdempotencyKey: "request-1",
		Scope: reportmodel.ReportExportScopeRequest{FieldProjection: []string{"id"}, Purpose: "concurrent audit winner", Freshness: reportmodel.ReportExportFreshness{Mode: "realtime"}},
	}
	type outcome struct {
		job reportexport.ExchangeJob
		err error
	}
	first := make(chan outcome, 1)
	go func() {
		job, err := service.PrepareResolvedExport(t.Context(), request, reportDefinition, control, principal)
		first <- outcome{job: job, err: err}
	}()
	<-started
	loserRequest := request
	loserRequest.IdempotencyKey = "request-2"
	loserJob, loserErr := service.PrepareResolvedExport(t.Context(), loserRequest, reportDefinition, control, principal)
	if apperror.CodeOf(loserErr) != "backend.report.export_audit_operation_conflict" || loserJob.ID != "" {
		t.Fatalf("concurrent audit loser job=%+v err=%v code=%q", loserJob, loserErr, apperror.CodeOf(loserErr))
	}
	close(release)
	winner := <-first
	if winner.err != nil || winner.job.ID == "" {
		t.Fatalf("winner=%+v err=%v", winner.job, winner.err)
	}
	exchange.mu.Lock()
	submits := exchange.submits
	exchange.mu.Unlock()
	owner.mu.Lock()
	reads, versions := owner.readCalls, owner.versionCalls
	owner.mu.Unlock()
	if submits != 1 || reads != 1 || versions != 2 {
		t.Fatalf("submits=%d reads=%d versions=%d", submits, reads, versions)
	}
}

func TestReportExportWorkerCompletionMayWinBeforeSubmitReceiptCompletion(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	principal := reportPrincipal()
	control := reportExportLifecycleControl()
	reportDefinition := reportmodel.ReportSchema{Key: "revenue", ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{
		SQL: "SELECT c.id AS id FROM customer c ORDER BY c.id LIMIT 2000", SourceObjects: []string{"customer"},
		ResultSchema: []reportmodel.ReportResultColumnSchema{{Key: "id", Type: "text", Kind: "dimension"}},
	}}
	store := &reportExportStoreStub{audit: recordmodel.Record{ID: "audit-1", Data: map[string]any{
		"report_key": "revenue", "requested_by_identity_user_id": principal.UserID, "status": "approved",
	}}}
	receipts := newReportPrepareReceiptStore(t)
	exchange := &reportDataExchangeBindingProbe{}
	providers := recordapplication.NewDataExchangeProviders(func(context.Context, string, string) principalmodel.Principal { return principal })
	service := NewReportExportApplicationService(ReportExportApplicationDependencies{
		Records: store, Audit: &reportAuditAppenderStub{}, DataExchange: exchange, DataExchangeProviders: providers, PrepareReceipts: receipts,
		Clock: func() time.Time { return now },
	})
	owner := &reportOwnerExportsStub{definition: reportmodel.ReportExportDefinition{Report: reportDefinition, Control: control}}
	if err := service.BindReportExports(owner); err != nil {
		t.Fatal(err)
	}
	exchange.onSubmit = func(request dataexchange.ExportRequest, job dataexchange.Job) error {
		var payload reportexport.ExportPayload
		if err := json.Unmarshal(request.Options, &payload); err != nil {
			return err
		}
		return service.dataExchangeProvider.CompleteExport(t.Context(), dataexchange.ExportCompletion{
			Scope: request.Scope, ObjectKey: request.ObjectKey, ReferenceID: request.ReferenceID, Options: request.Options, JobID: job.ID,
			Artifact: dataexchange.Artifact{ID: job.ID + ":artifact", Filename: "revenue.csv", ContentType: "text/csv; charset=utf-8", SHA256: payload.CSVContentSHA256, Size: 14, ExpiresAt: now.Add(10 * time.Minute)},
			Rows:     payload.ExactTotal, ResultChunks: 1,
		})
	}
	job, err := service.PrepareResolvedExport(t.Context(), reportmodel.ReportExportPrepareRequest{
		ReportKey: "revenue", ObjectKey: "customer", AuditID: "audit-1", IdempotencyKey: "request-1",
		Scope: reportmodel.ReportExportScopeRequest{FieldProjection: []string{"id"}, Purpose: "worker race", Freshness: reportmodel.ReportExportFreshness{Mode: "realtime"}},
	}, reportDefinition, control, principal)
	if err != nil || job.ID != "data_exchange:report" {
		t.Fatalf("job=%+v err=%v", job, err)
	}
	if status := fmt.Sprint(store.audit.Data[control.RecordMapping.AuditStatusField]); status != "prepared" {
		t.Fatalf("worker-first completion status=%q", status)
	}
	var payload reportexport.ExportPayload
	if err = json.Unmarshal(exchange.job.Options, &payload); err != nil {
		t.Fatal(err)
	}
	receipt, found, err := receipts.GetReportExportPrepareReceipt(t.Context(), principal.WorkspaceID, payload.PrepareReceiptID)
	if err != nil || !found || receipt.JobID != job.ID || receipt.CompletionArtifactID != job.ID+":artifact" {
		t.Fatalf("receipt=%+v found=%v err=%v", receipt, found, err)
	}
}

func TestReportExportRetryAfterUncertainSubmitUsesFrozenPayloadWithoutProbe(t *testing.T) {
	principal := reportPrincipal()
	control := reportExportLifecycleControl()
	reportDefinition := reportmodel.ReportSchema{Key: "revenue", ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{
		SQL: "SELECT c.id AS id FROM customer c ORDER BY c.id LIMIT 2000", SourceObjects: []string{"customer"},
		ResultSchema: []reportmodel.ReportResultColumnSchema{{Key: "id", Type: "text", Kind: "dimension"}},
	}}
	store := &reportExportStoreStub{audit: recordmodel.Record{ID: "audit-1", Data: map[string]any{
		"report_key": "revenue", "requested_by_identity_user_id": principal.UserID, "status": "approved",
	}}}
	exchange := &reportDataExchangeBindingProbe{}
	exchange.onSubmit = func(dataexchange.ExportRequest, dataexchange.Job) error { return fmt.Errorf("submit response lost") }
	providers := recordapplication.NewDataExchangeProviders(func(context.Context, string, string) principalmodel.Principal { return principal })
	receipts := newReportPrepareReceiptStore(t)
	service := NewReportExportApplicationService(ReportExportApplicationDependencies{
		Records: store, Audit: &reportAuditAppenderStub{}, DataExchange: exchange, DataExchangeProviders: providers, PrepareReceipts: receipts,
	})
	owner := &reportOwnerExportsStub{definition: reportmodel.ReportExportDefinition{Report: reportDefinition, Control: control}}
	if err := service.BindReportExports(owner); err != nil {
		t.Fatal(err)
	}
	request := reportmodel.ReportExportPrepareRequest{
		ReportKey: "revenue", ObjectKey: "customer", AuditID: "audit-1", IdempotencyKey: "request-1",
		Scope: reportmodel.ReportExportScopeRequest{FieldProjection: []string{"id"}, Purpose: "uncertain submit", Freshness: reportmodel.ReportExportFreshness{Mode: "realtime"}},
	}
	if _, err := service.PrepareResolvedExport(t.Context(), request, reportDefinition, control, principal); err == nil || !strings.Contains(err.Error(), "submit response lost") {
		t.Fatalf("first submit error=%v", err)
	}
	var frozen reportexport.ExportPayload
	if err := json.Unmarshal(exchange.submission.Options, &frozen); err != nil {
		t.Fatal(err)
	}
	receipt, found, err := receipts.GetReportExportPrepareReceipt(t.Context(), principal.WorkspaceID, frozen.PrepareReceiptID)
	if err != nil || !found || receipt.Status != "failed_retryable" || strings.TrimSpace(receipt.ExpiresAt) == "" {
		t.Fatalf("retained retryable receipt=%+v found=%v err=%v", receipt, found, err)
	}
	readCalls, versionCalls := owner.readCalls, owner.versionCalls
	owner.sourceVersion = "version-after-submit"
	job, err := service.PrepareResolvedExport(t.Context(), request, reportDefinition, control, principal)
	if err != nil || job.ID != "data_exchange:report" {
		t.Fatalf("reclaimed job=%+v err=%v", job, err)
	}
	if owner.readCalls != readCalls || owner.versionCalls != versionCalls || exchange.submits != 2 {
		t.Fatalf("retry probed or resubmitted incorrectly: reads=%d/%d versions=%d/%d submits=%d", owner.readCalls, readCalls, owner.versionCalls, versionCalls, exchange.submits)
	}
}

func TestReportExportTerminalSubmitConflictReplaysStableTypedFailure(t *testing.T) {
	principal := reportPrincipal()
	control := reportExportLifecycleControl()
	reportDefinition := reportmodel.ReportSchema{Key: "revenue", ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{
		SQL: "SELECT c.id AS id FROM customer c ORDER BY c.id LIMIT 2000", SourceObjects: []string{"customer"},
		ResultSchema: []reportmodel.ReportResultColumnSchema{{Key: "id", Type: "text", Kind: "dimension"}},
	}}
	store := &reportExportStoreStub{audit: recordmodel.Record{ID: "audit-1", Data: map[string]any{
		"report_key": "revenue", "requested_by_identity_user_id": principal.UserID, "status": "approved",
	}}}
	exchange := &reportDataExchangeBindingProbe{onSubmit: func(dataexchange.ExportRequest, dataexchange.Job) error {
		return fmt.Errorf("deterministic Data Exchange conflict: %w", dataexchange.ErrIdempotencyKeyReused)
	}}
	providers := recordapplication.NewDataExchangeProviders(func(context.Context, string, string) principalmodel.Principal { return principal })
	receipts := newReportPrepareReceiptStore(t)
	service := NewReportExportApplicationService(ReportExportApplicationDependencies{
		Records: store, Audit: &reportAuditAppenderStub{}, DataExchange: exchange, DataExchangeProviders: providers, PrepareReceipts: receipts,
	})
	owner := &reportOwnerExportsStub{definition: reportmodel.ReportExportDefinition{Report: reportDefinition, Control: control}}
	if err := service.BindReportExports(owner); err != nil {
		t.Fatal(err)
	}
	request := reportmodel.ReportExportPrepareRequest{
		ReportKey: "revenue", ObjectKey: "customer", AuditID: "audit-1", IdempotencyKey: "request-1",
		Scope: reportmodel.ReportExportScopeRequest{FieldProjection: []string{"id"}, Purpose: "terminal conflict", Freshness: reportmodel.ReportExportFreshness{Mode: "realtime"}},
	}
	if _, err := service.PrepareResolvedExport(t.Context(), request, reportDefinition, control, principal); apperror.CodeOf(err) != "backend.idempotency.key_reused" {
		t.Fatalf("initial error=%v code=%q", err, apperror.CodeOf(err))
	}
	readCalls, versionCalls, submits := owner.readCalls, owner.versionCalls, exchange.submits
	if _, err := service.PrepareResolvedExport(t.Context(), request, reportDefinition, control, principal); apperror.CodeOf(err) != "backend.idempotency.key_reused" {
		t.Fatalf("terminal replay error=%v code=%q", err, apperror.CodeOf(err))
	}
	if owner.readCalls != readCalls || owner.versionCalls != versionCalls || exchange.submits != submits || exchange.jobCalls != 0 {
		t.Fatalf("terminal replay touched mutable work: reads=%d/%d versions=%d/%d submits=%d/%d jobs=%d", owner.readCalls, readCalls, owner.versionCalls, versionCalls, exchange.submits, submits, exchange.jobCalls)
	}
	var payload reportexport.ExportPayload
	if err := json.Unmarshal(exchange.submission.Options, &payload); err != nil {
		t.Fatal(err)
	}
	receipt, found, err := receipts.GetReportExportPrepareReceipt(t.Context(), principal.WorkspaceID, payload.PrepareReceiptID)
	if err != nil || !found || receipt.Status != "failed_terminal" || receipt.TerminalErrorCode != "backend.idempotency.key_reused" || strings.TrimSpace(receipt.ExpiresAt) == "" {
		t.Fatalf("terminal receipt=%+v found=%v err=%v", receipt, found, err)
	}
}

func TestReportExportFreshFailureReleasesReceiptAfterRequestCancellation(t *testing.T) {
	principal := reportPrincipal()
	control := reportExportLifecycleControl()
	reportDefinition := reportmodel.ReportSchema{Key: "revenue", ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{
		SQL: "SELECT c.id AS id FROM customer c ORDER BY c.id LIMIT 2000", SourceObjects: []string{"customer"},
		ResultSchema: []reportmodel.ReportResultColumnSchema{{Key: "id", Type: "text", Kind: "dimension"}},
	}}
	requestContext, cancel := context.WithCancel(t.Context())
	store := &reportExportStoreStub{audit: recordmodel.Record{ID: "audit-1", Data: map[string]any{
		"report_key": "revenue", "requested_by_identity_user_id": principal.UserID, "status": "approved",
	}}, getErr: context.Canceled, getHook: cancel}
	exchange := &reportDataExchangeBindingProbe{}
	providers := recordapplication.NewDataExchangeProviders(func(context.Context, string, string) principalmodel.Principal { return principal })
	service := NewReportExportApplicationService(ReportExportApplicationDependencies{
		Records: store, Audit: &reportAuditAppenderStub{}, DataExchange: exchange, DataExchangeProviders: providers, PrepareReceipts: newReportPrepareReceiptStore(t),
	})
	owner := &reportOwnerExportsStub{definition: reportmodel.ReportExportDefinition{Report: reportDefinition, Control: control}}
	if err := service.BindReportExports(owner); err != nil {
		t.Fatal(err)
	}
	request := reportmodel.ReportExportPrepareRequest{
		ReportKey: "revenue", ObjectKey: "customer", AuditID: "audit-1", IdempotencyKey: "request-1",
		Scope: reportmodel.ReportExportScopeRequest{FieldProjection: []string{"id"}, Purpose: "cancelled release", Freshness: reportmodel.ReportExportFreshness{Mode: "realtime"}},
	}
	if _, err := service.PrepareResolvedExport(requestContext, request, reportDefinition, control, principal); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled prepare error=%v", err)
	}
	store.getErr, store.getHook = nil, nil
	job, err := service.PrepareResolvedExport(t.Context(), request, reportDefinition, control, principal)
	if err != nil || job.ID == "" || exchange.submits != 1 {
		t.Fatalf("prepare after cancelled release job=%+v err=%v submits=%d", job, err, exchange.submits)
	}
}

func TestReportExportBusinessKeySeparatesCallerReceiptFromBusinessIdentity(t *testing.T) {
	receipt := runtimereportmodel.ReportExportPrepareReceipt{
		ID: "receipt-a", WorkspaceID: "workspace-a", RequesterUserID: "requester-a", UseCase: runtimereportmodel.ReportExportPrepareUseCase,
		ReportKey: "revenue", ObjectKey: "customer", AuditID: "audit-a", CallerKey: "caller-a",
	}
	businessKey := reportExportBusinessJobKey(receipt)
	otherCaller := receipt
	otherCaller.ID, otherCaller.CallerKey = "receipt-b", "caller-b"
	if reportExportBusinessJobKey(otherCaller) != businessKey || reportExportArtifactKey(otherCaller) == reportExportArtifactKey(receipt) {
		t.Fatal("caller receipt identity leaked into the Data Exchange business key or artifact receipt key lost isolation")
	}
	otherRequester := receipt
	otherRequester.RequesterUserID = "requester-b"
	otherWorkspace := receipt
	otherWorkspace.WorkspaceID = "workspace-b"
	if reportExportBusinessJobKey(otherRequester) == businessKey || reportExportBusinessJobKey(otherWorkspace) == businessKey {
		t.Fatal("Data Exchange business key did not isolate requester and workspace")
	}

	mapped := reportExportDataExchangeError(fmt.Errorf("wrapped: %w", dataexchange.ErrIdempotencyKeyReused))
	if apperror.CodeOf(mapped) != "backend.idempotency.key_reused" {
		t.Fatalf("sentinel mapping=%v code=%q", mapped, apperror.CodeOf(mapped))
	}
	plain := fmt.Errorf("Data Exchange idempotency key reused")
	if reportExportDataExchangeError(plain) != plain {
		t.Fatal("plain error text must not be treated as the SDK sentinel")
	}
}

func TestReportExportPrepareRejectsTerminalAuditWithoutReplayReceipt(t *testing.T) {
	principal := reportPrincipal()
	reportDefinition := reportmodel.ReportSchema{Key: "revenue", ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{
		SQL: "SELECT c.id AS id FROM customer c ORDER BY c.id LIMIT 2000", SourceObjects: []string{"customer"},
		ResultSchema: []reportmodel.ReportResultColumnSchema{{Key: "id", Type: "text", Kind: "dimension"}},
	}}
	for _, terminalStatus := range []string{"downloaded", "denied", "expired"} {
		t.Run(terminalStatus, func(t *testing.T) {
			control := reportExportLifecycleControl()
			control.RecordMapping.AuditPreparedStatuses = append(control.RecordMapping.AuditPreparedStatuses, terminalStatus)
			store := &reportExportStoreStub{audit: recordmodel.Record{ID: "audit-1", Data: map[string]any{
				"report_key": "revenue", "requested_by_identity_user_id": principal.UserID, "status": terminalStatus,
			}}}
			exchange := &reportDataExchangeBindingProbe{}
			providers := recordapplication.NewDataExchangeProviders(func(context.Context, string, string) principalmodel.Principal { return principal })
			service := NewReportExportApplicationService(ReportExportApplicationDependencies{Records: store, Audit: &reportAuditAppenderStub{}, DataExchange: exchange, DataExchangeProviders: providers, PrepareReceipts: newReportPrepareReceiptStore(t)})
			owner := &reportOwnerExportsStub{definition: reportmodel.ReportExportDefinition{Report: reportDefinition, Control: control}}
			if err := service.BindReportExports(owner); err != nil {
				t.Fatal(err)
			}
			_, err := service.PrepareResolvedExport(t.Context(), reportmodel.ReportExportPrepareRequest{
				ReportKey: "revenue", ObjectKey: "customer", AuditID: "audit-1", IdempotencyKey: "request-1",
				Scope: reportmodel.ReportExportScopeRequest{FieldProjection: []string{"id"}, Purpose: "terminal lifecycle test", Freshness: reportmodel.ReportExportFreshness{Mode: "realtime"}},
			}, reportDefinition, control, principal)
			if apperror.CodeOf(err) != "backend.report.export_audit_status_invalid" {
				t.Fatalf("error=%v code=%q", err, apperror.CodeOf(err))
			}
			if exchange.submits != 0 || exchange.jobCalls != 0 || owner.resolveCalls != 0 || owner.readCalls != 0 || owner.versionCalls != 0 {
				t.Fatalf("terminal audit without receipt touched mutable work: submits=%d jobs=%d resolve=%d read=%d version=%d", exchange.submits, exchange.jobCalls, owner.resolveCalls, owner.readCalls, owner.versionCalls)
			}
		})
	}
}

func TestReportExportDownloadRejectsCompletedJobWithoutOwnedReceipt(t *testing.T) {
	principal := reportPrincipal()
	exchange := &reportDataExchangeBindingProbe{}
	payload := reportexport.ExportPayload{
		WorkspaceID: principal.WorkspaceID, RequesterUserID: principal.UserID, PrepareReceiptID: "missing-receipt",
		ReportKey: "revenue", ObjectKey: "customer", Format: "csv",
	}
	options, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	provider := reportexport.NewDataExchangeProvider(reportexport.DataExchangeDependencies{
		Binding: exchange, Receipts: newReportPrepareReceiptStore(t),
		ResolvePrincipal: func(context.Context, dataexchange.Scope) principalmodel.Principal { return principal },
	})
	_, err = provider.OpenDataExchangeArtifact(t.Context(), dataexchange.Job{
		ID: "job-a", Provider: reportexport.DataExchangeProviderKey, Operation: "export", Status: "completed",
		WorkspaceID: principal.WorkspaceID, ActorID: principal.UserID, ObjectKey: "customer", ReferenceID: "audit-a", Options: options, ArtifactID: "artifact-a",
	}, reportexport.Scope(principal))
	if apperror.CodeOf(err) != "backend.report.export_receipt_invalid" || exchange.downloads != 0 {
		t.Fatalf("missing receipt error=%v code=%q downloads=%d", err, apperror.CodeOf(err), exchange.downloads)
	}
}

func TestReportExportPrepareDownloadReplayLifecycle(t *testing.T) {
	now := time.Date(2026, 9, 7, 9, 0, 0, 0, time.UTC)
	principal := reportPrincipal()
	control := reportExportLifecycleControl()
	control.RecordMapping.AuditPreparedStatuses = append(control.RecordMapping.AuditPreparedStatuses, control.RecordMapping.AuditDownloadedStatus)
	reportDefinition := reportmodel.ReportSchema{Key: "revenue", ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{
		SQL: "SELECT c.id AS id FROM customer c ORDER BY c.id LIMIT 2000", SourceObjects: []string{"customer"},
		ResultSchema: []reportmodel.ReportResultColumnSchema{{Key: "id", Type: "text", Kind: "dimension"}},
	}}
	store := &reportExportStoreStub{audit: recordmodel.Record{ID: "audit-1", Data: map[string]any{
		"report_key": "revenue", "requested_by_identity_user_id": principal.UserID, "status": "approved",
	}}}
	exchange := &reportDataExchangeBindingProbe{}
	providers := recordapplication.NewDataExchangeProviders(func(context.Context, string, string) principalmodel.Principal { return principal })
	service := NewReportExportApplicationService(ReportExportApplicationDependencies{
		Records: store, Audit: &reportAuditAppenderStub{}, DataExchange: exchange, DataExchangeProviders: providers, PrepareReceipts: newReportPrepareReceiptStore(t), Clock: func() time.Time { return now },
	})
	owner := &reportOwnerExportsStub{definition: reportmodel.ReportExportDefinition{Report: reportDefinition, Control: control}}
	if err := service.BindReportExports(owner); err != nil {
		t.Fatal(err)
	}
	request := reportmodel.ReportExportPrepareRequest{
		ReportKey: "revenue", ObjectKey: "customer", AuditID: "audit-1", IdempotencyKey: "request-1",
		Scope: reportmodel.ReportExportScopeRequest{FieldProjection: []string{"id"}, Purpose: "lifecycle test", Freshness: reportmodel.ReportExportFreshness{Mode: "realtime"}},
	}
	first, err := service.PrepareResolvedExport(t.Context(), request, reportDefinition, control, principal)
	if err != nil {
		t.Fatal(err)
	}
	if exchange.submits != 1 || first.ID != exchange.job.ID {
		t.Fatalf("first=%+v submits=%d", first, exchange.submits)
	}
	readCalls, versionCalls := owner.readCalls, owner.versionCalls
	owner.sourceVersion = "version-2"
	queuedReplay, err := service.PrepareResolvedExport(t.Context(), request, reportDefinition, control, principal)
	if err != nil || queuedReplay.ID != first.ID || exchange.submits != 1 || exchange.jobCalls != 1 {
		t.Fatalf("queued replay=%+v err=%v submits=%d job_calls=%d", queuedReplay, err, exchange.submits, exchange.jobCalls)
	}
	if owner.readCalls != readCalls || owner.versionCalls != versionCalls {
		t.Fatalf("queued replay reran mutable probe: reads=%d/%d versions=%d/%d", owner.readCalls, readCalls, owner.versionCalls, versionCalls)
	}
	otherKey := request
	otherKey.IdempotencyKey = "request-2"
	if _, err = service.PrepareResolvedExport(t.Context(), otherKey, reportDefinition, control, principal); apperror.CodeOf(err) != "backend.report.export_audit_operation_conflict" {
		t.Fatalf("same audit with another caller key error=%v code=%q", err, apperror.CodeOf(err))
	}
	var payload reportexport.ExportPayload
	if err = json.Unmarshal(exchange.job.Options, &payload); err != nil {
		t.Fatal(err)
	}
	expiresAt := now.Add(10 * time.Minute)
	artifact := dataexchange.Artifact{ID: exchange.job.ID + ":artifact", Filename: "revenue.csv", ContentType: "text/csv; charset=utf-8", SHA256: payload.CSVContentSHA256, ExpiresAt: expiresAt}
	completion := dataexchange.ExportCompletion{
		Scope: reportexport.Scope(principal), ObjectKey: "customer", ReferenceID: "audit-1", Options: exchange.job.Options, JobID: exchange.job.ID,
		Artifact: artifact, Rows: 1, ResultChunks: 1,
	}
	store.audit.Data[control.RecordMapping.AuditStatusField] = control.RecordMapping.AuditPreparedStatus
	if err = service.dataExchangeProvider.CompleteExport(t.Context(), completion); err != nil {
		t.Fatal(err)
	}
	if status := fmt.Sprint(store.audit.Data[control.RecordMapping.AuditStatusField]); status != "prepared" {
		t.Fatalf("status after completion=%q", status)
	}
	if rowCount := store.audit.Data[control.RecordMapping.AuditRowCountField]; rowCount != 1 {
		t.Fatalf("row count after same-status completion=%v", rowCount)
	}
	if scopeHash := strings.TrimSpace(fmt.Sprint(store.audit.Data[control.RecordMapping.AuditScopeHashField])); !strings.HasPrefix(scopeHash, "sha256:") {
		t.Fatalf("scope hash after same-status completion=%q", scopeHash)
	}
	exchange.job.Status, exchange.job.ArtifactID, exchange.job.Checkpoint, exchange.job.Total = "completed", artifact.ID, 1, 1
	exchange.artifact, exchange.content = artifact, "id\ncustomer-1\n"
	opened, err := service.dataExchangeProvider.OpenDataExchangeArtifact(t.Context(), exchange.job, reportexport.Scope(principal))
	if err != nil {
		t.Fatal(err)
	}
	_ = opened.Content.Close()
	if status := fmt.Sprint(store.audit.Data[control.RecordMapping.AuditStatusField]); status != "downloaded" {
		t.Fatalf("status after download=%q", status)
	}

	if err = service.dataExchangeProvider.CompleteExport(t.Context(), completion); err != nil {
		t.Fatalf("late completion replay: %v", err)
	}
	if status := fmt.Sprint(store.audit.Data[control.RecordMapping.AuditStatusField]); status != "downloaded" {
		t.Fatalf("late completion regressed status=%q", status)
	}
	changedCompletion := completion
	changedCompletion.Artifact.ID = "different-artifact"
	if err = service.dataExchangeProvider.CompleteExport(t.Context(), changedCompletion); apperror.CodeOf(err) != "backend.report.export_completion_conflict" {
		t.Fatalf("changed completion error=%v code=%q", err, apperror.CodeOf(err))
	}
	for _, terminalStatus := range []string{control.RecordMapping.AuditDeniedStatus, control.RecordMapping.AuditExpiredStatus} {
		store.audit.Data[control.RecordMapping.AuditStatusField] = terminalStatus
		if err = service.dataExchangeProvider.CompleteExport(t.Context(), completion); apperror.CodeOf(err) != "backend.report.export_scope_changed" {
			t.Fatalf("late completion from %q error=%v code=%q", terminalStatus, err, apperror.CodeOf(err))
		}
		if status := fmt.Sprint(store.audit.Data[control.RecordMapping.AuditStatusField]); status != terminalStatus {
			t.Fatalf("late completion regressed terminal status=%q want=%q", status, terminalStatus)
		}
	}

	readCalls, versionCalls = owner.readCalls, owner.versionCalls
	replayed, err := service.PrepareResolvedExport(t.Context(), request, reportDefinition, control, principal)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.ID != first.ID || exchange.submits != 1 || exchange.jobCalls != 2 {
		t.Fatalf("replayed=%+v first=%+v submits=%d job_calls=%d", replayed, first, exchange.submits, exchange.jobCalls)
	}
	if owner.readCalls != readCalls || owner.versionCalls != versionCalls {
		t.Fatalf("replay reran export probe: reads=%d/%d versions=%d/%d", owner.readCalls, readCalls, owner.versionCalls, versionCalls)
	}

	changed := request
	changed.Scope.Purpose = "different request"
	readCalls, versionCalls = owner.readCalls, owner.versionCalls
	submits := exchange.submits
	if _, err = service.PrepareResolvedExport(t.Context(), changed, reportDefinition, control, principal); apperror.CodeOf(err) != "backend.idempotency.key_reused" {
		t.Fatalf("changed request error=%v code=%q", err, apperror.CodeOf(err))
	}
	if owner.readCalls != readCalls || owner.versionCalls != versionCalls || exchange.submits != submits {
		t.Fatalf("fingerprint conflict reached mutable work: reads=%d/%d versions=%d/%d submits=%d/%d", owner.readCalls, readCalls, owner.versionCalls, versionCalls, exchange.submits, submits)
	}
	jobCalls := exchange.jobCalls
	other := principal
	other.UserID = "operator-2"
	if _, err = service.PrepareResolvedExport(t.Context(), request, reportDefinition, control, other); apperror.CodeOf(err) != "backend.report.export_audit_operation_conflict" {
		t.Fatalf("cross requester error=%v code=%q", err, apperror.CodeOf(err))
	}
	if exchange.jobCalls != jobCalls {
		t.Fatalf("cross requester reached replay lookup: before=%d after=%d", jobCalls, exchange.jobCalls)
	}
}

func TestReportExportLegacyCollapsedCompletionWritesMetadataAndReplaysSafely(t *testing.T) {
	now := time.Date(2026, 9, 7, 11, 0, 0, 0, time.UTC)
	principal := reportPrincipal()
	control := reportExportEdgeControl()
	if control.RecordMapping.AuditPreparedStatus != "completed" || control.RecordMapping.AuditDownloadedStatus != "completed" {
		t.Fatalf("test requires legacy collapsed lifecycle mapping: %+v", control.RecordMapping)
	}
	reportDefinition := reportmodel.ReportSchema{Key: "revenue", ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{
		SQL: "SELECT c.id AS id FROM customer c ORDER BY c.id LIMIT 2000", SourceObjects: []string{"customer"},
		ResultSchema: []reportmodel.ReportResultColumnSchema{{Key: "id", Type: "text", Kind: "dimension"}},
	}}
	store := &reportExportStoreStub{audit: recordmodel.Record{ID: "audit-1", Data: map[string]any{
		"report_key": "revenue", "requested_by_identity_user_id": principal.UserID, "status": "approved", "row_count": 0, "filters_hash": "pending",
	}}}
	audit := &reportAuditAppenderStub{}
	exchange := &reportDataExchangeBindingProbe{}
	receipts := newReportPrepareReceiptStore(t)
	providers := recordapplication.NewDataExchangeProviders(func(context.Context, string, string) principalmodel.Principal { return principal })
	service := NewReportExportApplicationService(ReportExportApplicationDependencies{
		Records: store, Audit: audit, DataExchange: exchange, DataExchangeProviders: providers,
		PrepareReceipts: receipts, Clock: func() time.Time { return now },
	})
	owner := &reportOwnerExportsStub{definition: reportmodel.ReportExportDefinition{Report: reportDefinition, Control: control}}
	if err := service.BindReportExports(owner); err != nil {
		t.Fatal(err)
	}
	_, err := service.PrepareResolvedExport(t.Context(), reportmodel.ReportExportPrepareRequest{
		ReportKey: "revenue", ObjectKey: "customer", AuditID: "audit-1", IdempotencyKey: "legacy-request",
		Scope: reportmodel.ReportExportScopeRequest{FieldProjection: []string{"id"}, Purpose: "legacy completion", Freshness: reportmodel.ReportExportFreshness{Mode: "realtime"}},
	}, reportDefinition, control, principal)
	if err != nil {
		t.Fatal(err)
	}
	var payload reportexport.ExportPayload
	if err = json.Unmarshal(exchange.job.Options, &payload); err != nil {
		t.Fatal(err)
	}
	store.audit.Data[control.RecordMapping.AuditStatusField] = control.RecordMapping.AuditPreparedStatus
	completion := dataexchange.ExportCompletion{
		Scope: reportexport.Scope(principal), ObjectKey: "customer", ReferenceID: "audit-1", Options: exchange.job.Options, JobID: exchange.job.ID,
		Artifact: dataexchange.Artifact{ID: exchange.job.ID + ":artifact", Filename: "revenue.csv", ContentType: "text/csv; charset=utf-8", SHA256: payload.CSVContentSHA256, ExpiresAt: now.Add(10 * time.Minute)},
		Rows:     1, ResultChunks: 1,
	}
	transitionFailure := errors.New("injected audit transition failure")
	store.transitionErr = transitionFailure
	auditCalls := audit.calls.Load()
	if err = service.dataExchangeProvider.CompleteExport(t.Context(), completion); !errors.Is(err, transitionFailure) {
		t.Fatalf("legacy first completion error=%v", err)
	}
	if store.createCalls.Load() != 0 || audit.calls.Load() != auditCalls {
		t.Fatalf("failed audit transition reached side effects: creates=%d audits=%d/%d", store.createCalls.Load(), audit.calls.Load(), auditCalls)
	}
	receipt, found, err := receipts.GetReportExportPrepareReceipt(t.Context(), principal.WorkspaceID, payload.PrepareReceiptID)
	if err != nil || !found || receipt.CompletionArtifactID != completion.Artifact.ID {
		t.Fatalf("durable completion binding receipt=%+v found=%v err=%v", receipt, found, err)
	}
	store.transitionErr = nil
	if err = service.dataExchangeProvider.CompleteExport(t.Context(), completion); err != nil {
		t.Fatalf("legacy exact completion retry: %v", err)
	}
	if rowCount := fmt.Sprint(store.audit.Data[control.RecordMapping.AuditRowCountField]); rowCount != "1" {
		t.Fatalf("legacy recovered completion row count=%q", rowCount)
	}
	if scopeHash := strings.TrimSpace(fmt.Sprint(store.audit.Data[control.RecordMapping.AuditScopeHashField])); !strings.HasPrefix(scopeHash, "sha256:") {
		t.Fatalf("legacy recovered completion scope hash=%q", scopeHash)
	}
	if store.createCalls.Load() != 1 || store.download.ID == "" || audit.calls.Load() != auditCalls+1 {
		t.Fatalf("legacy retry side effects: creates=%d download=%+v audits=%d/%d", store.createCalls.Load(), store.download, audit.calls.Load(), auditCalls+1)
	}
}

func TestReportExportDistinctDownloadedStatusRejectsFailedBindingRetry(t *testing.T) {
	now := time.Date(2026, 9, 7, 12, 0, 0, 0, time.UTC)
	principal := reportPrincipal()
	control := reportExportLifecycleControl()
	reportDefinition := reportmodel.ReportSchema{Key: "revenue", ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{
		SQL: "SELECT c.id AS id FROM customer c ORDER BY c.id LIMIT 2000", SourceObjects: []string{"customer"},
		ResultSchema: []reportmodel.ReportResultColumnSchema{{Key: "id", Type: "text", Kind: "dimension"}},
	}}
	store := &reportExportStoreStub{audit: recordmodel.Record{ID: "audit-1", Data: map[string]any{
		"report_key": "revenue", "requested_by_identity_user_id": principal.UserID, "status": "approved",
	}}}
	audit := &reportAuditAppenderStub{}
	exchange := &reportDataExchangeBindingProbe{}
	receipts := newReportPrepareReceiptStore(t)
	providers := recordapplication.NewDataExchangeProviders(func(context.Context, string, string) principalmodel.Principal { return principal })
	service := NewReportExportApplicationService(ReportExportApplicationDependencies{
		Records: store, Audit: audit, DataExchange: exchange, DataExchangeProviders: providers,
		PrepareReceipts: receipts, Clock: func() time.Time { return now },
	})
	owner := &reportOwnerExportsStub{definition: reportmodel.ReportExportDefinition{Report: reportDefinition, Control: control}}
	if err := service.BindReportExports(owner); err != nil {
		t.Fatal(err)
	}
	_, err := service.PrepareResolvedExport(t.Context(), reportmodel.ReportExportPrepareRequest{
		ReportKey: "revenue", ObjectKey: "customer", AuditID: "audit-1", IdempotencyKey: "pre-downloaded-request",
		Scope: reportmodel.ReportExportScopeRequest{FieldProjection: []string{"id"}, Purpose: "pre-downloaded completion", Freshness: reportmodel.ReportExportFreshness{Mode: "realtime"}},
	}, reportDefinition, control, principal)
	if err != nil {
		t.Fatal(err)
	}
	var payload reportexport.ExportPayload
	if err = json.Unmarshal(exchange.job.Options, &payload); err != nil {
		t.Fatal(err)
	}
	store.audit.Data[control.RecordMapping.AuditStatusField] = control.RecordMapping.AuditDownloadedStatus
	completion := dataexchange.ExportCompletion{
		Scope: reportexport.Scope(principal), ObjectKey: "customer", ReferenceID: "audit-1", Options: exchange.job.Options, JobID: exchange.job.ID,
		Artifact: dataexchange.Artifact{ID: exchange.job.ID + ":artifact", Filename: "revenue.csv", ContentType: "text/csv; charset=utf-8", SHA256: payload.CSVContentSHA256, ExpiresAt: now.Add(10 * time.Minute)},
		Rows:     1, ResultChunks: 1,
	}
	auditCalls := audit.calls.Load()
	for attempt := 1; attempt <= 2; attempt++ {
		if err = service.dataExchangeProvider.CompleteExport(t.Context(), completion); apperror.CodeOf(err) != "backend.report.export_scope_changed" {
			t.Fatalf("pre-downloaded completion attempt %d error=%v code=%q", attempt, err, apperror.CodeOf(err))
		}
	}
	receipt, found, err := receipts.GetReportExportPrepareReceipt(t.Context(), principal.WorkspaceID, payload.PrepareReceiptID)
	if err != nil || !found || receipt.CompletionArtifactID != completion.Artifact.ID {
		t.Fatalf("durable rejected binding receipt=%+v found=%v err=%v", receipt, found, err)
	}
	if store.createCalls.Load() != 0 || store.download.ID != "" || audit.calls.Load() != auditCalls {
		t.Fatalf("failed binding retry reached side effects: creates=%d download=%+v audits=%d/%d", store.createCalls.Load(), store.download, audit.calls.Load(), auditCalls)
	}
}

func TestReportDataExchangeProviderPagesAndFinalizesBusinessProjection(t *testing.T) {
	now := time.Date(2026, 8, 30, 10, 0, 0, 0, time.UTC)
	principal := reportPrincipal()
	store := &reportExportStoreStub{audit: recordmodel.Record{ID: "audit-1", Data: map[string]any{
		"report_key": "revenue", "requested_by_identity_user_id": principal.UserID, "status": "approved",
	}}}
	control := reportExportEdgeControl()
	reportDefinition := reportmodel.ReportSchema{Key: "revenue", ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{
		SQL: "SELECT c.id AS id FROM customer c ORDER BY c.id LIMIT 2000", SourceObjects: []string{"customer"},
		ResultSchema: []reportmodel.ReportResultColumnSchema{{Key: "id", Type: "text", Kind: "dimension"}},
	}}
	exchange := &reportDataExchangeBindingProbe{}
	providers := recordapplication.NewDataExchangeProviders(func(context.Context, string, string) principalmodel.Principal { return principal })
	service := NewReportExportApplicationService(ReportExportApplicationDependencies{
		Records: store, Audit: &reportAuditAppenderStub{}, DataExchange: exchange, DataExchangeProviders: providers, PrepareReceipts: newReportPrepareReceiptStore(t),
		Clock: func() time.Time { return now },
	})
	owner := &reportOwnerExportsStub{definition: reportmodel.ReportExportDefinition{Report: reportDefinition, Control: control}}
	if err := service.BindReportExports(owner); err != nil {
		t.Fatal(err)
	}
	_, err := service.PrepareResolvedExport(t.Context(), reportmodel.ReportExportPrepareRequest{
		ReportKey: "revenue", ObjectKey: "customer", AuditID: "audit-1", IdempotencyKey: "request-1",
		Scope: reportmodel.ReportExportScopeRequest{FieldProjection: []string{"id"}, Purpose: "provider test", Freshness: reportmodel.ReportExportFreshness{Mode: "realtime"}},
	}, reportDefinition, control, principal)
	if err != nil {
		t.Fatal(err)
	}
	options := exchange.job.Options
	var payload reportexport.ExportPayload
	if err = json.Unmarshal(options, &payload); err != nil {
		t.Fatal(err)
	}
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
