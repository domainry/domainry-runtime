package report

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	reportexport "github.com/domainry/domainry-runtime/runtime/application/report/export"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	reportservice "github.com/domainry/domainry-runtime/runtime/domain/report/service"
)

func reportExportEdgeService(store ReportExportRecordStore, records ReportRecordExporter, controls []reportmodel.ReportExportControlSchema, now time.Time) *ReportApplicationService {
	reportDefinition := reportmodel.ReportSchema{Key: "revenue", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "customer", Alias: "customer"}, Dimensions: []reportmodel.ReportDatasetDimension{{Key: "id", Field: reportmodel.ReportDatasetField{SourceAlias: "customer", FieldKey: "id"}}}}}
	domain := reportservice.NewReportDomainService(reportservice.ReportDependencies{Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
		return []reportmodel.ReportSchema{reportDefinition}
	}, Access: reportExportDatasetAccessStub{}, Records: reportExportDatasetRecordsStub{}})
	artifacts := &reportExportArtifactStoreStub{}
	if legacy, ok := store.(*reportExportStoreStub); ok && legacy.download.ID != "" {
		content := []byte("id\ncustomer-1\n")
		principal := reportPrincipal()
		control := reportExportEdgeControl()
		if len(controls) > 0 {
			control = controls[0]
		}
		scope, _, _, _ := reportexport.NormalizeScope(reportDefinition, "customer", control, reportmodel.ReportExportScopeRequest{Purpose: "legacy governed report export", Freshness: reportmodel.ReportExportFreshness{Mode: "realtime"}}, principal)
		scopeHash, _ := reportexport.CanonicalJSONSHA256(scope)
		authorizationHash, _ := reportservice.ReportAccessScopeHash(principal)
		reportHash, _ := reportexport.CanonicalJSONSHA256(reportDefinition)
		controlHash, _ := reportexport.CanonicalJSONSHA256(control)
		artifacts.artifact = reportmodel.ReportExportArtifact{
			ID: "artifact-1", WorkspaceID: "workspace-a", ReportKey: "revenue", ObjectKey: "customer", AuditID: "audit-1",
			RequesterUserID: strings.TrimSpace(fmt.Sprint(legacy.audit.Data["requested_by_identity_user_id"])), Token: strings.TrimSpace(fmt.Sprint(legacy.download.Data["file_reference"])),
			RoleKey: principal.RoleKey, Scope: scope, ScopeSHA256: scopeHash, AuthorizationScopeSHA256: authorizationHash, ReportDefinitionSHA256: reportHash, ControlDefinitionSHA256: controlHash,
			ExpiresAt: strings.TrimSpace(fmt.Sprint(legacy.download.Data["expires_at"])), Filename: "revenue-customer.csv", Content: content, ContentSHA256: reportexport.SHA256Hex(content), RowCount: 1,
		}
	}
	return NewReportApplicationService(ReportApplicationDependencies{
		Domain: domain, Records: records, ExportRecords: store, Audit: &reportAuditAppenderStub{},
		ExportArtifacts: artifacts,
		ExportControls: func(context.Context, principalmodel.Principal) []reportmodel.ReportExportControlSchema {
			return controls
		}, Clock: func() time.Time { return now },
	})
}

func reportExportEdgeControl() reportmodel.ReportExportControlSchema {
	return reportmodel.ReportExportControlSchema{ReportKey: "revenue", SourceObjects: []string{"customer"}, AuditObject: "report_export_audit", DownloadObject: "report_export_download", RecordMapping: reportExportTestRecordMapping()}
}

