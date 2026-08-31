package export

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"strings"
	"time"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	"github.com/domainry/domainry-foundation/apperror"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type ExchangeJob struct {
	ID                string                               `json:"id"`
	DataExchangeJobID string                               `json:"data_exchange_job_id"`
	AuditID           string                               `json:"audit_id"`
	ReportKey         string                               `json:"report_key"`
	ObjectKey         string                               `json:"object_key"`
	Status            string                               `json:"status"`
	PagesCompleted    int                                  `json:"pages_completed"`
	RowsExported      int                                  `json:"rows_exported"`
	Total             int                                  `json:"total"`
	Scope             reportmodel.ReportExportScopeRequest `json:"scope"`
	ArtifactID        string                               `json:"artifact_id,omitempty"`
	ContentSHA256     string                               `json:"content_sha256,omitempty"`
	DownloadToken     string                               `json:"download_token,omitempty"`
	ExpiresAt         string                               `json:"expires_at,omitempty"`
	ErrorCode         string                               `json:"error_code,omitempty"`
	CreatedAt         string                               `json:"created_at"`
	UpdatedAt         string                               `json:"updated_at"`
}

func (p *DataExchangeProvider) ProjectJob(ctx context.Context, job dataexchange.Job, principal principalmodel.Principal) (ExchangeJob, error) {
	if job.Provider != DataExchangeProviderKey || job.Operation != "export" || job.WorkspaceID != strings.TrimSpace(principal.WorkspaceID) || job.ActorID != strings.TrimSpace(principal.UserID) {
		return ExchangeJob{}, &apperror.AppError{Kind: apperror.KindNotFound, Code: "backend.report.export_job_not_found"}
	}
	payload, err := exchangePayload(job.Options, job.ReferenceID)
	if err != nil {
		return ExchangeJob{}, err
	}
	status := job.Status
	if status == "queued" || status == "retrying" {
		status = "accepted"
	}
	total := job.Total
	if total == 0 && payload.ExactTotal > 0 {
		total = payload.ExactTotal
	}
	projection := ExchangeJob{
		ID: job.ID, DataExchangeJobID: job.ID, AuditID: job.ReferenceID, ReportKey: payload.ReportKey, ObjectKey: payload.ObjectKey,
		Status: status, PagesCompleted: job.ResultChunks, RowsExported: job.Checkpoint, Total: total, Scope: payload.Scope,
		ArtifactID: job.ArtifactID, ErrorCode: job.ErrorCode,
		CreatedAt: job.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: job.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if status != "completed" {
		return projection, nil
	}
	artifact, err := p.dependencies.Binding.Download(ctx, dataexchange.JobRequest{Scope: Scope(principal), JobID: job.ID})
	if err != nil {
		return ExchangeJob{}, err
	}
	if artifact.Content != nil {
		_ = artifact.Content.Close()
	}
	projection.ArtifactID, projection.ContentSHA256, projection.DownloadToken = artifact.ID, artifact.SHA256, job.ID
	projection.ExpiresAt = artifact.ExpiresAt.UTC().Format(time.RFC3339Nano)
	return projection, nil
}

func (p *DataExchangeProvider) CloseReplayAudit(ctx context.Context, payload ExportPayload, job dataexchange.Job, projection ExchangeJob, principal principalmodel.Principal) error {
	if strings.TrimSpace(payload.AuditID) == "" || strings.TrimSpace(payload.AuditID) == strings.TrimSpace(job.ReferenceID) {
		return nil
	}
	control, ok := p.dependencies.ExportControl(ctx, payload.ReportKey, payload.ObjectKey, principal)
	if !ok || p.dependencies.Records == nil {
		return internalError(nil)
	}
	auditRecord, err := p.dependencies.Records.GetReportRecord(ctx, control.AuditObject, payload.AuditID, principal)
	if err != nil {
		return err
	}
	scopeHash, _ := CanonicalJSONSHA256(payload.Scope)
	rowCount := payload.ExactTotal
	if projection.Total > 0 {
		rowCount = projection.Total
	} else if rowCount < 0 {
		rowCount = job.Checkpoint
	}
	mapping := control.RecordMapping
	patch := map[string]any{mapping.AuditRowCountField: rowCount, mapping.AuditScopeHashField: "sha256:" + scopeHash}
	if job.Status == "completed" {
		patch[mapping.AuditStatusField] = mapping.AuditPreparedStatus
	}
	if err := p.updateReplayAudit(ctx, control.AuditObject, auditRecord.ID, patch, "report-export-replay:"+job.ID+":"+auditRecord.ID, principal); err != nil {
		return err
	}
	return p.audit(ctx, "", "report_export_prepare_replayed", payload.ObjectKey, principal, auditRecord.ID, map[string]any{
		"report_key": payload.ReportKey, "job_id": job.ID, "artifact_id": projection.ArtifactID,
		"original_audit_id": job.ReferenceID, "replay_audit_id": auditRecord.ID, "status": projection.Status,
		"row_count": rowCount, "scope_sha256": scopeHash,
	}, true)
}

func (p *DataExchangeProvider) updateReplayAudit(ctx context.Context, objectKey, auditID string, patch map[string]any, key string, principal principalmodel.Principal) error {
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := p.dependencies.Records.UpdateReportRecord(ctx, objectKey, auditID, patch, key, principal); err == nil {
			return nil
		} else if apperror.CodeOf(err) != "backend.idempotency.in_progress" || !time.Now().Before(deadline) {
			return err
		}
		timer := time.NewTimer(5 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (p *DataExchangeProvider) Download(ctx context.Context, jobID string, principal principalmodel.Principal) ([]byte, string, error) {
	if p == nil || p.dependencies.Binding == nil {
		return nil, "", internalError(nil)
	}
	job, err := p.dependencies.Binding.Job(ctx, dataexchange.JobRequest{Scope: Scope(principal), JobID: jobID})
	if err != nil {
		return nil, "", err
	}
	payload, err := exchangePayload(job.Options, job.ReferenceID)
	if err != nil {
		return nil, "", err
	}
	if job.Provider != DataExchangeProviderKey || job.Operation != "export" || job.Status != "completed" || job.ActorID != strings.TrimSpace(principal.UserID) {
		return nil, "", &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_result_not_ready"}
	}
	prepared, err := p.prepare(ctx, payload, principal, false)
	if err != nil {
		return nil, "", err
	}
	artifact, err := p.dependencies.Binding.Download(ctx, dataexchange.JobRequest{Scope: Scope(principal), JobID: job.ID})
	if err != nil {
		return nil, "", err
	}
	if artifact.Content == nil {
		return nil, "", internalError(nil)
	}
	defer artifact.Content.Close()
	deny := func(code, reason string) ([]byte, string, error) {
		status, event := prepared.control.RecordMapping.AuditDeniedStatus, "report_export_download_denied"
		if reason == "expired" {
			status, event = prepared.control.RecordMapping.AuditExpiredStatus, "report_export_download_expired"
		}
		if strings.TrimSpace(status) != "" && p.dependencies.Records != nil {
			_ = p.dependencies.Records.TransitionReportExportAuditStatus(ctx, payload.WorkspaceID, prepared.control.AuditObject, payload.AuditID, prepared.control.RecordMapping.AuditStatusField, prepared.control.RecordMapping.AuditPreparedStatus, status)
		}
		_ = p.audit(ctx, "", event, payload.ObjectKey, principal, payload.AuditID, map[string]any{"report_key": payload.ReportKey, "artifact_id": artifact.ID, "reason": reason}, false)
		return nil, "", &apperror.AppError{Kind: apperror.KindForbidden, Code: code}
	}
	if artifact.ExpiresAt.IsZero() || !p.dependencies.Clock().UTC().Before(artifact.ExpiresAt) {
		return deny("backend.report.export_download_expired", "expired")
	}
	scopeHash, _ := CanonicalJSONSHA256(prepared.normalizedScope)
	snapshot := reportmodel.ReportExportAuthorizationSnapshot{
		ObjectKey: payload.ObjectKey, Scope: prepared.normalizedScope, ScopeSHA256: scopeHash,
		AuthorizationScopeSHA256: payload.AuthorizationScopeSHA256, ReportDefinitionSHA256: payload.ReportDefinitionSHA256,
		ControlDefinitionSHA256: payload.ControlDefinitionSHA256,
	}
	report, err := p.dependencies.Domain.ReportForExport(ctx, payload.ReportKey, payload.ObjectKey, principal)
	if err != nil {
		return deny(apperror.CodeOf(err), "current_report_permission")
	}
	if err = ValidateCurrentExportAuthorization(ctx, p.dependencies.Domain, report, prepared.control, snapshot, principal); err != nil {
		return deny(apperror.CodeOf(err), apperror.CodeOf(err))
	}
	content, err := io.ReadAll(io.LimitReader(artifact.Content, artifact.Size+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(content)) != artifact.Size {
		return deny("backend.report.export_integrity_failed", "content_size_mismatch")
	}
	digest := sha256.Sum256(content)
	if hex.EncodeToString(digest[:]) != artifact.SHA256 {
		return deny("backend.report.export_integrity_failed", "content_hash_mismatch")
	}
	auditRecord, err := p.dependencies.Records.GetReportRecord(ctx, prepared.control.AuditObject, payload.AuditID, principal)
	if err != nil {
		return nil, "", err
	}
	mapping := prepared.control.RecordMapping
	if strings.TrimSpace(fmt.Sprint(auditRecord.Data[mapping.AuditReportKeyField])) != payload.ReportKey || strings.TrimSpace(fmt.Sprint(auditRecord.Data[mapping.AuditRequesterField])) != payload.RequesterUserID {
		return deny("backend.report.export_requester_mismatch", "audit_binding_changed")
	}
	status := strings.TrimSpace(fmt.Sprint(auditRecord.Data[mapping.AuditStatusField]))
	if status != mapping.AuditDownloadedStatus && status != mapping.AuditPreparedStatus {
		return deny("backend.report.export_audit_status_invalid", "audit_status_changed")
	}
	if status != mapping.AuditDownloadedStatus {
		if _, err = p.dependencies.Records.UpdateReportRecord(ctx, prepared.control.AuditObject, auditRecord.ID, map[string]any{mapping.AuditStatusField: mapping.AuditDownloadedStatus}, "report-export-complete:"+auditRecord.ID, principal); err != nil {
			return nil, "", err
		}
	}
	parametersHash, _ := CanonicalJSONSHA256(prepared.normalizedScope.Parameters)
	if err = p.audit(ctx, "", "report_export_downloaded", payload.ObjectKey, principal, payload.AuditID, map[string]any{
		"report_key": payload.ReportKey, "artifact_id": artifact.ID, "expires_at": artifact.ExpiresAt.UTC().Format(time.RFC3339Nano),
		"watermarked": prepared.control.Watermark, "content_sha256": artifact.SHA256, "row_count": job.Checkpoint,
		"scope_sha256": scopeHash, "parameters_sha256": parametersHash,
	}, true); err != nil {
		return nil, "", internalError(err)
	}
	return content, artifact.Filename, nil
}
