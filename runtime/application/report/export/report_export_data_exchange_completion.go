package export

import (
	"context"
	"fmt"
	"strings"
	"time"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	"github.com/domainry/domainry-foundation/apperror"
	reportcontract "github.com/domainry/domainry-report-sdk/contract"
	auditcontract "github.com/domainry/domainry-runtime/runtime/application/auditbinding"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
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
	prepared, err := p.prepare(ctx, payload, principal, true)
	if err != nil {
		return err
	}
	if payload.ExactTotal >= 0 && completion.Rows != payload.ExactTotal {
		return &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_result_changed"}
	}
	if !prepared.control.Watermark && strings.TrimSpace(payload.CSVContentSHA256) != "" && completion.Artifact.SHA256 != payload.CSVContentSHA256 {
		return &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_result_changed"}
	}
	if p.dependencies.Records == nil {
		return internalError(nil)
	}
	auditRecord, err := p.dependencies.Records.GetReportRecord(ctx, prepared.control.AuditObject, payload.AuditID, principal)
	if err != nil {
		return err
	}
	mapping := prepared.control.RecordMapping
	status := strings.TrimSpace(fmt.Sprint(auditRecord.Data[mapping.AuditStatusField]))
	if strings.TrimSpace(fmt.Sprint(auditRecord.Data[mapping.AuditReportKeyField])) != payload.ReportKey ||
		strings.TrimSpace(fmt.Sprint(auditRecord.Data[mapping.AuditRequesterField])) != payload.RequesterUserID ||
		(!StatusAllowed(status, mapping.AuditPreparedStatuses) && status != mapping.AuditPreparedStatus && status != mapping.AuditDownloadedStatus) {
		return &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_scope_changed"}
	}
	scopeHash, _ := reportcontract.CanonicalJSONSHA256(prepared.normalizedScope)
	if _, err = p.dependencies.Records.UpdateReportRecord(ctx, prepared.control.AuditObject, auditRecord.ID, map[string]any{
		mapping.AuditStatusField: mapping.AuditPreparedStatus, mapping.AuditRowCountField: completion.Rows, mapping.AuditScopeHashField: "sha256:" + scopeHash,
	}, "report-export-prepared:"+completion.Artifact.ID, principal); err != nil {
		return err
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

func (p *DataExchangeProvider) audit(ctx context.Context, key, event, objectKey string, principal principalmodel.Principal, auditID string, metadata map[string]any, required bool) error {
	if p == nil || p.dependencies.Audit == nil {
		if required {
			return internalError(nil)
		}
		return nil
	}
	return p.dependencies.Audit.AppendAudit(ctx, auditcontract.AuditAppendRequest{IdempotencyKey: key, Event: event, ObjectKey: objectKey, RecordID: auditID, Principal: principal, Summary: event, Metadata: metadata})
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
