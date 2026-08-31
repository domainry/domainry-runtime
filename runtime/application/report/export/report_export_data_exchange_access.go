package export

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	"github.com/domainry/domainry-foundation/apperror"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	reportcontract "github.com/domainry/domainry-report/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

type ExchangeJob = reportmodel.ReportExportJob

func (p *DataExchangeProvider) OpenDataExchangeArtifact(ctx context.Context, job dataexchange.Job, scope dataexchange.Scope) (dataexchange.Artifact, error) {
	principal, err := p.principal(ctx, scope)
	if err != nil {
		return dataexchange.Artifact{}, err
	}
	return p.openDataExchangeArtifact(ctx, job, principal)
}

func (p *DataExchangeProvider) ProjectDataExchangeJob(ctx context.Context, job dataexchange.Job, scope dataexchange.Scope) (any, error) {
	principal, err := p.principal(ctx, scope)
	if err != nil {
		return nil, err
	}
	return p.ProjectJob(ctx, job, principal)
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
		ID: job.ID, AuditID: job.ReferenceID, ReportKey: payload.ReportKey, ObjectKey: payload.ObjectKey,
		Status: status, PagesCompleted: job.ResultChunks, RowsExported: job.Checkpoint, Total: total, Scope: payload.Scope,
		ArtifactID: job.ArtifactID, ErrorCode: job.ErrorCode,
		CreatedAt: job.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: job.UpdatedAt.UTC().Format(time.RFC3339Nano),
	}
	if status != "completed" {
		return projection, nil
	}
	artifact, err := p.dependencies.Binding.Download(ctx, dataexchange.JobRequest{Scope: Scope(principal), JobID: job.ID, Provider: DataExchangeProviderKey, Operation: "export"})
	if err != nil {
		return ExchangeJob{}, err
	}
	if artifact.Content != nil {
		_ = artifact.Content.Close()
	}
	projection.ArtifactID, projection.ContentSHA256 = artifact.ID, artifact.SHA256
	projection.ExpiresAt = artifact.ExpiresAt.UTC().Format(time.RFC3339Nano)
	return projection, nil
}

