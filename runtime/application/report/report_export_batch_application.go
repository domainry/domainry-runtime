package report

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	reportexport "github.com/domainry/domainry-runtime/runtime/application/report/export"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	reportservice "github.com/domainry/domainry-runtime/runtime/domain/report/service"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

const reportExportBatchKind = "report_export"
const reportExportAsyncThreshold = 1000

type reportExportBatchPayload struct {
	WorkspaceID              string                                  `json:"workspace_id"`
	RequesterUserID          string                                  `json:"requester_user_id"`
	ReportKey                string                                  `json:"report_key"`
	ObjectKey                string                                  `json:"object_key"`
	AuditID                  string                                  `json:"audit_id"`
	ArtifactIdempotencyKey   string                                  `json:"artifact_idempotency_key"`
	Scope                    reportmodel.ReportExportScopeRequest    `json:"scope"`
	ReportDefinitionSHA256   string                                  `json:"report_definition_sha256"`
	ReportSourceSHA256       string                                  `json:"report_source_sha256"`
	AuthorizationScopeSHA256 string                                  `json:"authorization_scope_sha256"`
	SourceVersion            reportmodel.ReportSnapshotSourceVersion `json:"source_version"`
	SourceVersionSHA256      string                                  `json:"source_version_sha256"`
	ResultSHA256             string                                  `json:"result_sha256"`
	CSVContentSHA256         string                                  `json:"csv_content_sha256"`
	ExactTotal               int                                     `json:"exact_total"`
	MaxRows                  int                                     `json:"max_rows"`
	Format                   string                                  `json:"format"`
}

type ReportExportPreparation struct {
	Async    bool
	Download ReportExportDownload
	Job      ReportExportJob
}

func (s *ReportApplicationService) PrepareExportRouted(ctx context.Context, reportKey, objectKey, auditID, idempotencyKey string, scope reportmodel.ReportExportScopeRequest, principal principalmodel.Principal) (ReportExportPreparation, error) {
	payload, err := s.prepareReportExportPayload(ctx, reportKey, objectKey, auditID, idempotencyKey, scope, principal)
	if err != nil {
		return ReportExportPreparation{}, err
	}
	fingerprint := reportExportRequestFingerprint(payload)
	if !reportExportRoutesAsync(payload.ExactTotal) {
		artifactKey, err := s.reportExportSynchronousReplayKey(ctx, payload.ReportKey, fingerprint, principal)
		if err != nil {
			return ReportExportPreparation{}, err
		}
		download, err := s.PrepareExportScoped(ctx, payload.ReportKey, payload.ObjectKey, payload.AuditID, artifactKey, payload.Scope, principal)
		return ReportExportPreparation{Download: download}, err
	}
	job, err := s.prepareExportJobFromPayload(ctx, payload, fingerprint, principal)
	return ReportExportPreparation{Async: true, Job: job}, err
}

func (s *ReportApplicationService) reportExportSynchronousReplayKey(ctx context.Context, reportKey, fingerprint string, principal principalmodel.Principal) (string, error) {
	reader, ok := s.exportArtifacts.(reportcontract.ReportExportArtifactRequestReader)
	if !ok {
		return fingerprint, nil
	}
	key := fingerprint
	for generation := 0; generation < 32; generation++ {
		artifact, found, err := reader.ReportExportArtifactByIdempotency(ctx, principal.WorkspaceID, principal.UserID, reportKey, key)
		if err != nil || !found {
			return key, err
		}
		expiresAt, parseErr := time.Parse(time.RFC3339Nano, artifact.ExpiresAt)
		intact := artifact.ContentSHA256 == reportexport.SHA256Hex(artifact.Content)
		if parseErr == nil && s.clock().UTC().Before(expiresAt) && intact {
			return key, nil
		}
		digest := sha256.Sum256([]byte(key + "\x00" + artifact.ExpiresAt + "\x00" + artifact.ContentSHA256))
		key = fingerprint + ":renew:" + hex.EncodeToString(digest[:12])
	}
	return "", &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_replay_generation_exhausted"}
}