func TestPrepareReportExportRemainingFailureBoundaries(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	principal := reportPrincipal()
	validAudit := recordmodel.Record{ID: "audit-1", Data: map[string]any{"report_key": "revenue", "requested_by_identity_user_id": principal.UserID, "status": "completed"}}
	control := reportExportEdgeControl()
	if _, err := reportExportEdgeService(&reportExportStoreStub{audit: validAudit}, &reportRecordExporterStub{}, []reportmodel.ReportExportControlSchema{control}, now).PrepareExport(t.Context(), "revenue", "customer", "audit-1", "key", principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("workspace err=%v", err)
	}
	if _, err := reportExportEdgeService(&reportExportStoreStub{audit: validAudit}, &reportRecordExporterStub{}, []reportmodel.ReportExportControlSchema{control}, now).PrepareExport(t.Context(), "revenue", "customer", "audit-1", " ", principal); apperror.CodeOf(err) != "backend.idempotency.key_required" {
		t.Fatalf("idempotency err=%v", err)
	}
	if _, err := reportExportEdgeService(&reportExportStoreStub{audit: validAudit}, &reportRecordExporterStub{}, []reportmodel.ReportExportControlSchema{control}, now).PrepareExport(t.Context(), "missing", "customer", "audit-1", "key", principal); apperror.CodeOf(err) != "backend.report.not_found" {
		t.Fatalf("report err=%v", err)
	}
	if _, err := reportExportEdgeService(&reportExportStoreStub{audit: validAudit}, &reportRecordExporterStub{}, nil, now).PrepareExport(t.Context(), "revenue", "customer", "audit-1", "key", principal); apperror.CodeOf(err) != "backend.report.export_control_not_found" {
		t.Fatalf("control err=%v", err)
	}
	if _, err := reportExportEdgeService(nil, &reportRecordExporterStub{}, []reportmodel.ReportExportControlSchema{control}, now).PrepareExport(t.Context(), "revenue", "customer", "audit-1", "key", principal); apperror.CodeOf(err) != "backend.internal_error" {
		t.Fatalf("store err=%v", err)
	}
	missingArtifacts := reportExportEdgeService(&reportExportStoreStub{audit: validAudit}, &reportRecordExporterStub{}, []reportmodel.ReportExportControlSchema{control}, now)
	missingArtifacts.exportArtifacts = nil
	if _, err := missingArtifacts.PrepareExport(t.Context(), "revenue", "customer", "audit-1", "key", principal); apperror.CodeOf(err) != "backend.internal_error" {
		t.Fatalf("artifact dependency err=%v", err)
	}
	for name, store := range map[string]*reportExportStoreStub{
		"get":       {audit: validAudit, getErr: errors.New("get")},
		"mismatch":  {audit: recordmodel.Record{ID: "audit-1", Data: map[string]any{"report_key": "other", "requested_by_identity_user_id": principal.UserID}}},
		"requester": {audit: recordmodel.Record{ID: "audit-1", Data: map[string]any{"report_key": "revenue", "requested_by_identity_user_id": ""}}},
		"create":    {audit: validAudit, createErr: errors.New("create")},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := reportExportEdgeService(store, &reportRecordExporterStub{}, []reportmodel.ReportExportControlSchema{control}, now).PrepareExport(t.Context(), "revenue", "customer", "audit-1", "key", principal); err == nil {
				t.Fatal("expected preparation error")
			}
		})
	}
	approvalControl := control
	approvalControl.ApprovalRequired = true
	if _, err := reportExportEdgeService(&reportExportStoreStub{audit: validAudit}, &reportRecordExporterStub{}, []reportmodel.ReportExportControlSchema{approvalControl}, now).PrepareExport(t.Context(), "revenue", "customer", "audit-1", "key", principal); err != nil {
		t.Fatalf("completed approval err=%v", err)
	}
	invalidStatus := validAudit
	invalidStatus.Data = map[string]any{"report_key": "revenue", "requested_by_identity_user_id": principal.UserID, "status": "requested"}
	if _, err := reportExportEdgeService(&reportExportStoreStub{audit: invalidStatus}, &reportRecordExporterStub{}, []reportmodel.ReportExportControlSchema{control}, now).PrepareExport(t.Context(), "revenue", "customer", "audit-1", "invalid-status", principal); apperror.CodeOf(err) != "backend.report.export_audit_status_invalid" {
		t.Fatalf("non-approval status err=%v", err)
	}
	withoutToken := control
	withoutToken.RecordMapping.DownloadTokenField = ""
	if _, err := reportExportEdgeService(&reportExportStoreStub{audit: validAudit}, &reportRecordExporterStub{}, []reportmodel.ReportExportControlSchema{withoutToken}, now).PrepareExport(t.Context(), "revenue", "customer", "audit-1", "without-token-field", principal); err != nil {
		t.Fatalf("optional token mapping err=%v", err)
	}
	originalRead := reportExportRandomRead
	reportExportRandomRead = func([]byte) (int, error) { return 0, errors.New("random") }
	t.Cleanup(func() { reportExportRandomRead = originalRead })
	if _, err := reportExportEdgeService(&reportExportStoreStub{audit: validAudit}, &reportRecordExporterStub{}, []reportmodel.ReportExportControlSchema{control}, now).PrepareExport(t.Context(), "revenue", "customer", "audit-1", "key", principal); apperror.CodeOf(err) != "backend.internal_error" {
		t.Fatalf("token err=%v", err)
	}
}

