package runtime

import (
	"context"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	dataexchangemodule "github.com/domainry/domainry-data-exchange/module"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	reportsdk "github.com/domainry/domainry-report-sdk"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	auditcontract "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	reportexportapplication "github.com/domainry/domainry-runtime/runtime/application/report/export/application"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	persistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	reportpersistence "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/report"
	"github.com/domainry/domainry-runtime/runtime/platform/config"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

func TestReportExportDeliveryMaterializesSmallResultWithBusinessTTL(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "report-export-inline-real-binding.db")
	principal := realBindingReportPrincipal()
	requestContext := identitysdk.WithRequestIdentity(t.Context(), identitysdk.RequestIdentity{Principal: principal.Principal})
	store, err := persistence.OpenContext(t.Context(), config.Config{
		DatabaseDriver: "sqlite", DBPath: databasePath, IntegrationSecretKey: "report-export-inline-real-binding",
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	if err = store.EnsureRuntimeSchema(t.Context()); err != nil {
		t.Fatal(err)
	}
	providers := recordapplication.NewDataExchangeProviders(func(context.Context, string, string) principalmodel.Principal { return principal })
	binding, err := openDataExchangeBinding(t.Context(), dataexchangemodule.NewFactory(dataexchangemodule.Options{}), dataexchange.ApplicationRef{
		ApplicationID: "report-export-inline-integration", RuntimeID: "runtime-integration",
	}, dataExchangeModuleHost{store: store, providers: providers})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = binding.Close(context.Background()) })

	control := realBindingReportControl()
	control.DownloadTTLSeconds = int64((15 * 24 * time.Hour) / time.Second)
	reportDefinition := reportmodel.ReportSchema{Key: "revenue", ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{
		SQL: "SELECT c.id AS id FROM customer c ORDER BY c.id LIMIT 2000", SourceObjects: []string{"customer"},
		ResultSchema: []reportmodel.ReportResultColumnSchema{{Key: "id", Type: "text", Kind: "dimension"}},
	}}
	records := &realBindingReportRecords{audit: recordmodel.Record{ID: "audit-1", Data: map[string]any{
		"report_key": "revenue", "requested_by_identity_user_id": principal.UserID, "status": "approved",
	}}}
	owner := &realBindingReportExports{definition: reportmodel.ReportExportDefinition{Report: reportDefinition, Control: control}}
	service := reportexportapplication.NewReportExportApplicationService(reportexportapplication.ReportExportApplicationDependencies{
		Records: records, Audit: realBindingReportAudit{}, DataExchange: binding, DataExchangeProviders: providers,
		PrepareReceipts: reportpersistence.NewReportExportPrepareReceiptStore(store),
	})
	if err = service.BindReportExports(owner); err != nil {
		t.Fatal(err)
	}
	result, err := service.PrepareResolvedExportDelivery(requestContext, reportmodel.ReportExportPrepareRequest{
		ReportKey: "revenue", ObjectKey: "customer", AuditID: "audit-1", IdempotencyKey: "request-inline-1",
		Scope: reportmodel.ReportExportScopeRequest{FieldProjection: []string{"id"}, Purpose: "inline integration", Freshness: reportmodel.ReportExportFreshness{Mode: "realtime"}},
	}, reportDefinition, control, principal)
	if err != nil {
		t.Fatal(err)
	}
	if result.Job.Status != "completed" || result.Job.RowsExported != 1 || result.Artifact == nil {
		t.Fatalf("inline result=%+v", result)
	}
	content, err := io.ReadAll(result.Artifact.Content)
	if err != nil {
		t.Fatal(err)
	}
	_ = result.Artifact.Content.Close()
	if string(content) != "id\ncustomer-1\n" {
		t.Fatalf("inline content=%q", content)
	}
	createdAt, createdErr := time.Parse(time.RFC3339Nano, result.Job.CreatedAt)
	expiresAt, expiresErr := time.Parse(time.RFC3339Nano, result.Artifact.ExpiresAt)
	if createdErr != nil || expiresErr != nil || expiresAt.Sub(createdAt) != 15*24*time.Hour {
		t.Fatalf("created=%q expires=%q createdErr=%v expiresErr=%v", result.Job.CreatedAt, result.Artifact.ExpiresAt, createdErr, expiresErr)
	}
	if records.status() != control.RecordMapping.AuditDownloadedStatus {
		t.Fatalf("audit status=%q", records.status())
	}
	var jobCount int
	if err = store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _data_exchange_jobs WHERE workspace_id = ? AND provider = ? AND operation = ?`, principal.WorkspaceID, "reports", "export").Scan(&jobCount); err != nil || jobCount != 1 {
		t.Fatalf("data exchange jobs=%d err=%v", jobCount, err)
	}
}

func TestReportExportDurableReceiptWithRealSQLiteDataExchangeBinding(t *testing.T) {
	databasePath := filepath.Join(t.TempDir(), "report-export-real-binding.db")
	principal := realBindingReportPrincipal()
	requestContext := identitysdk.WithRequestIdentity(t.Context(), identitysdk.RequestIdentity{Principal: principal.Principal})
	var store *persistence.RuntimeStore
	var binding dataexchange.Binding
	var providers *recordapplication.DataExchangeProviders
	var done <-chan struct{}
	closeGeneration := func() {
		if binding != nil {
			_ = binding.Close(context.Background())
		}
		if done != nil {
			select {
			case <-done:
			case <-time.After(2 * time.Second):
			}
		}
		if store != nil {
			_ = store.Close()
		}
		store, binding, providers, done = nil, nil, nil, nil
	}
	t.Cleanup(closeGeneration)
	openGeneration := func() {
		var err error
		store, err = persistence.OpenContext(t.Context(), config.Config{
			DatabaseDriver: "sqlite", DBPath: databasePath, IntegrationSecretKey: "report-export-real-binding",
		})
		if err != nil {
			t.Fatal(err)
		}
		if err = store.EnsureRuntimeSchema(t.Context()); err != nil {
			t.Fatal(err)
		}
		providers = recordapplication.NewDataExchangeProviders(func(context.Context, string, string) principalmodel.Principal { return principal })
		binding, err = openDataExchangeBinding(t.Context(), dataexchangemodule.NewFactory(dataexchangemodule.Options{}), dataexchange.ApplicationRef{
			ApplicationID: "report-export-integration", RuntimeID: "runtime-integration",
		}, dataExchangeModuleHost{store: store, providers: providers})
		if err != nil {
			t.Fatal(err)
		}
	}
	openGeneration()

	control := realBindingReportControl()
	reportDefinition := reportmodel.ReportSchema{Key: "revenue", ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{
		SQL: "SELECT c.id AS id FROM customer c ORDER BY c.id LIMIT 2000", SourceObjects: []string{"customer"},
		ResultSchema: []reportmodel.ReportResultColumnSchema{{Key: "id", Type: "text", Kind: "dimension"}},
	}}
	records := &realBindingReportRecords{audit: recordmodel.Record{ID: "audit-1", Data: map[string]any{
		"report_key": "revenue", "requested_by_identity_user_id": principal.UserID, "status": "approved",
	}}}
	owner := &realBindingReportExports{definition: reportmodel.ReportExportDefinition{Report: reportDefinition, Control: control}}
	newService := func(exports *realBindingReportExports) *reportexportapplication.ReportExportApplicationService {
		service := reportexportapplication.NewReportExportApplicationService(reportexportapplication.ReportExportApplicationDependencies{
			Records: records, Audit: realBindingReportAudit{}, DataExchange: binding, DataExchangeProviders: providers,
			PrepareReceipts: reportpersistence.NewReportExportPrepareReceiptStore(store),
		})
		if bindErr := service.BindReportExports(exports); bindErr != nil {
			t.Fatal(bindErr)
		}
		return service
	}
	service := newService(owner)
	request := reportmodel.ReportExportPrepareRequest{
		ReportKey: "revenue", ObjectKey: "customer", AuditID: "audit-1", IdempotencyKey: "request-1",
		Scope: reportmodel.ReportExportScopeRequest{FieldProjection: []string{"id"}, Purpose: "real binding integration", Freshness: reportmodel.ReportExportFreshness{Mode: "realtime"}},
	}
	prepared, err := service.PrepareResolvedExport(requestContext, request, reportDefinition, control, principal)
	if err != nil {
		t.Fatal(err)
	}
	closeGeneration()
	openGeneration()
	queuedOwner := &realBindingReportExports{definition: owner.definition, sourceVersion: "version-after-service-rebuild"}
	service = newService(queuedOwner)
	queuedReplay, err := service.PrepareResolvedExport(requestContext, request, reportDefinition, control, principal)
	if err != nil || queuedReplay.ID != prepared.ID || queuedReplay.Status != prepared.Status || queuedReplay.Status == "completed" {
		t.Fatalf("queued replay=%+v prepared=%+v err=%v", queuedReplay, prepared, err)
	}
	if resolveCalls, readCalls, versionCalls := queuedOwner.calls(); resolveCalls != 0 || readCalls != 0 || versionCalls != 0 {
		t.Fatalf("queued replay probed rebuilt owner: resolve=%d read=%d version=%d", resolveCalls, readCalls, versionCalls)
	}
	queuedOwner.mu.Lock()
	queuedOwner.sourceVersion = "version-1"
	queuedOwner.mu.Unlock()
	done = binding.Start(t.Context(), dataexchange.WorkerConfig{Enabled: true, PollInterval: time.Millisecond, BatchSize: 2, LeaseTTL: time.Second})
	completed := waitRealBindingReportJob(t, requestContext, binding, principal, prepared.ID)
	if completed.Status != "completed" || completed.ArtifactID == "" || completed.Checkpoint != 1 {
		t.Fatalf("completed=%+v", completed)
	}
	closeGeneration()
	openGeneration()
	completedOwner := &realBindingReportExports{definition: owner.definition, sourceVersion: "version-after-completion"}
	service = newService(completedOwner)
	replayed, err := service.PrepareResolvedExport(requestContext, request, reportDefinition, control, principal)
	if err != nil || replayed.ID != prepared.ID || replayed.Status != "completed" {
		t.Fatalf("replayed=%+v prepared=%+v err=%v", replayed, prepared, err)
	}
	if resolveCalls, readCalls, versionCalls := completedOwner.calls(); resolveCalls != 0 || readCalls != 0 || versionCalls != 0 {
		t.Fatalf("completed replay probed rebuilt owner: resolve=%d read=%d version=%d", resolveCalls, readCalls, versionCalls)
	}
	if status := records.status(); status != "prepared" {
		t.Fatalf("audit status=%q", status)
	}
	var jobCount int
	if err = store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM _data_exchange_jobs WHERE workspace_id = ? AND provider = ? AND operation = ?`, principal.WorkspaceID, "reports", "export").Scan(&jobCount); err != nil || jobCount != 1 {
		t.Fatalf("data exchange jobs=%d err=%v", jobCount, err)
	}
	var ledgerTables int
	if err = store.DB().QueryRowContext(t.Context(), `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name LIKE '%schema_migrations%'`).Scan(&ledgerTables); err != nil || ledgerTables != 1 {
		t.Fatalf("migration ledger tables=%d err=%v", ledgerTables, err)
	}
}

