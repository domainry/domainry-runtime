package application

import (
	"context"
	"encoding/json"
	"strings"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	"github.com/domainry/domainry-foundation/apperror"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	reportexport "github.com/domainry/domainry-runtime/runtime/application/report/export"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

// A retry is a new immutable attempt, never a reset of the old job's status,
// content, authorization hash or completion receipt. The receipt store admits
// only one successor for each failed job, including concurrent caller keys.
func (s *ReportExportApplicationService) validateReportExportRetry(ctx context.Context, request reportmodel.ReportExportPrepareRequest, principal principalmodel.Principal) error {
	invalid := func() error {
		return &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_retry_invalid"}
	}
	job, err := s.dataExchange.Job(ctx, dataexchange.JobRequest{Scope: reportexport.Scope(principal), JobID: request.RetryOfJobID, Provider: reportexport.DataExchangeProviderKey, Operation: "export"})
	if err != nil {
		return err
	}
	if job.Status != "failed" || job.ArtifactID != "" || job.WorkspaceID != principal.WorkspaceID || job.ActorID != principal.UserID || job.Provider != reportexport.DataExchangeProviderKey || job.Operation != "export" || job.ReferenceID != strings.TrimSpace(request.AuditID) {
		return invalid()
	}
	var payload reportexport.ExportPayload
	if err := json.Unmarshal(job.Options, &payload); err != nil {
		return invalid()
	}
	payload.AuditID = job.ReferenceID
	if payload.WorkspaceID != principal.WorkspaceID || payload.RequesterUserID != principal.UserID || payload.ReportKey != strings.TrimSpace(request.ReportKey) || payload.ObjectKey != strings.TrimSpace(request.ObjectKey) || payload.PrepareReceiptID == "" {
		return invalid()
	}
	receipt, found, err := s.prepareReceipts.GetReportExportPrepareReceipt(ctx, principal.WorkspaceID, payload.PrepareReceiptID)
	if err != nil {
		return err
	}
	if !found || receipt.JobID != job.ID || receipt.CompletionArtifactID != "" || receipt.CompletionFingerprint != "" {
		return invalid()
	}
	frozen, err := reportExportPayloadFromReceipt(receipt)
	if err != nil {
		return invalid()
	}
	frozen.AuditID = ""
	expectedOptions, err := json.Marshal(frozen)
	if err != nil {
		return err
	}
	if err := validateReportExportReceiptJob(job, receipt, expectedOptions); err != nil {
		return invalid()
	}
	return nil
}