func TestPrepareReportExportArtifactAndEvidenceFailures(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	principal := reportPrincipal()
	validAudit := recordmodel.Record{ID: "audit-1", Data: map[string]any{"report_key": "revenue", "requested_by_identity_user_id": principal.UserID, "status": "completed"}}
	control := reportExportEdgeControl()
	originalRows := reportExportRows
	reportExportRows = func(summary reportmodel.ReportSummary, _ string) ([]reportmodel.ReportResultRow, error) {
		summary.SourceRowCount = 1
		return nil, nil
	}
	zeroRowsService := reportExportEdgeService(&reportExportStoreStub{audit: validAudit}, &reportRecordExporterStub{}, []reportmodel.ReportExportControlSchema{control}, now)
	if prepared, err := zeroRowsService.PrepareExport(t.Context(), "revenue", "customer", "audit-1", "zero-rows", principal); err != nil || prepared.RowCount != 0 {
		t.Fatalf("zero projected rows err=%v", err)
	}
	validAudit.Data["status"] = "completed"
	deniedUpdateErr := errors.New("denied update")
	zeroRowsUpdateService := reportExportEdgeService(&reportExportStoreStub{audit: validAudit, updateErr: deniedUpdateErr}, &reportRecordExporterStub{}, []reportmodel.ReportExportControlSchema{control}, now)
	if _, err := zeroRowsUpdateService.PrepareExport(t.Context(), "revenue", "customer", "audit-1", "zero-rows-update", principal); !errors.Is(err, deniedUpdateErr) {
		t.Fatalf("zero projected rows update err=%v", err)
	}
	validAudit.Data["status"] = "completed"
	reportExportRows = originalRows
	t.Cleanup(func() { reportExportRows = originalRows })
	want := errors.New("artifact failure")
	for name, artifacts := range map[string]*reportExportArtifactStoreStub{
		"create": {createErr: want},
		"link":   {linkErr: want},
	} {
		t.Run(name, func(t *testing.T) {
			service := reportExportEdgeService(&reportExportStoreStub{audit: validAudit}, &reportRecordExporterStub{}, []reportmodel.ReportExportControlSchema{control}, now)
			service.exportArtifacts = artifacts
			if _, err := service.PrepareExport(t.Context(), "revenue", "customer", "audit-1", "key", principal); apperror.CodeOf(err) != "backend.internal_error" {
				t.Fatalf("err=%v", err)
			}
		})
	}
	preparedUpdateErr := errors.New("prepared update")
	preparedUpdateService := reportExportEdgeService(&reportExportStoreStub{audit: validAudit, updateErr: preparedUpdateErr}, &reportRecordExporterStub{}, []reportmodel.ReportExportControlSchema{control}, now)
	if _, err := preparedUpdateService.PrepareExport(t.Context(), "revenue", "customer", "audit-1", "prepared-update", principal); !errors.Is(err, preparedUpdateErr) {
		t.Fatalf("prepared update err=%v", err)
	}
	service := reportExportEdgeService(&reportExportStoreStub{audit: validAudit}, &reportRecordExporterStub{}, []reportmodel.ReportExportControlSchema{control}, now)
	service.audit = &reportAuditAppenderStub{err: want}
	if _, err := service.PrepareExport(t.Context(), "revenue", "customer", "audit-1", "key", principal); apperror.CodeOf(err) != "backend.internal_error" {
		t.Fatalf("audit err=%v", err)
	}

	maxControl := control
	maxControl.MaxRows = 1
	reportDefinition := reportmodel.ReportSchema{Key: "revenue", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "customer", Alias: "customer"}, Dimensions: []reportmodel.ReportDatasetDimension{{Key: "id", Field: reportmodel.ReportDatasetField{SourceAlias: "customer", FieldKey: "id"}}}}}
	service = reportExportEdgeService(&reportExportStoreStub{audit: validAudit}, &reportRecordExporterStub{}, []reportmodel.ReportExportControlSchema{maxControl}, now)
	service.domain = reportservice.NewReportDomainService(reportservice.ReportDependencies{
		Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
			return []reportmodel.ReportSchema{reportDefinition}
		},
		Access: reportExportDatasetAccessStub{}, Records: reportExportDatasetRecordsStub{items: []recordmodel.Record{{ID: "one"}, {ID: "two"}}},
	})
	if _, err := service.PrepareExport(t.Context(), "revenue", "customer", "audit-1", "key", principal); apperror.CodeOf(err) != "backend.report.export_too_many_rows" {
		t.Fatalf("max rows err=%v", err)
	}
	validAudit.Data["status"] = "completed"

	service = reportExportEdgeService(&reportExportStoreStub{audit: validAudit}, &reportRecordExporterStub{}, []reportmodel.ReportExportControlSchema{control}, now)
	if _, err := service.PrepareExportScoped(t.Context(), "revenue", "customer", "audit-1", "invalid-scope", reportmodel.ReportExportScopeRequest{}, principal); apperror.CodeOf(err) != "backend.report.export_scope_invalid" {
		t.Fatalf("scope err=%v", err)
	}
	validAudit.Data["status"] = "completed"
	want = errors.New("field authorization")
	service = reportExportEdgeService(&reportExportStoreStub{audit: validAudit}, &reportRecordExporterStub{}, []reportmodel.ReportExportControlSchema{control}, now)
	service.domain = reportservice.NewReportDomainService(reportservice.ReportDependencies{
		Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
			return []reportmodel.ReportSchema{reportDefinition}
		},
		Access: reportExportFailingAccessStub{err: want}, Records: reportExportDatasetRecordsStub{},
	})
	if _, err := service.PrepareExport(t.Context(), "revenue", "customer", "audit-1", "field-error", principal); !errors.Is(err, want) {
		t.Fatalf("field err=%v", err)
	}
	validAudit.Data["status"] = "completed"
	want = errors.New("dataset execution")
	service = reportExportEdgeService(&reportExportStoreStub{audit: validAudit}, &reportRecordExporterStub{}, []reportmodel.ReportExportControlSchema{control}, now)
	service.domain = reportservice.NewReportDomainService(reportservice.ReportDependencies{
		Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
			return []reportmodel.ReportSchema{reportDefinition}
		},
		Access: reportExportDatasetAccessStub{}, Records: reportExportDatasetRecordsStub{err: want},
	})
	if _, err := service.PrepareExport(t.Context(), "revenue", "customer", "audit-1", "dataset-error", principal); !errors.Is(err, want) {
		t.Fatalf("dataset err=%v", err)
	}
}