type realBindingReportExports struct {
	mu                                    sync.Mutex
	definition                            reportmodel.ReportExportDefinition
	resolveCalls, readCalls, versionCalls int
	sourceVersion                         string
}

func (*realBindingReportExports) Prepare(context.Context, reportmodel.ReportExportPrepareRequest, reportmodel.ReportAuthority) (reportmodel.ReportExportJob, error) {
	return reportmodel.ReportExportJob{}, nil
}
func (r *realBindingReportExports) ResolveExecution(_ context.Context, request reportmodel.ReportExportExecutionRequest, _ reportmodel.ReportAuthority) (reportmodel.ReportExportExecution, error) {
	r.mu.Lock()
	r.resolveCalls++
	r.mu.Unlock()
	return reportmodel.ReportExportExecution{Definition: r.definition, Scope: request.Scope}, nil
}
func (r *realBindingReportExports) ReadPage(context.Context, reportmodel.ReportExportExecutionRequest, reportmodel.ReportAuthority) (reportmodel.ReportSummary, error) {
	r.mu.Lock()
	r.readCalls++
	r.mu.Unlock()
	return reportmodel.ReportSummary{Rows: []reportmodel.ReportResultRow{{Dimensions: map[string]string{"id": "customer-1"}}}, RowCount: 1, Total: 1, TotalSemantics: reportmodel.ReportTotalExact}, nil
}
func (r *realBindingReportExports) SourceVersion(context.Context, reportmodel.ReportExportExecutionRequest, reportmodel.ReportAuthority) (reportmodel.ReportSnapshotSourceVersion, error) {
	r.mu.Lock()
	r.versionCalls++
	version := r.sourceVersion
	r.mu.Unlock()
	if version == "" {
		version = "version-1"
	}
	return reportmodel.ReportSnapshotSourceVersion{Watermark: version, SourceVersions: map[string]string{"customer": version}}, nil
}
func (r *realBindingReportExports) calls() (int, int, int) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.resolveCalls, r.readCalls, r.versionCalls
}

