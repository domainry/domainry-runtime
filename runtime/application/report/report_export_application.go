package report

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	reportexport "github.com/domainry/domainry-runtime/runtime/application/report/export"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	reportservice "github.com/domainry/domainry-runtime/runtime/domain/report/service"
)

var reportExportRandomRead = rand.Read
var reportExportRows = reportexport.Rows

type ReportExportDownload struct {
	// ID is the project-owned DownloadObject record identity used by business
	// clients. ArtifactID is the Runtime-owned immutable artifact identity.
	// They are intentionally distinct and must never share one response field.
	ID            string                               `json:"id"`
	ArtifactID    string                               `json:"artifact_id"`
	ReportKey     string                               `json:"report_key"`
	ObjectKey     string                               `json:"object_key"`
	Filename      string                               `json:"filename"`
	Token         string                               `json:"token"`
	ExpiresAt     string                               `json:"expires_at"`
	ContentSHA256 string                               `json:"content_sha256"`
	RowCount      int                                  `json:"row_count"`
	Scope         reportmodel.ReportExportScopeRequest `json:"scope"`
	Watermark     bool                                 `json:"watermarked"`
}

// PrepareExport preserves the pre-scope application call for internal callers.
// It still enters the governed path with an explicit immutable legacy purpose.
func (s *ReportApplicationService) PrepareExport(ctx context.Context, reportKey, objectKey, auditID, idempotencyKey string, principal principalmodel.Principal) (ReportExportDownload, error) {
	return s.PrepareExportScoped(ctx, reportKey, objectKey, auditID, idempotencyKey, reportmodel.ReportExportScopeRequest{Purpose: "legacy governed report export", Freshness: reportmodel.ReportExportFreshness{Mode: "realtime"}}, principal)
}