func (p *DataExchangeProvider) CloseReplayAudit(ctx context.Context, payload ExportPayload, job dataexchange.Job, projection ExchangeJob, principal principalmodel.Principal) error {
	if strings.TrimSpace(payload.AuditID) == "" || strings.TrimSpace(payload.AuditID) == strings.TrimSpace(job.ReferenceID) {
		return nil
	}
	if p.dependencies.ResolveExecution == nil || p.dependencies.Records == nil {
		return internalError(nil)
	}
	resolved, err := p.dependencies.ResolveExecution(ctx, reportmodel.ReportExportExecutionRequest{ReportKey: payload.ReportKey, ObjectKey: payload.ObjectKey, Scope: payload.Scope}, principal)
	if err != nil {
		return err
	}
	control := resolved.Definition.Control
	auditRecord, err := p.dependencies.Records.GetReportRecord(ctx, control.AuditObject, payload.AuditID, principal)
	if err != nil {
		return err
	}
	scopeHash, _ := reportcontract.CanonicalReportJSONSHA256(payload.Scope)
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

func (p *DataExchangeProvider) openDataExchangeArtifact(ctx context.Context, job dataexchange.Job, principal principalmodel.Principal) (dataexchange.Artifact, error) {
	if p == nil || p.dependencies.Binding == nil {
		return dataexchange.Artifact{}, internalError(nil)
	}
	if job.Provider != DataExchangeProviderKey || job.Operation != "export" || job.Status != "completed" || job.WorkspaceID != strings.TrimSpace(principal.WorkspaceID) || job.ActorID != strings.TrimSpace(principal.UserID) {
		return dataexchange.Artifact{}, &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_result_not_ready"}
	}
	payload, err := exchangePayload(job.Options, job.ReferenceID)
	if err != nil {
		return dataexchange.Artifact{}, err
	}
	prepared, err := p.prepare(ctx, payload, principal, false)
	if err != nil {
		return dataexchange.Artifact{}, err
	}
	artifact, err := p.dependencies.Binding.Download(ctx, dataexchange.JobRequest{Scope: Scope(principal), JobID: job.ID, Provider: DataExchangeProviderKey, Operation: "export"})
	if err != nil {
		return dataexchange.Artifact{}, err
	}
	if artifact.Content == nil {
		return dataexchange.Artifact{}, internalError(nil)
	}
	defer artifact.Content.Close()
	deny := func(code, reason string) error {
		status, event := prepared.control.RecordMapping.AuditDeniedStatus, "report_export_download_denied"
		if reason == "expired" {
			status, event = prepared.control.RecordMapping.AuditExpiredStatus, "report_export_download_expired"
		}
		if strings.TrimSpace(status) != "" && p.dependencies.Records != nil {
			_ = p.dependencies.Records.TransitionReportExportAuditStatus(ctx, payload.WorkspaceID, prepared.control.AuditObject, payload.AuditID, prepared.control.RecordMapping.AuditStatusField, prepared.control.RecordMapping.AuditPreparedStatus, status)
		}
		_ = p.audit(ctx, "", event, payload.ObjectKey, principal, payload.AuditID, map[string]any{"report_key": payload.ReportKey, "artifact_id": artifact.ID, "reason": reason}, false)
		return &apperror.AppError{Kind: apperror.KindForbidden, Code: code}
	}
	if artifact.ExpiresAt.IsZero() || !p.dependencies.Clock().UTC().Before(artifact.ExpiresAt) {
		return dataexchange.Artifact{}, deny("backend.report.export_download_expired", "expired")
	}
	scopeHash, _ := reportcontract.CanonicalReportJSONSHA256(prepared.normalizedScope)
	content, err := io.ReadAll(artifact.Content)
	if err != nil {
		if errors.Is(err, dataexchange.ErrContentCorrupt) {
			return dataexchange.Artifact{}, deny("backend.report.export_integrity_failed", "content_integrity_mismatch")
		}
		return dataexchange.Artifact{}, err
	}
	auditRecord, err := p.dependencies.Records.GetReportRecord(ctx, prepared.control.AuditObject, payload.AuditID, principal)
	if err != nil {
		return dataexchange.Artifact{}, err
	}
	mapping := prepared.control.RecordMapping
	if strings.TrimSpace(fmt.Sprint(auditRecord.Data[mapping.AuditReportKeyField])) != payload.ReportKey || strings.TrimSpace(fmt.Sprint(auditRecord.Data[mapping.AuditRequesterField])) != payload.RequesterUserID {
		return dataexchange.Artifact{}, deny("backend.report.export_requester_mismatch", "audit_binding_changed")
	}
	status := strings.TrimSpace(fmt.Sprint(auditRecord.Data[mapping.AuditStatusField]))
	if status != mapping.AuditDownloadedStatus && status != mapping.AuditPreparedStatus {
		return dataexchange.Artifact{}, deny("backend.report.export_audit_status_invalid", "audit_status_changed")
	}
	if status != mapping.AuditDownloadedStatus {
		if _, err = p.dependencies.Records.UpdateReportRecord(ctx, prepared.control.AuditObject, auditRecord.ID, map[string]any{mapping.AuditStatusField: mapping.AuditDownloadedStatus}, "report-export-complete:"+auditRecord.ID, principal); err != nil {
			return dataexchange.Artifact{}, err
		}
	}
	parametersHash, _ := reportcontract.CanonicalReportJSONSHA256(prepared.normalizedScope.Parameters)
	if err = p.audit(ctx, "", "report_export_downloaded", payload.ObjectKey, principal, payload.AuditID, map[string]any{
		"report_key": payload.ReportKey, "artifact_id": artifact.ID, "expires_at": artifact.ExpiresAt.UTC().Format(time.RFC3339Nano),
		"watermarked": prepared.control.Watermark, "content_sha256": artifact.SHA256, "row_count": job.Checkpoint,
		"scope_sha256": scopeHash, "parameters_sha256": parametersHash,
	}, true); err != nil {
		return dataexchange.Artifact{}, internalError(err)
	}
	artifact.Size = int64(len(content))
	artifact.Content = io.NopCloser(bytes.NewReader(content))
	return artifact, nil
}