type realBindingReportRecords struct {
	mu       sync.Mutex
	audit    recordmodel.Record
	download recordmodel.Record
}

func (r *realBindingReportRecords) GetReportRecord(_ context.Context, objectKey, recordID string, _ principalmodel.Principal) (recordmodel.Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if objectKey == "report_export_audit" && recordID == r.audit.ID {
		return cloneRealBindingReportRecord(r.audit), nil
	}
	if objectKey == "report_export_download" && recordID == r.download.ID {
		return cloneRealBindingReportRecord(r.download), nil
	}
	return recordmodel.Record{}, fmt.Errorf("report record not found")
}
func (r *realBindingReportRecords) CreateReportRecord(_ context.Context, _ string, data map[string]any, _ string, _ principalmodel.Principal) (recordmodel.Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.download.ID == "" {
		r.download = recordmodel.Record{ID: "download-1", Data: cloneRealBindingReportData(data)}
	}
	return cloneRealBindingReportRecord(r.download), nil
}
func (r *realBindingReportRecords) UpdateReportRecord(_ context.Context, objectKey, _ string, patch map[string]any, _ string, _ principalmodel.Principal) (recordmodel.Record, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	target := &r.download
	if objectKey == "report_export_audit" {
		target = &r.audit
	}
	for key, value := range patch {
		target.Data[key] = value
	}
	return cloneRealBindingReportRecord(*target), nil
}
func (r *realBindingReportRecords) TransitionReportExportAuditStatus(_ context.Context, _, _, recordID, statusField, fromStatus, toStatus string) error {
	_, err := r.TransitionReportExportAudit(context.Background(), "", "", recordID, statusField, fromStatus, map[string]any{statusField: toStatus})
	return err
}
func (r *realBindingReportRecords) TransitionReportExportAudit(_ context.Context, _, _, recordID, statusField, fromStatus string, patch map[string]any) (bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.audit.ID != recordID || fmt.Sprint(r.audit.Data[statusField]) != fromStatus {
		return false, nil
	}
	for key, value := range patch {
		r.audit.Data[key] = value
	}
	return true, nil
}
func (r *realBindingReportRecords) status() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return fmt.Sprint(r.audit.Data["status"])
}