func (s *ReportApplicationService) PrepareExportScoped(ctx context.Context, reportKey, objectKey, auditID, idempotencyKey string, scopeRequest reportmodel.ReportExportScopeRequest, principal principalmodel.Principal) (ReportExportDownload, error) {
	if _, err := principalmodel.CommandScopeForPrincipal(principal); err != nil {
		return ReportExportDownload{}, reportWorkspaceError(err)
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		return ReportExportDownload{}, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.idempotency.key_required"}
	}
	if s.exportRecords == nil || s.exportArtifacts == nil {
		return ReportExportDownload{}, reportApplicationError(nil)
	}
	report, err := s.domain.ReportForExport(ctx, reportKey, objectKey, principal)
	if err != nil {
		return ReportExportDownload{}, err
	}
	control, ok := s.exportControl(ctx, report.Key, objectKey, principal)
	if !ok {
		return ReportExportDownload{}, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.report.export_control_not_found"}
	}
	auditID = strings.TrimSpace(auditID)
	auditRecord, err := s.exportRecords.GetReportRecord(ctx, control.AuditObject, auditID, principal)
	if err != nil {
		return ReportExportDownload{}, err
	}
	mapping := control.RecordMapping
	if strings.TrimSpace(fmt.Sprint(auditRecord.Data[mapping.AuditReportKeyField])) != report.Key {
		return ReportExportDownload{}, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.report.export_audit_report_mismatch"}
	}
	requester := strings.TrimSpace(fmt.Sprint(auditRecord.Data[mapping.AuditRequesterField]))
	if requester == "" || requester != strings.TrimSpace(principal.UserID) {
		s.auditExport(ctx, "report_export_prepare_denied", objectKey, principal, auditID, map[string]any{"report_key": report.Key, "reason": "requester_mismatch"})
		return ReportExportDownload{}, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.report.export_requester_mismatch"}
	}
	status := strings.TrimSpace(fmt.Sprint(auditRecord.Data[mapping.AuditStatusField]))
	if !reportExportStatusAllowed(status, mapping.AuditPreparedStatuses) {
		reason, code := "audit_status_invalid", "backend.report.export_audit_status_invalid"
		if control.ApprovalRequired {
			reason, code = "approval_required", "backend.report.export_approval_required"
		}
		s.auditExport(ctx, "report_export_prepare_denied", objectKey, principal, auditID, map[string]any{"report_key": report.Key, "reason": reason, "status": status})
		return ReportExportDownload{}, &apperror.AppError{Kind: apperror.KindForbidden, Code: code}
	}

	scope, scopedReport, maskedDimensions, err := reportexport.NormalizeScope(report, objectKey, control, scopeRequest, principal)
	if err != nil {
		return s.denyReportExportPrepare(ctx, control, auditRecord, principal, report.Key, apperror.CodeOf(err), err)
	}
	maskedDimensions, err = reportexport.ValidateFieldAccess(ctx, s.domain, scopedReport, control, principal)
	if err != nil {
		return s.denyReportExportPrepare(ctx, control, auditRecord, principal, report.Key, apperror.CodeOf(err), err)
	}
	summary, err := s.executeScopedExport(ctx, report, scopedReport, scope, principal)
	if err != nil {
		return ReportExportDownload{}, err
	}
	rows, err := reportExportRows(summary, scope.AnalysisKey)
	if err != nil {
		return ReportExportDownload{}, err
	}
	if control.MaxRows > 0 && len(rows) > control.MaxRows {
		cause := &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.report.export_too_many_rows", Params: map[string]string{"limit": fmt.Sprint(control.MaxRows)}}
		return s.denyReportExportPrepare(ctx, control, auditRecord, principal, report.Key, "too_many_rows", cause)
	}
	now := s.clock().UTC()
	expiresAt := now.Add(reportExportTTL(control)).Format(time.RFC3339Nano)
	content, _ := reportexport.EncodeCSV(rows, scope.FieldProjection, maskedDimensions)
	if control.Watermark {
		watermark := s.reportExportWatermark(report.Key, requester, expiresAt)
		content, _ = reportexport.ApplyWatermark(content, watermark, expiresAt)
	}
	contentHash := reportexport.SHA256Hex(content)
	scopeHash, _ := reportexport.CanonicalJSONSHA256(scope)
	parametersHash, _ := reportexport.CanonicalJSONSHA256(scope.Parameters)
	authorizationHash, _ := reportservice.ReportAccessScopeHash(principal)
	reportHash, _ := reportexport.CanonicalJSONSHA256(report)
	controlHash, _ := reportexport.CanonicalJSONSHA256(control)

	token, err := newReportExportToken()
	if err != nil {
		return ReportExportDownload{}, reportApplicationError(err)
	}
	filename := reportexport.SafeFilename(report.Key, objectKey)
	artifact, created, err := s.exportArtifacts.CreateOrGetReportExportArtifact(ctx, reportmodel.ReportExportArtifact{
		WorkspaceID: principal.WorkspaceID, ReportKey: report.Key, ObjectKey: strings.TrimSpace(objectKey), AuditID: auditID,
		RequesterUserID: requester, RoleKey: principal.RoleKey, IdempotencyKey: strings.TrimSpace(idempotencyKey), Token: token, Filename: filename,
		Scope: scope, ScopeSHA256: scopeHash, AuthorizationScopeSHA256: authorizationHash, ReportDefinitionSHA256: reportHash, ControlDefinitionSHA256: controlHash,
		ContentSHA256: contentHash, RowCount: len(rows), Content: content, Watermarked: control.Watermark, CreatedAt: now.Format(time.RFC3339Nano), ExpiresAt: expiresAt,
	})
	if errors.Is(err, reportcontract.ErrReportExportIdempotencyConflict) {
		return ReportExportDownload{}, &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.idempotency.key_conflict", Err: err}
	}
	if err != nil {
		return ReportExportDownload{}, reportApplicationError(err)
	}
	return s.completeReportExportArtifact(ctx, artifact, created, control, auditRecord, parametersHash, idempotencyKey, principal)
}