// ReportExportJob is the Report-owned public projection over the generic batch
// substrate. Generic leases, fencing tokens and payloads never leak to clients.
type ReportExportJob struct {
	ID             string                               `json:"id"`
	BatchJobID     string                               `json:"batch_job_id"`
	AuditID        string                               `json:"audit_id"`
	ReportKey      string                               `json:"report_key"`
	ObjectKey      string                               `json:"object_key"`
	Status         string                               `json:"status"`
	PagesCompleted int                                  `json:"pages_completed"`
	RowsExported   int                                  `json:"rows_exported"`
	Total          int                                  `json:"total"`
	Scope          reportmodel.ReportExportScopeRequest `json:"scope"`
	ArtifactID     string                               `json:"artifact_id,omitempty"`
	ContentSHA256  string                               `json:"content_sha256,omitempty"`
	DownloadToken  string                               `json:"download_token,omitempty"`
	ExpiresAt      string                               `json:"expires_at,omitempty"`
	ErrorCode      string                               `json:"error_code,omitempty"`
	CreatedAt      string                               `json:"created_at"`
	UpdatedAt      string                               `json:"updated_at"`
}

// PrepareExportJobScoped freezes the governed request after executing the same
// authorized Report for its exact count/result version, then enqueues only the
// >1000 branch. The worker never owns a second export-only query definition.
func (s *ReportApplicationService) PrepareExportJobScoped(ctx context.Context, reportKey, objectKey, auditID, idempotencyKey string, scope reportmodel.ReportExportScopeRequest, principal principalmodel.Principal) (ReportExportJob, error) {
	payload, err := s.prepareReportExportPayload(ctx, reportKey, objectKey, auditID, idempotencyKey, scope, principal)
	if err != nil {
		return ReportExportJob{}, err
	}
	if !reportExportRequiresAsync(payload.ExactTotal) {
		return ReportExportJob{}, &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_synchronous_required", Params: map[string]string{"total": fmt.Sprint(payload.ExactTotal)}}
	}
	return s.prepareExportJobFromPayload(ctx, payload, reportExportRequestFingerprint(payload), principal)
}

