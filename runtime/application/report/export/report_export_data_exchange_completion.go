package export

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	auditmodel "github.com/domainry/domainry-audit-sdk/contract"
	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	"github.com/domainry/domainry-foundation/apperror"
	reportcontract "github.com/domainry/domainry-report-sdk/contract"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	auditcontract "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	runtimereportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
)

func (p *DataExchangeProvider) CompleteExport(ctx context.Context, completion dataexchange.ExportCompletion) error {
	payload, err := exchangePayload(completion.Options, completion.ReferenceID)
	if err != nil {
		return err
	}
	principal, err := p.principal(ctx, completion.Scope)
	if err != nil {
		return err
	}
	prepared, err := p.prepare(ctx, payload, principal, false)
	if err != nil {
		return err
	}
	if payload.ExactTotal >= 0 && completion.Rows != payload.ExactTotal {
		return &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_result_changed"}
	}
	if !prepared.control.Watermark && strings.TrimSpace(payload.CSVContentSHA256) != "" && completion.Artifact.SHA256 != payload.CSVContentSHA256 {
		return &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_result_changed"}
	}
	if p.dependencies.Records == nil || p.dependencies.Receipts == nil {
		return internalError(nil)
	}
	completionFingerprint, err := reportExportCompletionFingerprint(completion)
	if err != nil {
		return internalError(err)
	}
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return internalError(err)
	}
	now := p.dependencies.Clock().UTC()
	binding, err := p.dependencies.Receipts.BindReportExportCompletion(ctx, runtimereportmodel.ReportExportCompletionBinding{
		ReceiptID: payload.PrepareReceiptID, WorkspaceID: payload.WorkspaceID, RequesterUserID: payload.RequesterUserID,
		ReportKey: payload.ReportKey, ObjectKey: payload.ObjectKey, AuditID: payload.AuditID,
		JobID: completion.JobID, ArtifactID: completion.Artifact.ID, PayloadJSON: string(payloadJSON), CompletionFingerprint: completionFingerprint,
		Now: now, ExpiresAt: now.Add(90 * 24 * time.Hour),
	})
	if err != nil {
		return internalError(err)
	}
	if binding.Decision == runtimereportmodel.ReportExportCompletionConflict {
		return &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_completion_conflict"}
	}
	auditRecord, err := p.dependencies.Records.GetReportRecord(ctx, prepared.control.AuditObject, payload.AuditID, principal)
	if err != nil {
		return err
	}
	mapping := prepared.control.RecordMapping
	status := strings.TrimSpace(fmt.Sprint(auditRecord.Data[mapping.AuditStatusField]))
	if reportExportProtectedTerminalAuditStatus(status, mapping) || !reportExportAuditBindingMatches(auditRecord.Data, mapping, payload) ||
		(!StatusAllowed(status, mapping.AuditPreparedStatuses) && status != mapping.AuditPreparedStatus && status != mapping.AuditDownloadedStatus) {
		return &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_scope_changed"}
	}
	scopeHash, _ := reportcontract.CanonicalJSONSHA256(prepared.normalizedScope)
	expectedScopeHash := "sha256:" + scopeHash
	preparedStatus, downloadedStatus := strings.TrimSpace(mapping.AuditPreparedStatus), strings.TrimSpace(mapping.AuditDownloadedStatus)
	transitionAudit := status != downloadedStatus
	if status == downloadedStatus {
		switch {
		case preparedStatus != downloadedStatus:
			// A distinct downloaded state is valid only for an exact late replay.
			// Matching completion metadata proves the replay follows a legitimate
			// prepare transition. Without it, a first rejected binding could turn
			// its own retry into authority to manufacture a download record.
			if binding.Decision != runtimereportmodel.ReportExportCompletionReplay ||
				!reportExportAuditCompletionMetadataMatches(auditRecord.Data, mapping, completion.Rows, expectedScopeHash) {
				return &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_scope_changed"}
			}
		default:
			// Some report definitions collapse prepared and downloaded into one
			// completed status, so the audit status cannot prove which phase ran.
			// The receipt's exact job/artifact/completion binding is authoritative:
			// reapply the same-status metadata transition to recover any crash
			// between durable binding and source-owned audit persistence.
			transitionAudit = true
		}
	}
	if transitionAudit {
		won, transitionErr := p.dependencies.Records.TransitionReportExportAudit(ctx, payload.WorkspaceID, prepared.control.AuditObject, auditRecord.ID,
			mapping.AuditStatusField, status, map[string]any{
				mapping.AuditStatusField: mapping.AuditPreparedStatus, mapping.AuditRowCountField: completion.Rows, mapping.AuditScopeHashField: expectedScopeHash,
			})
		if transitionErr != nil {
			return transitionErr
		}
		if !won {
			auditRecord, err = p.dependencies.Records.GetReportRecord(ctx, prepared.control.AuditObject, payload.AuditID, principal)
			if err != nil {
				return err
			}
			status = strings.TrimSpace(fmt.Sprint(auditRecord.Data[mapping.AuditStatusField]))
			if reportExportProtectedTerminalAuditStatus(status, mapping) || !reportExportAuditBindingMatches(auditRecord.Data, mapping, payload) ||
				(status != preparedStatus && status != downloadedStatus) ||
				!reportExportAuditCompletionMetadataMatches(auditRecord.Data, mapping, completion.Rows, expectedScopeHash) {
				return &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_scope_changed"}
			}
		}
	}
	downloadData := map[string]any{
		mapping.DownloadAuditField: payload.AuditID, mapping.DownloadFilenameField: completion.Artifact.Filename,
		mapping.DownloadContentHashField: "sha256:" + completion.Artifact.SHA256,
		mapping.DownloadExpiresAtField:   completion.Artifact.ExpiresAt.UTC().Format(time.RFC3339Nano),
	}
	if mapping.DownloadJobIDField != "" {
		downloadData[mapping.DownloadJobIDField] = completion.JobID
	}
	if mapping.DownloadWatermarkedField != "" {
		downloadData[mapping.DownloadWatermarkedField] = prepared.control.Watermark
	}
	if mapping.DownloadNumberField != "" {
		identifier := completion.Artifact.SHA256
		if len(identifier) > 16 {
			identifier = identifier[:16]
		}
		downloadData[mapping.DownloadNumberField] = "EXPORT-" + strings.ToUpper(identifier)
	}
	downloadRecord, err := p.dependencies.Records.CreateReportRecord(ctx, prepared.control.DownloadObject, downloadData, payload.ArtifactIdempotencyKey, principal)
	if err != nil {
		return err
	}
	parametersHash, _ := reportcontract.CanonicalJSONSHA256(prepared.normalizedScope.Parameters)
	return p.audit(ctx, "report-export-download-prepared:"+completion.Artifact.ID, "report_export_download_prepared", payload.ObjectKey, principal, payload.AuditID, map[string]any{
		"report_key": payload.ReportKey, "download_id": downloadRecord.ID, "artifact_id": completion.Artifact.ID,
		"expires_at": completion.Artifact.ExpiresAt.UTC().Format(time.RFC3339Nano), "watermarked": prepared.control.Watermark,
		"content_sha256": completion.Artifact.SHA256, "row_count": completion.Rows, "scope_sha256": scopeHash, "parameters_sha256": parametersHash,
	}, true)
}