func (s *ReportApplicationService) reportExportWatermark(reportKey, requester, expiresAt string) string {
	return fmt.Sprintf("%s governed export | report=%s | requester=%s | expires_at=%s", s.productBrandName, reportKey, requester, expiresAt)
}

// completeReportExportArtifact is replay-safe across every post-artifact crash
// window. Audit status, business download creation, artifact linking, and the
// mandatory audit fact each carry deterministic idempotency identities, so a
// retry repairs missing later steps without creating a second download.
func (s *ReportApplicationService) completeReportExportArtifact(ctx context.Context, artifact reportmodel.ReportExportArtifact, _ bool, control reportmodel.ReportExportControlSchema, auditRecord recordmodel.Record, parametersHash, idempotencyKey string, principal principalmodel.Principal) (ReportExportDownload, error) {
	mapping := control.RecordMapping
	if artifact.BusinessDownloadID == "" {
		if _, updateErr := s.exportRecords.UpdateReportRecord(ctx, control.AuditObject, auditRecord.ID, map[string]any{
			mapping.AuditStatusField:    mapping.AuditPreparedStatus,
			mapping.AuditRowCountField:  artifact.RowCount,
			mapping.AuditScopeHashField: "sha256:" + artifact.ScopeSHA256,
		}, "report-export-prepared:"+artifact.ID, principal); updateErr != nil {
			return ReportExportDownload{}, updateErr
		}
		downloadData := map[string]any{
			mapping.DownloadAuditField:       artifact.AuditID,
			mapping.DownloadFilenameField:    artifact.Filename,
			mapping.DownloadContentHashField: "sha256:" + artifact.ContentSHA256,
			mapping.DownloadExpiresAtField:   artifact.ExpiresAt,
		}
		if mapping.DownloadTokenField != "" {
			downloadData[mapping.DownloadTokenField] = artifact.Token
		}
		if mapping.DownloadWatermarkedField != "" {
			downloadData[mapping.DownloadWatermarkedField] = artifact.Watermarked
		}
		if mapping.DownloadNumberField != "" {
			downloadData[mapping.DownloadNumberField] = "EXPORT-" + strings.ToUpper(artifact.Token[:16])
		}
		downloadRecord, err := s.exportRecords.CreateReportRecord(ctx, control.DownloadObject, downloadData, idempotencyKey, principal)
		if err != nil {
			return ReportExportDownload{}, err
		}
		artifact.BusinessDownloadID = downloadRecord.ID
		if err := s.exportArtifacts.LinkReportExportBusinessDownload(ctx, principal.WorkspaceID, artifact.ID, downloadRecord.ID); err != nil {
			return ReportExportDownload{}, reportApplicationError(err)
		}
	}
	if err := s.auditExportRequiredIdempotent(ctx, "report-export-download-prepared:"+artifact.ID, "report_export_download_prepared", artifact.ObjectKey, principal, artifact.AuditID, map[string]any{
		"report_key": artifact.ReportKey, "download_id": artifact.BusinessDownloadID, "artifact_id": artifact.ID, "expires_at": artifact.ExpiresAt,
		"watermarked": artifact.Watermarked, "content_sha256": artifact.ContentSHA256, "row_count": artifact.RowCount, "scope_sha256": artifact.ScopeSHA256, "parameters_sha256": parametersHash,
	}); err != nil {
		return ReportExportDownload{}, reportApplicationError(err)
	}
	return reportExportDownloadFromArtifact(artifact), nil
}