func (s *ReportApplicationService) prepareReportExportPayload(ctx context.Context, reportKey, objectKey, auditID, idempotencyKey string, scope reportmodel.ReportExportScopeRequest, principal principalmodel.Principal) (reportExportBatchPayload, error) {
	if _, err := principalmodel.CommandScopeForPrincipal(principal); err != nil {
		return reportExportBatchPayload{}, reportWorkspaceError(err)
	}
	if s == nil || s.domain == nil || s.exportRecords == nil {
		return reportExportBatchPayload{}, reportApplicationError(nil)
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		return reportExportBatchPayload{}, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.idempotency.key_required"}
	}
	report, err := s.domain.ReportForExport(ctx, reportKey, objectKey, principal)
	if err != nil {
		return reportExportBatchPayload{}, err
	}
	control, ok := s.exportControl(ctx, report.Key, objectKey, principal)
	if !ok {
		return reportExportBatchPayload{}, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.report.export_control_not_found"}
	}
	auditID = strings.TrimSpace(auditID)
	auditRecord, err := s.exportRecords.GetReportRecord(ctx, control.AuditObject, auditID, principal)
	if err != nil {
		return reportExportBatchPayload{}, err
	}
	mapping := control.RecordMapping
	if strings.TrimSpace(fmt.Sprint(auditRecord.Data[mapping.AuditReportKeyField])) != report.Key {
		return reportExportBatchPayload{}, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.report.export_audit_report_mismatch"}
	}
	if strings.TrimSpace(fmt.Sprint(auditRecord.Data[mapping.AuditRequesterField])) != strings.TrimSpace(principal.UserID) {
		return reportExportBatchPayload{}, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.report.export_requester_mismatch"}
	}
	if !reportExportStatusAllowed(strings.TrimSpace(fmt.Sprint(auditRecord.Data[mapping.AuditStatusField])), mapping.AuditPreparedStatuses) {
		return reportExportBatchPayload{}, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.report.export_audit_status_invalid"}
	}
	normalizedScope, scopedReport, maskedDimensions, err := reportexport.NormalizeScope(report, objectKey, control, scope, principal)
	if err != nil {
		return reportExportBatchPayload{}, err
	}
	if _, err = reportexport.ValidateFieldAccess(ctx, s.domain, scopedReport, control, principal); err != nil {
		return reportExportBatchPayload{}, err
	}
	reportHash, _ := reportexport.CanonicalJSONSHA256(report)
	sourceHash := reportHash
	if report.ObjectSQLV1 != nil {
		sourceHash, _ = reportexport.CanonicalJSONSHA256(report.ObjectSQLV1)
	}
	authHash, err := reportservice.ReportAccessScopeHash(principal)
	if err != nil {
		return reportExportBatchPayload{}, err
	}
	rows, sourceVersion, exactTotal, err := s.probeReportExport(ctx, report, scopedReport, normalizedScope, control.MaxRows, principal)
	if err != nil {
		return reportExportBatchPayload{}, err
	}
	sourceVersionHash, err := reportexport.CanonicalJSONSHA256(sourceVersion)
	if err != nil {
		return reportExportBatchPayload{}, err
	}
	resultHash, csvHash := "", ""
	if exactTotal >= 0 {
		resultHash, err = reportPageResultHash(reportmodel.ReportSummary{Rows: rows, SourceRowCount: -1})
		if err != nil {
			return reportExportBatchPayload{}, err
		}
		csvContent, encodeErr := reportexport.EncodeCSV(rows, normalizedScope.FieldProjection, maskedDimensions)
		if encodeErr != nil {
			return reportExportBatchPayload{}, encodeErr
		}
		csvHash = reportexport.SHA256Hex(csvContent)
	}
	return reportExportBatchPayload{WorkspaceID: strings.TrimSpace(principal.WorkspaceID), RequesterUserID: strings.TrimSpace(principal.UserID), ReportKey: report.Key, ObjectKey: strings.TrimSpace(objectKey), AuditID: auditID, Scope: normalizedScope, ReportDefinitionSHA256: reportHash, ReportSourceSHA256: sourceHash, AuthorizationScopeSHA256: authHash, SourceVersion: sourceVersion, SourceVersionSHA256: sourceVersionHash, ResultSHA256: resultHash, CSVContentSHA256: csvHash, ExactTotal: exactTotal, MaxRows: control.MaxRows, Format: "csv"}, nil
}

// reportExportRequestFingerprint intentionally excludes the caller supplied
// idempotency key and audit record identity. It represents the canonical export
// condition and frozen result version, so a second audit/request cannot create
// another file for the same requester, scope and result.
func reportExportRequestFingerprint(payload reportExportBatchPayload) string {
	canonical := struct {
		WorkspaceID, RequesterUserID, ReportKey, ObjectKey, ReportDefinitionSHA256, ReportSourceSHA256, AuthorizationScopeSHA256, SourceVersionSHA256, ResultSHA256, CSVContentSHA256, Format string
		ExactTotal                                                                                                                                                                            int
		MaxRows                                                                                                                                                                               int
		Scope                                                                                                                                                                                 reportmodel.ReportExportScopeRequest
	}{payload.WorkspaceID, payload.RequesterUserID, payload.ReportKey, payload.ObjectKey, payload.ReportDefinitionSHA256, payload.ReportSourceSHA256, payload.AuthorizationScopeSHA256, payload.SourceVersionSHA256, payload.ResultSHA256, payload.CSVContentSHA256, payload.Format, payload.ExactTotal, payload.MaxRows, payload.Scope}
	raw, _ := json.Marshal(canonical)
	digest := sha256.Sum256(append([]byte(reportExportBatchKind+"\x00"), raw...))
	return hex.EncodeToString(digest[:])
}