func TestDownloadReportExportRemainingFailureBoundaries(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	principal := reportPrincipal()
	token := strings.Repeat("a", 64)
	control := reportExportEdgeControl()
	validAudit := recordmodel.Record{ID: "audit-1", Data: map[string]any{"report_key": "revenue", "requested_by_identity_user_id": principal.UserID, "status": "completed"}}
	validDownload := recordmodel.Record{ID: "download-1", Data: map[string]any{"file_reference": token, "audit_id": "audit-1", "expires_at": now.Add(time.Hour).Format(time.RFC3339Nano)}}
	service := reportExportEdgeService(&reportExportStoreStub{audit: validAudit, download: validDownload}, &reportRecordExporterStub{}, []reportmodel.ReportExportControlSchema{control}, now)
	if _, _, err := service.DownloadExport(t.Context(), token, principalmodel.Principal{}); apperror.CodeOf(err) != "backend.workspace_scope_required" {
		t.Fatalf("workspace err=%v", err)
	}
	if _, _, err := service.DownloadExport(t.Context(), "short", principal); apperror.CodeOf(err) != "backend.report.export_download_not_found" {
		t.Fatalf("token err=%v", err)
	}
	service.exportArtifacts = nil
	if _, _, err := service.DownloadExport(t.Context(), token, principal); apperror.CodeOf(err) != "backend.report.export_download_not_found" {
		t.Fatalf("missing artifact store err=%v", err)
	}
	service = reportExportEdgeService(&reportExportStoreStub{audit: validAudit, download: validDownload}, &reportRecordExporterStub{}, []reportmodel.ReportExportControlSchema{control}, now)
	for name, candidate := range map[string]*ReportApplicationService{
		"missing dependencies": reportExportEdgeService(nil, &reportRecordExporterStub{}, []reportmodel.ReportExportControlSchema{control}, now),
		"not found":            reportExportEdgeService(&reportExportStoreStub{audit: validAudit}, &reportRecordExporterStub{}, []reportmodel.ReportExportControlSchema{control}, now),
		"audit error":          reportExportEdgeService(&reportExportStoreStub{audit: validAudit, download: validDownload, getErr: errors.New("audit")}, &reportRecordExporterStub{}, []reportmodel.ReportExportControlSchema{control}, now),
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := candidate.DownloadExport(t.Context(), token, principal); err == nil {
				t.Fatal("expected download error")
			}
		})
	}
	for name, audit := range map[string]recordmodel.Record{
		"empty requester": {ID: "audit-1", Data: map[string]any{"report_key": "revenue", "requested_by_identity_user_id": ""}},
		"other requester": {ID: "audit-1", Data: map[string]any{"report_key": "revenue", "requested_by_identity_user_id": "other"}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := reportExportEdgeService(&reportExportStoreStub{audit: audit, download: validDownload}, &reportRecordExporterStub{}, []reportmodel.ReportExportControlSchema{control}, now).DownloadExport(t.Context(), token, principal); apperror.CodeOf(err) != "backend.report.export_requester_mismatch" {
				t.Fatalf("requester err=%v", err)
			}
		})
	}
	invalidExpiry := validDownload
	invalidExpiry.Data = map[string]any{"file_reference": token, "audit_id": "audit-1", "expires_at": "invalid"}
	if _, _, err := reportExportEdgeService(&reportExportStoreStub{audit: validAudit, download: invalidExpiry}, &reportRecordExporterStub{}, []reportmodel.ReportExportControlSchema{control}, now).DownloadExport(t.Context(), token, principal); apperror.CodeOf(err) != "backend.report.export_download_expired" {
		t.Fatalf("expiry err=%v", err)
	}
	validAudit.Data["status"] = "completed"
	updateStore := &reportExportStoreStub{audit: recordmodel.Record{ID: "audit-1", Data: map[string]any{"report_key": "revenue", "requested_by_identity_user_id": principal.UserID, "status": "approved"}}, download: validDownload, updateErr: errors.New("update")}
	updateControl := control
	updateControl.RecordMapping.AuditPreparedStatus = "approved"
	if _, _, err := reportExportEdgeService(updateStore, &reportRecordExporterStub{}, []reportmodel.ReportExportControlSchema{updateControl}, now).DownloadExport(t.Context(), token, principal); !errors.Is(err, updateStore.updateErr) {
		t.Fatalf("update err=%v", err)
	}
	watermarked := control
	watermarked.Watermark = true
	if _, _, err := reportExportEdgeService(&reportExportStoreStub{audit: validAudit, download: validDownload}, &reportRecordExporterStub{}, []reportmodel.ReportExportControlSchema{watermarked}, now).DownloadExport(t.Context(), token, principal); err != nil {
		t.Fatalf("watermark err=%v", err)
	}
	missingReport := control
	missingReport.ReportKey = "missing"
	if _, _, err := reportExportEdgeService(&reportExportStoreStub{audit: validAudit, download: validDownload}, &reportRecordExporterStub{}, []reportmodel.ReportExportControlSchema{missingReport}, now).DownloadExport(t.Context(), token, principal); apperror.CodeOf(err) != "backend.report.export_scope_changed" {
		t.Fatalf("report access err=%v", err)
	}
	if content, _, err := reportExportEdgeService(&reportExportStoreStub{audit: validAudit, download: validDownload}, &reportRecordExporterStub{content: []byte("\"unterminated")}, []reportmodel.ReportExportControlSchema{watermarked}, now).DownloadExport(t.Context(), token, principal); err != nil || string(content) != "id\ncustomer-1\n" {
		t.Fatalf("persisted artifact download content=%q err=%v", content, err)
	}
}