type realBindingReportAudit struct{}

func (realBindingReportAudit) AppendAudit(context.Context, auditcontract.AuditAppendRequest) error {
	return nil
}

func waitRealBindingReportJob(t testing.TB, ctx context.Context, binding dataexchange.Binding, principal principalmodel.Principal, jobID string) dataexchange.Job {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	deadline := time.NewTimer(10 * time.Second)
	defer deadline.Stop()
	for {
		job, err := binding.Job(ctx, dataexchange.JobRequest{Scope: dataexchange.Scope{WorkspaceID: principal.WorkspaceID, ActorID: principal.UserID}, JobID: jobID, Provider: "reports", Operation: "export"})
		if err != nil {
			t.Fatal(err)
		}
		if job.Status == "completed" {
			return job
		}
		if job.Status == "failed" || job.Status == "cancelled" {
			t.Fatalf("job=%+v", job)
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatalf("timed out waiting for report export job: %+v", job)
		}
	}
}

func realBindingReportPrincipal() principalmodel.Principal {
	bundle := accessfixture.Bundle{Key: "report-export", Permissions: []string{"customer.read", "customer.export"}, DataPolicies: accessfixture.DataPoliciesForPermissions([]string{"customer.read", "customer.export"}, "all"), FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: "customer", FieldKey: "id", Read: true, Export: true}}}
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "operator-1", WorkspaceID: "workspace-a"}}, bundle)
	for index, permission := range []string{dataexchange.ActionDataExchangeJobGet, dataexchange.ActionDataExchangeJobDownload} {
		separator := strings.LastIndexByte(permission, '.')
		resource, action := permission[:separator], permission[separator+1:]
		principal.AccessBundle.FunctionGrants = append(principal.AccessBundle.FunctionGrants, identitysdk.FunctionGrant{Resource: identitysdk.ResourceType(resource), Action: identitysdk.Action(action), Effect: identitysdk.EffectAllow})
		principal.AccessBundle.DataPolicies = append(principal.AccessBundle.DataPolicies, identitysdk.DataPolicy{
			Key: fmt.Sprintf("data-%s-%d", permission, index), Resource: identitysdk.ResourceType(resource), Action: identitysdk.Action(action), Effect: identitysdk.EffectAllow,
			DataScopes: []identitysdk.DataScope{identitysdk.DataScopeOwner}, Predicate: identitysdk.Predicate{Fact: "owner_user_id", Operator: identitysdk.OperatorEqual, Value: "$subject.id"},
		})
	}
	return principal
}