func (s *ReportApplicationService) prepareExportJobFromPayload(ctx context.Context, payload reportExportBatchPayload, fingerprint string, principal principalmodel.Principal) (ReportExportJob, error) {
	if s.batchJobs == nil {
		return ReportExportJob{}, reportApplicationError(nil)
	}
	baseFingerprint := fingerprint
	if existing, found, err := s.batchJobs.FindOwnedBatchJobByIdempotency(ctx, reportExportBatchKind, payload.ObjectKey, fingerprint, principal); err != nil {
		return ReportExportJob{}, err
	} else if found {
		var stored reportExportBatchPayload
		if err := json.Unmarshal([]byte(existing.PayloadJSON), &stored); err != nil || reportExportRequestFingerprint(stored) != baseFingerprint {
			return ReportExportJob{}, &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.idempotency.key_reused", Err: err, Params: map[string]string{"use_case": "record.report_export.async"}}
		}
		projection, reusable, err := s.reusableReportExportJob(ctx, existing, stored, principal)
		if err != nil {
			return ReportExportJob{}, err
		}
		if reusable {
			if err := s.closeReportExportReplayAudit(ctx, payload, existing, projection, principal); err != nil {
				return ReportExportJob{}, err
			}
			return projection, nil
		}
		fingerprint += ":" + existing.UpdatedAt
	}

	payload.ArtifactIdempotencyKey = "report-export-artifact:" + fingerprint
	// audit_id is request evidence, not an export condition. Keep the winning
	// audit on the job column while persisting a canonical payload so concurrent
	// clicks with different audit rows compare equal under the store's unique
	// (workspace, kind, object, idempotency_key) constraint.
	canonicalPayload := payload
	canonicalPayload.AuditID = ""
	raw, _ := json.Marshal(canonicalPayload)
	job, replayed, err := s.batchJobs.EnqueueOwnedBatchJob(ctx, reportExportBatchKind, payload.ObjectKey, fingerprint, payload.AuditID, raw, principal)
	if err != nil {
		return ReportExportJob{}, err
	}
	projection, projectionErr := s.reportExportJob(ctx, job, principal)
	if projectionErr != nil {
		return ReportExportJob{}, projectionErr
	}
	if replayed && strings.TrimSpace(payload.AuditID) != strings.TrimSpace(job.AuditID) {
		if err := s.closeReportExportReplayAudit(ctx, payload, job, projection, principal); err != nil {
			return ReportExportJob{}, err
		}
	}
	return projection, nil
}

func (s *ReportApplicationService) reusableReportExportJob(ctx context.Context, job recordmodel.RecordBatchJob, payload reportExportBatchPayload, principal principalmodel.Principal) (ReportExportJob, bool, error) {
	if job.Status == "queued" || job.Status == "running" || job.Status == "retrying" {
		projection, err := s.reportExportJob(ctx, job, principal)
		return projection, err == nil, err
	}
	if job.Status != "completed" {
		return ReportExportJob{}, false, nil
	}
	projection, err := s.reportExportJob(ctx, job, principal)
	if err != nil {
		return ReportExportJob{}, false, err
	}
	expiresAt, parseErr := time.Parse(time.RFC3339Nano, projection.ExpiresAt)
	artifactReader, readable := s.exportArtifacts.(reportcontract.ReportExportArtifactRequestReader)
	artifact, found, artifactErr := reportmodel.ReportExportArtifact{}, false, error(nil)
	if readable {
		artifact, found, artifactErr = artifactReader.ReportExportArtifactByIdempotency(ctx, principal.WorkspaceID, principal.UserID, payload.ReportKey, payload.ArtifactIdempotencyKey)
	}
	intact := found && artifactErr == nil && artifact.ContentSHA256 == reportexport.SHA256Hex(artifact.Content) && artifact.ContentSHA256 == projection.ContentSHA256
	reusable := parseErr == nil && s.clock().UTC().Before(expiresAt) && projection.DownloadToken != "" && projection.ContentSHA256 != "" && intact
	return projection, reusable, nil
}