func (s *ReportApplicationService) DownloadExport(ctx context.Context, token string, principal principalmodel.Principal) ([]byte, string, error) {
	if _, err := principalmodel.QueryScopeForPrincipal(principal); err != nil {
		return nil, "", reportWorkspaceError(err)
	}
	token = strings.TrimSpace(token)
	if len(token) != 64 || s.exportArtifacts == nil {
		s.auditExport(ctx, "report_export_download_denied", "", principal, "", map[string]any{"reason": "not_found"})
		return nil, "", &apperror.AppError{Kind: apperror.KindNotFound, Code: "backend.report.export_download_not_found"}
	}
	artifact, found, err := s.exportArtifacts.ReportExportArtifactByToken(ctx, principal.WorkspaceID, token)
	if err != nil {
		return nil, "", reportApplicationError(err)
	}
	if !found {
		s.auditExport(ctx, "report_export_download_denied", "", principal, "", map[string]any{"reason": "not_found"})
		return nil, "", &apperror.AppError{Kind: apperror.KindNotFound, Code: "backend.report.export_download_not_found"}
	}
	control, ok := s.exportControl(ctx, artifact.ReportKey, artifact.ObjectKey, principal)
	if !ok {
		s.auditExport(ctx, "report_export_download_denied", artifact.ObjectKey, principal, artifact.AuditID, map[string]any{"report_key": artifact.ReportKey, "artifact_id": artifact.ID, "reason": "control_missing"})
		return nil, "", &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.report.export_scope_changed"}
	}
	deny := func(code, reason string) ([]byte, string, error) {
		event := "report_export_download_denied"
		status := control.RecordMapping.AuditDeniedStatus
		if reason == "expired" {
			event = "report_export_download_expired"
			status = control.RecordMapping.AuditExpiredStatus
		}
		if strings.TrimSpace(status) != "" && s.exportRecords != nil {
			_ = s.exportRecords.TransitionReportExportAuditStatus(ctx, artifact.WorkspaceID, control.AuditObject, artifact.AuditID, control.RecordMapping.AuditStatusField, control.RecordMapping.AuditPreparedStatus, status)
		}
		s.auditExport(ctx, event, artifact.ObjectKey, principal, artifact.AuditID, map[string]any{"report_key": artifact.ReportKey, "artifact_id": artifact.ID, "reason": reason})
		return nil, "", &apperror.AppError{Kind: apperror.KindForbidden, Code: code}
	}
	if artifact.WorkspaceID != strings.TrimSpace(principal.WorkspaceID) || artifact.RequesterUserID != strings.TrimSpace(principal.UserID) {
		return deny("backend.report.export_requester_mismatch", "requester_or_workspace_mismatch")
	}
	expiresAt, parseErr := time.Parse(time.RFC3339Nano, artifact.ExpiresAt)
	if parseErr != nil || !s.clock().UTC().Before(expiresAt) {
		return deny("backend.report.export_download_expired", "expired")
	}
	report, err := s.domain.ReportForExport(ctx, artifact.ReportKey, artifact.ObjectKey, principal)
	if err != nil {
		return deny(apperror.CodeOf(err), "current_report_permission")
	}
	if err := reportexport.ValidateCurrentArtifact(ctx, s.domain, report, control, artifact, principal); err != nil {
		return deny(apperror.CodeOf(err), apperror.CodeOf(err))
	}
	if reportexport.SHA256Hex(artifact.Content) != artifact.ContentSHA256 {
		return deny("backend.report.export_integrity_failed", "content_hash_mismatch")
	}
	if s.exportRecords == nil || strings.TrimSpace(control.AuditObject) == "" {
		return nil, "", reportApplicationError(nil)
	}
	auditRecord, getErr := s.exportRecords.GetReportRecord(ctx, control.AuditObject, artifact.AuditID, principal)
	if getErr != nil {
		return nil, "", getErr
	}
	mapping := control.RecordMapping
	if strings.TrimSpace(fmt.Sprint(auditRecord.Data[mapping.AuditReportKeyField])) != artifact.ReportKey {
		return deny("backend.report.export_audit_report_mismatch", "audit_report_changed")
	}
	if strings.TrimSpace(fmt.Sprint(auditRecord.Data[mapping.AuditRequesterField])) != artifact.RequesterUserID {
		return deny("backend.report.export_requester_mismatch", "audit_requester_changed")
	}
	status := strings.TrimSpace(fmt.Sprint(auditRecord.Data[mapping.AuditStatusField]))
	if status != mapping.AuditDownloadedStatus && status != mapping.AuditPreparedStatus {
		return deny("backend.report.export_audit_status_invalid", "audit_status_changed")
	}
	if status != mapping.AuditDownloadedStatus {
		if _, updateErr := s.exportRecords.UpdateReportRecord(ctx, control.AuditObject, auditRecord.ID, map[string]any{mapping.AuditStatusField: mapping.AuditDownloadedStatus}, "report-export-complete:"+auditRecord.ID, principal); updateErr != nil {
			return nil, "", updateErr
		}
	}
	if err := s.auditExportRequired(ctx, "report_export_downloaded", artifact.ObjectKey, principal, artifact.AuditID, map[string]any{
		"report_key": artifact.ReportKey, "download_id": artifact.BusinessDownloadID, "artifact_id": artifact.ID, "expires_at": artifact.ExpiresAt,
		"watermarked": artifact.Watermarked, "content_sha256": artifact.ContentSHA256, "row_count": artifact.RowCount, "scope_sha256": artifact.ScopeSHA256, "parameters_sha256": reportExportParametersSHA256(artifact.Scope.Parameters),
	}); err != nil {
		return nil, "", reportApplicationError(err)
	}
	return append([]byte(nil), artifact.Content...), artifact.Filename, nil
}