func TestDownloadReportExportArtifactReauthorizationFailures(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	principal := reportPrincipal()
	token := strings.Repeat("c", 64)
	control := reportExportEdgeControl()
	newService := func() (*ReportApplicationService, *reportExportStoreStub, *reportExportArtifactStoreStub) {
		store := &reportExportStoreStub{
			audit:    recordmodel.Record{ID: "audit-1", Data: map[string]any{"report_key": "revenue", "requested_by_identity_user_id": principal.UserID, "status": "completed"}},
			download: recordmodel.Record{ID: "download-1", Data: map[string]any{"file_reference": token, "audit_id": "audit-1", "expires_at": now.Add(time.Hour).Format(time.RFC3339Nano)}},
		}
		service := reportExportEdgeService(store, &reportRecordExporterStub{}, []reportmodel.ReportExportControlSchema{control}, now)
		return service, store, service.exportArtifacts.(*reportExportArtifactStoreStub)
	}

	service, _, artifacts := newService()
	artifacts.readErr = errors.New("read artifact")
	if _, _, err := service.DownloadExport(t.Context(), token, principal); apperror.CodeOf(err) != "backend.internal_error" {
		t.Fatalf("read err=%v", err)
	}
	service, _, artifacts = newService()
	other := principal
	other.UserID = "other"
	if _, _, err := service.DownloadExport(t.Context(), token, other); apperror.CodeOf(err) != "backend.report.export_requester_mismatch" {
		t.Fatalf("requester err=%v", err)
	}
	service, _, artifacts = newService()
	artifacts.artifact.WorkspaceID = "other-workspace"
	artifacts.ignoreWorkspace = true
	if _, _, err := service.DownloadExport(t.Context(), token, principal); apperror.CodeOf(err) != "backend.report.export_requester_mismatch" {
		t.Fatalf("workspace err=%v", err)
	}
	service, _, artifacts = newService()
	artifacts.artifact.ExpiresAt = now.Add(-time.Second).Format(time.RFC3339Nano)
	if _, _, err := service.DownloadExport(t.Context(), token, principal); apperror.CodeOf(err) != "backend.report.export_download_expired" {
		t.Fatalf("expired err=%v", err)
	}
	service, _, artifacts = newService()
	artifacts.artifact.ReportKey = "missing"
	if _, _, err := service.DownloadExport(t.Context(), token, principal); apperror.CodeOf(err) != "backend.report.export_scope_changed" {
		t.Fatalf("report err=%v", err)
	}
	service, currentStore, _ := newService()
	service.domain = reportservice.NewReportDomainService(reportservice.ReportDependencies{Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema { return nil }})
	if _, _, err := service.DownloadExport(t.Context(), token, principal); apperror.CodeOf(err) != "backend.report.not_found" || currentStore.audit.Data["status"] != "denied" {
		t.Fatalf("current report permission status=%v err=%v", currentStore.audit.Data["status"], err)
	}
	service, currentStore, _ = newService()
	currentStore.transitionErr = errors.New("transition failed")
	service.domain = reportservice.NewReportDomainService(reportservice.ReportDependencies{Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema { return nil }})
	if _, _, err := service.DownloadExport(t.Context(), token, principal); apperror.CodeOf(err) != "backend.report.not_found" || currentStore.audit.Data["status"] != "completed" {
		t.Fatalf("failed transition status=%v err=%v", currentStore.audit.Data["status"], err)
	}
	service, _, _ = newService()
	service.exportControls = nil
	if _, _, err := service.DownloadExport(t.Context(), token, principal); apperror.CodeOf(err) != "backend.report.export_scope_changed" {
		t.Fatalf("control err=%v", err)
	}
	service, _, _ = newService()
	service.exportRecords = nil
	if _, _, err := service.DownloadExport(t.Context(), token, principal); apperror.CodeOf(err) != "backend.internal_error" {
		t.Fatalf("records err=%v", err)
	}
	service, _, artifacts = newService()
	emptyAuditControl := control
	emptyAuditControl.AuditObject = ""
	service.exportControls = func(context.Context, principalmodel.Principal) []reportmodel.ReportExportControlSchema {
		return []reportmodel.ReportExportControlSchema{emptyAuditControl}
	}
	controlHash, _ := reportexport.CanonicalJSONSHA256(emptyAuditControl)
	artifacts.artifact.ControlDefinitionSHA256 = controlHash
	if _, _, err := service.DownloadExport(t.Context(), token, principal); apperror.CodeOf(err) != "backend.internal_error" {
		t.Fatalf("empty audit object err=%v", err)
	}
	service, store, _ := newService()
	store.audit.Data["report_key"] = "changed"
	if _, _, err := service.DownloadExport(t.Context(), token, principal); apperror.CodeOf(err) != "backend.report.export_audit_report_mismatch" {
		t.Fatalf("audit report err=%v", err)
	}
	service, store, _ = newService()
	store.audit.Data["requested_by_identity_user_id"] = "changed"
	if _, _, err := service.DownloadExport(t.Context(), token, principal); apperror.CodeOf(err) != "backend.report.export_requester_mismatch" {
		t.Fatalf("audit requester err=%v", err)
	}
	service, store, _ = newService()
	store.audit.Data["status"] = "revoked"
	if _, _, err := service.DownloadExport(t.Context(), token, principal); apperror.CodeOf(err) != "backend.report.export_audit_status_invalid" {
		t.Fatalf("audit status err=%v", err)
	}
	service, store, _ = newService()
	preparedControl := control
	preparedControl.RecordMapping.AuditPreparedStatus = "approved"
	service.exportControls = func(context.Context, principalmodel.Principal) []reportmodel.ReportExportControlSchema {
		return []reportmodel.ReportExportControlSchema{preparedControl}
	}
	preparedControlHash, _ := reportexport.CanonicalJSONSHA256(preparedControl)
	service.exportArtifacts.(*reportExportArtifactStoreStub).artifact.ControlDefinitionSHA256 = preparedControlHash
	store.audit.Data["status"] = "approved"
	if _, _, err := service.DownloadExport(t.Context(), token, principal); err != nil || store.audit.Data["status"] != "completed" {
		t.Fatalf("status=%v err=%v", store.audit.Data["status"], err)
	}
	service, _, _ = newService()
	service.audit = &reportAuditAppenderStub{err: errors.New("audit")}
	if _, _, err := service.DownloadExport(t.Context(), token, principal); apperror.CodeOf(err) != "backend.internal_error" {
		t.Fatalf("download audit err=%v", err)
	}
}