func (s *ReportApplicationService) closeReportExportReplayAudit(ctx context.Context, payload reportExportBatchPayload, job recordmodel.RecordBatchJob, projection ReportExportJob, principal principalmodel.Principal) error {
	if strings.TrimSpace(payload.AuditID) == "" || strings.TrimSpace(payload.AuditID) == strings.TrimSpace(job.AuditID) {
		return nil
	}
	control, ok := s.exportControl(ctx, payload.ReportKey, payload.ObjectKey, principal)
	if !ok || s.exportRecords == nil {
		return reportApplicationError(nil)
	}
	auditRecord, err := s.exportRecords.GetReportRecord(ctx, control.AuditObject, payload.AuditID, principal)
	if err != nil {
		return err
	}
	mapping := control.RecordMapping
	scopeHash, _ := reportexport.CanonicalJSONSHA256(payload.Scope)
	rowCount := payload.ExactTotal
	if projection.Total > 0 {
		rowCount = projection.Total
	} else if rowCount < 0 {
		rowCount = job.Checkpoint
	}
	patch := map[string]any{
		mapping.AuditRowCountField:  rowCount,
		mapping.AuditScopeHashField: "sha256:" + scopeHash,
	}
	if job.Status == "completed" {
		patch[mapping.AuditStatusField] = mapping.AuditPreparedStatus
	}
	if err := s.closeReportExportReplayAuditRecord(ctx, control.AuditObject, auditRecord.ID, patch, "report-export-replay:"+job.ID+":"+auditRecord.ID, principal); err != nil {
		return err
	}
	return s.auditExportRequired(ctx, "report_export_prepare_replayed", payload.ObjectKey, principal, auditRecord.ID, map[string]any{
		"report_key": payload.ReportKey, "job_id": job.ID, "artifact_id": projection.ArtifactID,
		"original_audit_id": job.AuditID, "replay_audit_id": auditRecord.ID, "status": projection.Status,
		"row_count": rowCount, "scope_sha256": scopeHash,
	})
}

func (s *ReportApplicationService) closeReportExportReplayAuditRecord(ctx context.Context, objectKey, auditID string, patch map[string]any, idempotencyKey string, principal principalmodel.Principal) error {
	// Equivalent export prepares deliberately converge on one deterministic
	// audit-update idempotency key. The generic record mutation owner reports a
	// short-lived in_progress decision while the winning transaction commits;
	// wait for that same mutation to become a replay instead of leaking a 409 to
	// callers that already atomically resolved the same completed job/artifact.
	const retryInterval = 5 * time.Millisecond
	const maximumWait = 2 * time.Second
	deadline := time.Now().Add(maximumWait)
	for {
		if _, err := s.exportRecords.UpdateReportRecord(ctx, objectKey, auditID, patch, idempotencyKey, principal); err == nil {
			return nil
		} else if apperror.CodeOf(err) != "backend.idempotency.in_progress" || !time.Now().Before(deadline) {
			return err
		}
		timer := time.NewTimer(retryInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (s *ReportApplicationService) GetExportJob(ctx context.Context, jobID string, principal principalmodel.Principal) (ReportExportJob, error) {
	job, err := s.batchJobs.GetOwnedBatchJob(ctx, jobID, reportExportBatchKind, principal)
	if err != nil {
		return ReportExportJob{}, err
	}
	return s.reportExportJob(ctx, job, principal)
}

func (s *ReportApplicationService) CancelExportJob(ctx context.Context, jobID string, principal principalmodel.Principal) (ReportExportJob, error) {
	job, err := s.batchJobs.CancelOwnedBatchJob(ctx, jobID, reportExportBatchKind, principal)
	if err != nil {
		return ReportExportJob{}, err
	}
	return s.reportExportJob(ctx, job, principal)
}