func reportExportParametersSHA256(parameters map[string]any) string {
	hash, _ := reportexport.CanonicalJSONSHA256(parameters)
	return hash
}

func reportExportStatusAllowed(status string, allowed []string) bool {
	status = strings.TrimSpace(status)
	for _, value := range allowed {
		if status == strings.TrimSpace(value) {
			return true
		}
	}
	return false
}

func (s *ReportApplicationService) denyReportExportPrepare(ctx context.Context, control reportmodel.ReportExportControlSchema, auditRecord recordmodel.Record, principal principalmodel.Principal, reportKey, reason string, cause error) (ReportExportDownload, error) {
	mapping := control.RecordMapping
	s.auditExport(ctx, "report_export_prepare_denied", firstReportExportSource(control), principal, auditRecord.ID, map[string]any{"report_key": reportKey, "reason": reason})
	if strings.TrimSpace(mapping.AuditDeniedStatus) != "" {
		if _, updateErr := s.exportRecords.UpdateReportRecord(ctx, control.AuditObject, auditRecord.ID, map[string]any{mapping.AuditStatusField: mapping.AuditDeniedStatus}, "report-export-denied:"+auditRecord.ID+":"+reason, principal); updateErr != nil {
			return ReportExportDownload{}, updateErr
		}
	}
	return ReportExportDownload{}, cause
}

func (s *ReportApplicationService) executeScopedExport(ctx context.Context, original, scoped reportmodel.ReportSchema, scope reportmodel.ReportExportScopeRequest, principal principalmodel.Principal) (reportmodel.ReportSummary, error) {
	if scope.Freshness.Mode == "snapshot" {
		summary, err := s.domain.SummaryMode(ctx, original.Key, "snapshot", principal)
		if err != nil {
			return reportmodel.ReportSummary{}, err
		}
		if !reportExportSnapshotFreshnessSatisfied(summary, scope.Freshness) {
			return reportmodel.ReportSummary{}, &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_freshness_not_satisfied"}
		}
		return summary, nil
	}
	return s.domain.ExecuteExportReport(ctx, scoped, scope.Parameters, principal)
}