func TestExecuteScopedSnapshotFreshnessBranches(t *testing.T) {
	principal := reportPrincipal()
	reportDefinition := reportmodel.ReportSchema{Key: "revenue", Dataset: reportmodel.ReportDatasetSchema{Source: reportmodel.ReportDatasetSource{ObjectKey: "customer", Alias: "customer"}}, Materialization: &reportmodel.ReportMaterializationPolicy{MaximumLagSeconds: 60}}
	snapshot := reportmodel.ReportSnapshot{ID: "snapshot-1", ReportKey: "revenue", WorkspaceID: principal.WorkspaceID, Status: "succeeded", Summary: reportmodel.ReportSummary{Rows: []reportmodel.ReportResultRow{{Dimensions: map[string]string{"id": "one"}}}, RowCount: 1, SourceRowCount: 1}, RefreshedAt: "2026-08-01T11:59:50Z"}
	store := &reportSnapshotStoreStub{latest: snapshot, found: true}
	domain := reportservice.NewReportDomainService(reportservice.ReportDependencies{Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
		return []reportmodel.ReportSchema{reportDefinition}
	}, Snapshots: store, Clock: func() time.Time { return time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC) }})
	service := NewReportApplicationService(ReportApplicationDependencies{Domain: domain})
	for name, freshness := range map[string]reportmodel.ReportExportFreshness{
		"snapshot id": {Mode: "snapshot", SnapshotID: "other"},
		"lag":         {Mode: "snapshot", MaximumLagSeconds: 1},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.executeScopedExport(t.Context(), reportDefinition, reportDefinition, reportmodel.ReportExportScopeRequest{Freshness: freshness}, principal); apperror.CodeOf(err) != "backend.report.export_freshness_not_satisfied" {
				t.Fatalf("err=%v", err)
			}
		})
	}
	if summary, err := service.executeScopedExport(t.Context(), reportDefinition, reportDefinition, reportmodel.ReportExportScopeRequest{Freshness: reportmodel.ReportExportFreshness{Mode: "snapshot", SnapshotID: "snapshot-1", MaximumLagSeconds: 20}}, principal); err != nil || summary.Snapshot == nil {
		t.Fatalf("snapshot summary=%#v err=%v", summary, err)
	}
	store.found = false
	if _, err := service.executeScopedExport(t.Context(), reportDefinition, reportDefinition, reportmodel.ReportExportScopeRequest{Freshness: reportmodel.ReportExportFreshness{Mode: "snapshot"}}, principal); apperror.CodeOf(err) != "backend.report.snapshot_not_found" {
		t.Fatalf("missing snapshot err=%v", err)
	}
	if result := reportExportDownloadFromArtifact(reportmodel.ReportExportArtifact{ID: "artifact"}); result.ID != "artifact" || result.ArtifactID != "artifact" {
		t.Fatalf("fallback identity=%#v", result)
	}
	if reportExportSnapshotFreshnessSatisfied(reportmodel.ReportSummary{}, reportmodel.ReportExportFreshness{}) {
		t.Fatal("nil snapshot satisfied freshness")
	}
	analysisReport := reportDefinition
	analysisReport.Dataset.Dimensions = []reportmodel.ReportDatasetDimension{{Key: "id", Field: reportmodel.ReportDatasetField{SourceAlias: "customer", FieldKey: "id"}}}
	analysisReport.Dataset.Analyses = []reportmodel.ReportDatasetAnalysis{{Key: "funnel", Type: "cohort_retention", EntityField: reportmodel.ReportDatasetField{SourceAlias: "customer", FieldKey: "id"}, TimeField: reportmodel.ReportDatasetField{SourceAlias: "customer", FieldKey: "created_at"}}}
	store.latest, store.found = snapshot, true
	domain = reportservice.NewReportDomainService(reportservice.ReportDependencies{
		Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
			return []reportmodel.ReportSchema{analysisReport}
		},
		Access: reportExportDatasetAccessStub{}, Records: reportExportDatasetRecordsStub{}, Snapshots: store, Clock: func() time.Time { return time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC) },
	})
	legacy := &reportExportStoreStub{audit: recordmodel.Record{ID: "audit-1", Data: map[string]any{"report_key": "revenue", "requested_by_identity_user_id": principal.UserID, "status": "completed"}}}
	service = NewReportApplicationService(ReportApplicationDependencies{
		Domain: domain, Records: &reportRecordExporterStub{}, ExportRecords: legacy, ExportArtifacts: &reportExportArtifactStoreStub{}, Audit: &reportAuditAppenderStub{},
		ExportControls: func(context.Context, principalmodel.Principal) []reportmodel.ReportExportControlSchema {
			return []reportmodel.ReportExportControlSchema{reportExportEdgeControl()}
		}, Clock: func() time.Time { return time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC) },
	})
	request := reportmodel.ReportExportScopeRequest{Purpose: "snapshot analysis", AnalysisKey: "funnel", FieldProjection: []string{"id"}, Freshness: reportmodel.ReportExportFreshness{Mode: "snapshot", SnapshotID: "snapshot-1"}}
	if _, err := service.PrepareExportScoped(t.Context(), "revenue", "customer", "audit-1", "snapshot-analysis", request, principal); apperror.CodeOf(err) != "backend.report.export_analysis_not_allowed" {
		t.Fatalf("snapshot analysis rows err=%v", err)
	}
}