func realBindingReportControl() reportmodel.ReportExportControlSchema {
	return reportmodel.ReportExportControlSchema{
		ReportKey: "revenue", SourceObjects: []string{"customer"}, AuditObject: "report_export_audit", DownloadObject: "report_export_download", MaxRows: 1000,
		RecordMapping: reportmodel.ReportExportRecordMappingSchema{
			AuditReportKeyField: "report_key", AuditRequesterField: "requested_by_identity_user_id", AuditStatusField: "status",
			AuditPreparedStatuses: []string{"approved"}, AuditPreparedStatus: "prepared", AuditDownloadedStatus: "downloaded", AuditDeniedStatus: "denied", AuditExpiredStatus: "expired",
			AuditRowCountField: "row_count", AuditScopeHashField: "filters_hash", DownloadAuditField: "audit_id", DownloadFilenameField: "file_name",
			DownloadContentHashField: "content_hash", DownloadExpiresAtField: "expires_at", DownloadJobIDField: "file_reference", DownloadWatermarkedField: "watermarked", DownloadNumberField: "download_no",
		},
	}
}

func cloneRealBindingReportRecord(value recordmodel.Record) recordmodel.Record {
	value.Data = cloneRealBindingReportData(value.Data)
	return value
}

func cloneRealBindingReportData(value map[string]any) map[string]any {
	clone := make(map[string]any, len(value))
	for key, item := range value {
		clone[key] = item
	}
	return clone
}

var _ reportsdk.Exports = (*realBindingReportExports)(nil)
var _ reportexportapplication.ReportExportRecordStore = (*realBindingReportRecords)(nil)