func reportExportSnapshotFreshnessSatisfied(summary reportmodel.ReportSummary, freshness reportmodel.ReportExportFreshness) bool {
	return summary.Snapshot != nil &&
		(freshness.SnapshotID == "" || freshness.SnapshotID == summary.Snapshot.SnapshotID) &&
		(freshness.MaximumLagSeconds <= 0 || summary.Snapshot.LagSeconds <= freshness.MaximumLagSeconds)
}

func reportExportDownloadFromArtifact(artifact reportmodel.ReportExportArtifact) ReportExportDownload {
	id := artifact.BusinessDownloadID
	if id == "" {
		id = artifact.ID
	}
	return ReportExportDownload{ID: id, ArtifactID: artifact.ID, ReportKey: artifact.ReportKey, ObjectKey: artifact.ObjectKey, Filename: artifact.Filename, Token: artifact.Token, ExpiresAt: artifact.ExpiresAt, ContentSHA256: artifact.ContentSHA256, RowCount: artifact.RowCount, Scope: artifact.Scope, Watermark: artifact.Watermarked}
}

func (s *ReportApplicationService) exportControl(ctx context.Context, reportKey, objectKey string, principal principalmodel.Principal) (reportmodel.ReportExportControlSchema, bool) {
	if s == nil || s.exportControls == nil {
		return reportmodel.ReportExportControlSchema{}, false
	}
	for _, control := range s.exportControls(ctx, principal) {
		if strings.TrimSpace(control.ReportKey) != strings.TrimSpace(reportKey) {
			continue
		}
		for _, source := range control.SourceObjects {
			if strings.TrimSpace(source) == strings.TrimSpace(objectKey) {
				return control, true
			}
		}
	}
	return reportmodel.ReportExportControlSchema{}, false
}

// findExportDownload remains as a compatibility helper for the legacy
// project-owned download handoff. Governed GET download never trusts it as the
// artifact store; Runtime-owned report_export_artifacts is authoritative.
func (s *ReportApplicationService) findExportDownload(ctx context.Context, token string, principal principalmodel.Principal) (reportmodel.ReportExportControlSchema, recordmodel.Record, bool, error) {
	if s.exportRecords == nil || s.exportControls == nil {
		return reportmodel.ReportExportControlSchema{}, recordmodel.Record{}, false, reportApplicationError(nil)
	}
	seen := map[string]bool{}
	for _, control := range s.exportControls(ctx, principal) {
		objectKey := strings.TrimSpace(control.DownloadObject)
		if objectKey == "" || seen[objectKey] {
			continue
		}
		seen[objectKey] = true
		page, err := s.exportRecords.ListReportRecordsForPrincipal(ctx, objectKey, recordmodel.RecordListQuery{Page: 1, PageSize: 2, Filters: map[string]any{"file_reference": token}}, principal)
		if err != nil {
			return reportmodel.ReportExportControlSchema{}, recordmodel.Record{}, false, err
		}
		if len(page.Items) == 1 {
			return control, page.Items[0], true, nil
		}
	}
	return reportmodel.ReportExportControlSchema{}, recordmodel.Record{}, false, nil
}

func firstReportExportSource(control reportmodel.ReportExportControlSchema) string {
	if len(control.SourceObjects) == 0 {
		return ""
	}
	return strings.TrimSpace(control.SourceObjects[0])
}

func reportExportTTL(control reportmodel.ReportExportControlSchema) time.Duration {
	if seconds, ok := control.Config["download_ttl_seconds"].(float64); ok && seconds >= 60 && seconds <= 86400 {
		return time.Duration(seconds) * time.Second
	}
	if seconds, ok := control.Config["download_ttl_seconds"].(int); ok && seconds >= 60 && seconds <= 86400 {
		return time.Duration(seconds) * time.Second
	}
	return reportExportDownloadTTL
}

func newReportExportToken() (string, error) {
	value := make([]byte, 32)
	if _, err := reportExportRandomRead(value); err != nil {
		return "", err
	}
	return hex.EncodeToString(value), nil
}