func reportExportAuditCompletionMetadataMatches(data map[string]any, mapping reportmodel.ReportExportRecordMappingSchema, rows int, scopeHash string) bool {
	return strings.TrimSpace(fmt.Sprint(data[mapping.AuditRowCountField])) == fmt.Sprint(rows) &&
		strings.TrimSpace(fmt.Sprint(data[mapping.AuditScopeHashField])) == strings.TrimSpace(scopeHash)
}

func reportExportProtectedTerminalAuditStatus(status string, mapping reportmodel.ReportExportRecordMappingSchema) bool {
	status = strings.TrimSpace(status)
	for _, terminal := range []string{mapping.AuditDeniedStatus, mapping.AuditExpiredStatus} {
		if value := strings.TrimSpace(terminal); value != "" && status == value {
			return true
		}
	}
	return false
}

func reportExportCompletionFingerprint(completion dataexchange.ExportCompletion) (string, error) {
	return reportcontract.CanonicalJSONSHA256(struct {
		JobID        string `json:"job_id"`
		ArtifactID   string `json:"artifact_id"`
		Filename     string `json:"filename"`
		ContentType  string `json:"content_type"`
		SHA256       string `json:"sha256"`
		Size         int64  `json:"size"`
		ExpiresAt    string `json:"expires_at"`
		Rows         int    `json:"rows"`
		ResultChunks int    `json:"result_chunks"`
	}{
		JobID: strings.TrimSpace(completion.JobID), ArtifactID: strings.TrimSpace(completion.Artifact.ID),
		Filename: strings.TrimSpace(completion.Artifact.Filename), ContentType: strings.TrimSpace(completion.Artifact.ContentType),
		SHA256: strings.TrimSpace(completion.Artifact.SHA256), Size: completion.Artifact.Size,
		ExpiresAt: completion.Artifact.ExpiresAt.UTC().Format(time.RFC3339Nano), Rows: completion.Rows, ResultChunks: completion.ResultChunks,
	})
}

func reportExportAuditBindingMatches(data map[string]any, mapping reportmodel.ReportExportRecordMappingSchema, payload ExportPayload) bool {
	return strings.TrimSpace(fmt.Sprint(data[mapping.AuditReportKeyField])) == payload.ReportKey &&
		strings.TrimSpace(fmt.Sprint(data[mapping.AuditRequesterField])) == payload.RequesterUserID
}

func (p *DataExchangeProvider) audit(ctx context.Context, key, event, objectKey string, principal principalmodel.Principal, auditID string, metadata map[string]any, required bool) error {
	if p == nil || p.dependencies.Audit == nil {
		if required {
			return internalError(nil)
		}
		return nil
	}
	return p.dependencies.Audit.AppendAudit(ctx, auditcontract.AuditAppendRequest{IdempotencyKey: key, Family: auditmodel.EventFamilyRuntimeReport, Event: event, ObjectKey: objectKey, RecordID: auditID, Principal: principal, Summary: event, Metadata: metadata})
}

func StatusAllowed(status string, allowed []string) bool {
	status = strings.TrimSpace(status)
	for _, value := range allowed {
		if status == strings.TrimSpace(value) {
			return true
		}
	}
	return false
}