func TestReportExportHelperBranches(t *testing.T) {
	service := &ReportApplicationService{exportControls: func(context.Context, principalmodel.Principal) []reportmodel.ReportExportControlSchema { return nil }}
	if _, _, _, err := service.findExportDownload(t.Context(), "token", reportPrincipal()); apperror.CodeOf(err) != "backend.internal_error" {
		t.Fatalf("missing export record store err=%v", err)
	}
	if err := (&ReportApplicationService{}).auditExportRequired(t.Context(), "event", "object", reportPrincipal(), "audit", nil); err == nil {
		t.Fatal("missing required audit accepted")
	}
	if source := firstReportExportSource(reportmodel.ReportExportControlSchema{}); source != "" {
		t.Fatalf("source=%q", source)
	}
	if source := firstReportExportSource(reportmodel.ReportExportControlSchema{SourceObjects: []string{" customer "}}); source != "customer" {
		t.Fatalf("source=%q", source)
	}
	for _, test := range []struct {
		value any
		want  time.Duration
	}{
		{value: float64(60), want: time.Minute}, {value: float64(59), want: reportExportDownloadTTL}, {value: float64(86401), want: reportExportDownloadTTL},
		{value: 60, want: time.Minute}, {value: 59, want: reportExportDownloadTTL}, {value: 86401, want: reportExportDownloadTTL}, {value: "60", want: reportExportDownloadTTL},
	} {
		if got := reportExportTTL(reportmodel.ReportExportControlSchema{Config: map[string]any{"download_ttl_seconds": test.value}}); got != test.want {
			t.Fatalf("value=%v ttl=%v want=%v", test.value, got, test.want)
		}
	}
	if content, err := reportexport.ApplyWatermark(nil, "mark", "expires"); err != nil || len(content) != 0 {
		t.Fatalf("empty watermark=%q err=%v", content, err)
	}
	if _, err := reportexport.ApplyWatermark([]byte("\"unterminated"), "mark", "expires"); err == nil {
		t.Fatal("invalid csv accepted")
	}
	var nilService *ReportApplicationService
	if _, ok := nilService.exportControl(t.Context(), "report", "object", reportPrincipal()); ok {
		t.Fatal("nil service returned export control")
	}
	nilService.auditExport(t.Context(), "event", "object", reportPrincipal(), "audit", nil)
	if err := nilService.auditExportRequired(t.Context(), "event", "object", reportPrincipal(), "audit", nil); err == nil {
		t.Fatal("missing mandatory audit accepted")
	}
	(&ReportApplicationService{}).auditExport(t.Context(), "event", "object", reportPrincipal(), "audit", nil)
	controls := []reportmodel.ReportExportControlSchema{
		{ReportKey: "other", SourceObjects: []string{"customer"}},
		{ReportKey: "revenue", SourceObjects: []string{"other", "customer"}},
	}
	controlService := &ReportApplicationService{exportControls: func(context.Context, principalmodel.Principal) []reportmodel.ReportExportControlSchema {
		return controls
	}}
	if _, ok := controlService.exportControl(t.Context(), "revenue", "customer", reportPrincipal()); !ok {
		t.Fatal("matching control not found")
	}
	store := &reportExportStoreStub{}
	if _, _, _, err := (&ReportApplicationService{exportRecords: store}).findExportDownload(t.Context(), strings.Repeat("a", 64), reportPrincipal()); apperror.CodeOf(err) != "backend.internal_error" {
		t.Fatalf("nil controls err=%v", err)
	}
	duplicateControls := []reportmodel.ReportExportControlSchema{
		{DownloadObject: ""}, {DownloadObject: "download"}, {DownloadObject: "download"},
	}
	service = &ReportApplicationService{exportRecords: store, exportControls: func(context.Context, principalmodel.Principal) []reportmodel.ReportExportControlSchema {
		return duplicateControls
	}}
	if _, _, found, err := service.findExportDownload(t.Context(), strings.Repeat("b", 64), reportPrincipal()); err != nil || found {
		t.Fatalf("duplicate lookup found=%v err=%v", found, err)
	}
	service = &ReportApplicationService{exportRecords: &reportExportStoreStub{listErr: errors.New("list")}, exportControls: func(context.Context, principalmodel.Principal) []reportmodel.ReportExportControlSchema {
		return []reportmodel.ReportExportControlSchema{{DownloadObject: "report_export_download"}}
	}}
	if _, _, _, err := service.findExportDownload(t.Context(), strings.Repeat("c", 64), reportPrincipal()); err == nil {
		t.Fatal("list failure was ignored")
	}
	matching := &reportExportStoreStub{download: recordmodel.Record{ID: "download", Data: map[string]any{"file_reference": strings.Repeat("d", 64)}}}
	service = &ReportApplicationService{exportRecords: matching, exportControls: func(context.Context, principalmodel.Principal) []reportmodel.ReportExportControlSchema {
		return []reportmodel.ReportExportControlSchema{{DownloadObject: "report_export_download"}}
	}}
	if _, record, found, err := service.findExportDownload(t.Context(), strings.Repeat("d", 64), reportPrincipal()); err != nil || !found || record.ID != "download" {
		t.Fatalf("matching record=%#v found=%v err=%v", record, found, err)
	}
}
