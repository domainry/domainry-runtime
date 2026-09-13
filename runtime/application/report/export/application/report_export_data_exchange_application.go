package application

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	reportcontract "github.com/domainry/domainry-report-sdk/contract"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	reportexport "github.com/domainry/domainry-runtime/runtime/application/report/export"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	runtimereportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	reportadapter "github.com/domainry/domainry-runtime/runtime/modulehost/report"
)

const reportExportProbeRowLimit = 1000

const (
	reportExportPrepareLeaseTTL  = 5 * time.Minute
	reportExportReceiptRetention = 90 * 24 * time.Hour
	reportExportFinalizeTimeout  = 5 * time.Second
)

// PrepareResolvedExport receives the already resolved, authorized Report
// definition and normalized scope. Runtime claims durable coordination before
// reading the mutable audit row or probing source data.
func (s *ReportExportApplicationService) PrepareResolvedExport(ctx context.Context, request reportmodel.ReportExportPrepareRequest, report reportmodel.ReportSchema, control reportmodel.ReportExportControlSchema, principal principalmodel.Principal) (reportexport.ExchangeJob, error) {
	request.IdempotencyKey = strings.TrimSpace(request.IdempotencyKey)
	request.RetryOfJobID = strings.TrimSpace(request.RetryOfJobID)
	if request.IdempotencyKey == "" {
		return reportexport.ExchangeJob{}, &apperror.AppError{Kind: apperror.KindBadRequest, Code: idempotency.ErrorCodeMissingKey}
	}
	if err := s.validateReportExportPrepareStatic(request, report, control, principal); err != nil {
		return reportexport.ExchangeJob{}, err
	}
	if request.RetryOfJobID != "" {
		if err := s.validateReportExportRetry(ctx, request, principal); err != nil {
			return reportexport.ExchangeJob{}, err
		}
	}
	fingerprint, err := reportExportPrepareRequestFingerprint(request, report, control, principal)
	if err != nil {
		return reportexport.ExchangeJob{}, err
	}
	leaseOwner, err := reportExportPrepareLeaseOwner(principal)
	if err != nil {
		return reportexport.ExchangeJob{}, reportApplicationError(err)
	}
	now := s.clock().UTC()
	claim, err := s.prepareReceipts.TryBeginReportExportPrepare(ctx, runtimereportmodel.ReportExportPrepareClaimRequest{
		Receipt: runtimereportmodel.ReportExportPrepareReceipt{
			WorkspaceID: strings.TrimSpace(principal.WorkspaceID), RequesterUserID: strings.TrimSpace(principal.UserID),
			UseCase: runtimereportmodel.ReportExportPrepareUseCase, ReportKey: strings.TrimSpace(report.Key),
			ObjectKey: strings.TrimSpace(request.ObjectKey), AuditID: strings.TrimSpace(request.AuditID), CallerKey: request.IdempotencyKey,
			RetryOfJobID: request.RetryOfJobID,
		},
		RequestFingerprint: fingerprint, LeaseOwner: leaseOwner, LeaseTTL: reportExportPrepareLeaseTTL, Now: now,
	})
	if err != nil {
		return reportexport.ExchangeJob{}, reportApplicationError(err)
	}
	if claim.AuditOperationConflict {
		return reportexport.ExchangeJob{}, &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_audit_operation_conflict"}
	}
	switch claim.Decision {
	case idempotency.DecisionReplay:
		return s.replayReportExportPrepare(ctx, claim.Receipt, principal)
	case idempotency.DecisionInProgress:
		return reportexport.ExchangeJob{}, &apperror.AppError{Kind: apperror.KindConflict, Code: idempotency.ErrorCodeInProgress}
	case idempotency.DecisionFingerprintConflict:
		return reportexport.ExchangeJob{}, reportExportIdempotencyConflict(nil)
	case idempotency.DecisionAcquired:
		return s.executeReportExportPrepareClaim(ctx, claim.Receipt, request, report, control, principal)
	default:
		return reportexport.ExchangeJob{}, &apperror.AppError{Kind: apperror.KindConflict, Code: idempotency.ErrorCodeReceiptUnavailable}
	}
}

func (s *ReportExportApplicationService) executeReportExportPrepareClaim(ctx context.Context, receipt runtimereportmodel.ReportExportPrepareReceipt, request reportmodel.ReportExportPrepareRequest, report reportmodel.ReportSchema, control reportmodel.ReportExportControlSchema, principal principalmodel.Principal) (reportexport.ExchangeJob, error) {
	if strings.TrimSpace(receipt.PayloadJSON) != "" {
		payload, err := reportExportPayloadFromReceipt(receipt)
		if err != nil {
			return reportexport.ExchangeJob{}, err
		}
		return s.submitClaimedReportExport(ctx, receipt, payload, principal)
	}
	auditStatus, err := s.validateReportExportAudit(ctx, report, control, request.ObjectKey, request.AuditID, principal)
	if err != nil {
		return reportexport.ExchangeJob{}, s.releaseFreshReportExportPrepare(ctx, receipt, err)
	}
	mapping := control.RecordMapping
	if reportExportFreshPrepareTerminalStatus(auditStatus, mapping) || !reportexport.StatusAllowed(auditStatus, mapping.AuditPreparedStatuses) {
		err = &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.report.export_audit_status_invalid"}
		return reportexport.ExchangeJob{}, s.releaseFreshReportExportPrepare(ctx, receipt, err)
	}
	payload, err := s.buildReportExportPayload(ctx, report, control, request.ObjectKey, request.AuditID, request.Scope, principal)
	if err != nil {
		return reportexport.ExchangeJob{}, s.releaseFreshReportExportPrepare(ctx, receipt, err)
	}
	payload.PrepareReceiptID = receipt.ID
	payload.RetryOfJobID = receipt.RetryOfJobID
	receipt.BusinessJobKey = reportExportBusinessJobKey(receipt)
	payload.ArtifactIdempotencyKey = reportExportArtifactKey(receipt)
	payloadJSON, err := json.Marshal(payload)
	if err != nil {
		return reportexport.ExchangeJob{}, s.releaseFreshReportExportPrepare(ctx, receipt, err)
	}
	receipt, err = s.prepareReceipts.SaveReportExportPreparePayload(ctx, runtimereportmodel.ReportExportPreparePayload{
		WorkspaceID: receipt.WorkspaceID, ReceiptID: receipt.ID, PayloadJSON: string(payloadJSON), BusinessJobKey: receipt.BusinessJobKey,
		LeaseOwner: receipt.LeaseOwner, FencingToken: receipt.FencingToken, Now: s.clock().UTC(),
	})
	if err != nil {
		// The write outcome may be uncertain. Keep the fenced lease so a later
		// claimant can inspect durable state before deciding whether to reprobe.
		return reportexport.ExchangeJob{}, reportApplicationError(err)
	}
	return s.submitClaimedReportExport(ctx, receipt, payload, principal)
}

func reportExportFreshPrepareTerminalStatus(status string, mapping reportmodel.ReportExportRecordMappingSchema) bool {
	status = strings.TrimSpace(status)
	for _, terminal := range []string{mapping.AuditDownloadedStatus, mapping.AuditDeniedStatus, mapping.AuditExpiredStatus} {
		if value := strings.TrimSpace(terminal); value != "" && status == value {
			return true
		}
	}
	return false
}

func (s *ReportExportApplicationService) submitClaimedReportExport(ctx context.Context, receipt runtimereportmodel.ReportExportPrepareReceipt, payload reportexport.ExportPayload, principal principalmodel.Principal) (reportexport.ExchangeJob, error) {
	canonical := payload
	canonical.AuditID = ""
	options, err := json.Marshal(canonical)
	if err != nil {
		return reportexport.ExchangeJob{}, s.failRetryableReportExportPrepare(ctx, receipt, err)
	}
	job, _, err := s.dataExchange.SubmitExport(ctx, dataexchange.ExportRequest{
		Scope: reportexport.Scope(principal), Provider: reportexport.DataExchangeProviderKey,
		ObjectKey: payload.ObjectKey, IdempotencyKey: receipt.BusinessJobKey, ReferenceID: payload.AuditID, Options: options,
	})
	if err != nil {
		// Submit may have committed remotely. The frozen payload and stable,
		// requester-namespaced business key make the next reclaim safe.
		mapped := reportExportDataExchangeError(err)
		if errors.Is(err, dataexchange.ErrIdempotencyKeyReused) {
			return reportexport.ExchangeJob{}, s.failTerminalReportExportPrepare(ctx, receipt, mapped)
		}
		return reportexport.ExchangeJob{}, s.failRetryableReportExportPrepare(ctx, receipt, mapped)
	}
	if err := validateReportExportReceiptJob(job, receipt, options); err != nil {
		return reportexport.ExchangeJob{}, s.failTerminalReportExportPrepare(ctx, receipt, err)
	}
	now := s.clock().UTC()
	finalizeCtx, cancel := reportExportFinalizeContext(ctx)
	defer cancel()
	if err := s.prepareReceipts.CompleteReportExportPrepare(finalizeCtx, runtimereportmodel.ReportExportPrepareCompletion{
		WorkspaceID: receipt.WorkspaceID, ReceiptID: receipt.ID, JobID: job.ID,
		LeaseOwner: receipt.LeaseOwner, FencingToken: receipt.FencingToken, Now: now, ExpiresAt: now.Add(reportExportReceiptRetention),
	}); err != nil {
		return reportexport.ExchangeJob{}, s.failRetryableReportExportPrepare(ctx, receipt, reportApplicationError(err))
	}
	return s.dataExchangeProvider.ProjectJob(ctx, job, principal)
}

func (s *ReportExportApplicationService) replayReportExportPrepare(ctx context.Context, receipt runtimereportmodel.ReportExportPrepareReceipt, principal principalmodel.Principal) (reportexport.ExchangeJob, error) {
	if receipt.Status == string(idempotency.StatusFailedTerminal) {
		code := strings.TrimSpace(receipt.TerminalErrorCode)
		if code == "" {
			code = idempotency.ErrorCodeReceiptUnavailable
		}
		return reportexport.ExchangeJob{}, &apperror.AppError{Kind: apperror.KindConflict, Code: code, Params: map[string]string{"use_case": "report.export.async"}}
	}
	if strings.TrimSpace(receipt.JobID) == "" || strings.TrimSpace(receipt.PayloadJSON) == "" {
		return reportexport.ExchangeJob{}, &apperror.AppError{Kind: apperror.KindConflict, Code: idempotency.ErrorCodeReceiptUnavailable}
	}
	payload, err := reportExportPayloadFromReceipt(receipt)
	if err != nil {
		return reportexport.ExchangeJob{}, err
	}
	job, err := s.dataExchange.Job(ctx, dataexchange.JobRequest{
		Scope: reportexport.Scope(principal), JobID: receipt.JobID, Provider: reportexport.DataExchangeProviderKey, Operation: "export",
	})
	if errors.Is(err, dataexchange.ErrJobNotFound) {
		return reportexport.ExchangeJob{}, &apperror.AppError{Kind: apperror.KindConflict, Code: idempotency.ErrorCodeReceiptUnavailable, Err: err}
	}
	if err != nil {
		return reportexport.ExchangeJob{}, err
	}
	canonical := payload
	canonical.AuditID = ""
	options, err := json.Marshal(canonical)
	if err != nil {
		return reportexport.ExchangeJob{}, reportApplicationError(err)
	}
	if err := validateReportExportReceiptJob(job, receipt, options); err != nil {
		return reportexport.ExchangeJob{}, err
	}
	return s.dataExchangeProvider.ProjectJob(ctx, job, principal)
}

func (s *ReportExportApplicationService) validateReportExportPrepareStatic(request reportmodel.ReportExportPrepareRequest, report reportmodel.ReportSchema, control reportmodel.ReportExportControlSchema, principal principalmodel.Principal) error {
	if _, err := principalmodel.CommandScopeForPrincipal(principal); err != nil {
		return reportWorkspaceError(err)
	}
	if s == nil || s.reportExports == nil || s.exportRecords == nil || s.prepareReceipts == nil || s.dataExchange == nil || s.dataExchangeProvider == nil {
		return reportApplicationError(nil)
	}
	if strings.TrimSpace(request.ReportKey) != strings.TrimSpace(report.Key) || strings.TrimSpace(report.Key) == "" ||
		strings.TrimSpace(control.ReportKey) != strings.TrimSpace(report.Key) || !reportExportControlIncludesObject(control, request.ObjectKey) || strings.TrimSpace(request.AuditID) == "" {
		return &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.report.export_control_not_found"}
	}
	return nil
}

func (s *ReportExportApplicationService) buildReportExportPayload(ctx context.Context, report reportmodel.ReportSchema, control reportmodel.ReportExportControlSchema, objectKey, auditID string, normalizedScope reportmodel.ReportExportScopeRequest, principal principalmodel.Principal) (reportexport.ExportPayload, error) {
	reportHash, err := reportcontract.CanonicalJSONSHA256(report)
	if err != nil {
		return reportexport.ExportPayload{}, err
	}
	sourceHash := reportHash
	if report.ObjectSQLV1 != nil {
		sourceHash, err = reportcontract.CanonicalJSONSHA256(report.ObjectSQLV1)
		if err != nil {
			return reportexport.ExportPayload{}, err
		}
	}
	authHash, err := reportadapter.ReportAccessScopeHash(principal)
	if err != nil {
		return reportexport.ExportPayload{}, err
	}
	controlHash, err := reportcontract.CanonicalJSONSHA256(control)
	if err != nil {
		return reportexport.ExportPayload{}, err
	}
	executionRequest := reportmodel.ReportExportExecutionRequest{ReportKey: report.Key, ObjectKey: objectKey, Scope: normalizedScope}
	rows, sourceVersion, exactTotal, err := s.probeReportExport(ctx, executionRequest, control.MaxRows, principal)
	if err != nil {
		return reportexport.ExportPayload{}, err
	}
	sourceVersionHash, err := reportcontract.CanonicalJSONSHA256(sourceVersion)
	if err != nil {
		return reportexport.ExportPayload{}, err
	}
	resultHash, csvHash := "", ""
	if exactTotal >= 0 {
		resultHash, err = reportExportResultHash(rows)
		if err != nil {
			return reportexport.ExportPayload{}, err
		}
		csvContent, encodeErr := reportexport.EncodePreflightCSV(rows, normalizedScope.FieldProjection, nil)
		if encodeErr != nil {
			return reportexport.ExportPayload{}, encodeErr
		}
		csvHash = reportcontract.SHA256Hex(csvContent)
	}
	return reportexport.ExportPayload{
		WorkspaceID: strings.TrimSpace(principal.WorkspaceID), RequesterUserID: strings.TrimSpace(principal.UserID),
		ReportKey: strings.TrimSpace(report.Key), ObjectKey: strings.TrimSpace(objectKey), AuditID: strings.TrimSpace(auditID), Scope: normalizedScope,
		ReportDefinitionSHA256: reportHash, ReportSourceSHA256: sourceHash, AuthorizationScopeSHA256: authHash, ControlDefinitionSHA256: controlHash,
		SourceVersion: sourceVersion, SourceVersionSHA256: sourceVersionHash, ResultSHA256: resultHash, CSVContentSHA256: csvHash,
		ExactTotal: exactTotal, MaxRows: control.MaxRows, Format: "csv",
	}, nil
}

func (s *ReportExportApplicationService) validateReportExportAudit(ctx context.Context, report reportmodel.ReportSchema, control reportmodel.ReportExportControlSchema, objectKey, auditID string, principal principalmodel.Principal) (string, error) {
	if _, err := principalmodel.CommandScopeForPrincipal(principal); err != nil {
		return "", reportWorkspaceError(err)
	}
	if s == nil || s.reportExports == nil || s.exportRecords == nil {
		return "", reportApplicationError(nil)
	}
	if strings.TrimSpace(report.Key) == "" || strings.TrimSpace(control.ReportKey) != strings.TrimSpace(report.Key) || !reportExportControlIncludesObject(control, objectKey) {
		return "", &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.report.export_control_not_found"}
	}
	auditRecord, err := s.exportRecords.GetReportRecord(ctx, control.AuditObject, strings.TrimSpace(auditID), principal)
	if err != nil {
		return "", err
	}
	mapping := control.RecordMapping
	if strings.TrimSpace(fmt.Sprint(auditRecord.Data[mapping.AuditReportKeyField])) != strings.TrimSpace(report.Key) {
		return "", &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.report.export_audit_report_mismatch"}
	}
	if strings.TrimSpace(fmt.Sprint(auditRecord.Data[mapping.AuditRequesterField])) != strings.TrimSpace(principal.UserID) {
		return "", &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.report.export_requester_mismatch"}
	}
	return strings.TrimSpace(fmt.Sprint(auditRecord.Data[mapping.AuditStatusField])), nil
}

func reportExportPrepareRequestFingerprint(request reportmodel.ReportExportPrepareRequest, report reportmodel.ReportSchema, control reportmodel.ReportExportControlSchema, principal principalmodel.Principal) (string, error) {
	reportHash, err := reportcontract.CanonicalJSONSHA256(report)
	if err != nil {
		return "", err
	}
	sourceHash := reportHash
	if report.ObjectSQLV1 != nil {
		sourceHash, err = reportcontract.CanonicalJSONSHA256(report.ObjectSQLV1)
		if err != nil {
			return "", err
		}
	}
	controlHash, err := reportcontract.CanonicalJSONSHA256(control)
	if err != nil {
		return "", err
	}
	authorizationHash, err := reportadapter.ReportAccessScopeHash(principal)
	if err != nil {
		return "", err
	}
	return reportExportPrepareFingerprint(reportexport.ExportPayload{
		WorkspaceID: strings.TrimSpace(principal.WorkspaceID), RequesterUserID: strings.TrimSpace(principal.UserID),
		ReportKey: strings.TrimSpace(report.Key), ObjectKey: strings.TrimSpace(request.ObjectKey), AuditID: strings.TrimSpace(request.AuditID),
		Scope: request.Scope, ReportDefinitionSHA256: reportHash, ReportSourceSHA256: sourceHash,
		AuthorizationScopeSHA256: authorizationHash, ControlDefinitionSHA256: controlHash, MaxRows: control.MaxRows, Format: "csv",
		RetryOfJobID: request.RetryOfJobID,
	})
}

func reportExportPrepareFingerprint(payload reportexport.ExportPayload) (string, error) {
	preconditions := map[string]any{
		"report_definition_sha256": payload.ReportDefinitionSHA256, "report_source_sha256": payload.ReportSourceSHA256,
		"authorization_scope_sha256": payload.AuthorizationScopeSHA256, "control_definition_sha256": payload.ControlDefinitionSHA256,
		"max_rows": payload.MaxRows,
	}
	if payload.RetryOfJobID != "" {
		preconditions["retry_of_job_id"] = payload.RetryOfJobID
	}
	return idempotency.Fingerprint(idempotency.FingerprintInput{
		UseCase: runtimereportmodel.ReportExportPrepareUseCase, ResourceType: "report_export_audit", TargetID: strings.TrimSpace(payload.AuditID),
		Payload: map[string]any{
			"report_key": strings.TrimSpace(payload.ReportKey), "object_key": strings.TrimSpace(payload.ObjectKey),
			"scope": payload.Scope, "format": strings.TrimSpace(payload.Format),
		},
		Preconditions: preconditions,
	})
}

func reportExportPayloadFromReceipt(receipt runtimereportmodel.ReportExportPrepareReceipt) (reportexport.ExportPayload, error) {
	var payload reportexport.ExportPayload
	if err := json.Unmarshal([]byte(receipt.PayloadJSON), &payload); err != nil {
		return payload, &apperror.AppError{Kind: apperror.KindConflict, Code: idempotency.ErrorCodeReceiptUnavailable, Err: err}
	}
	fingerprint, err := reportExportPrepareFingerprint(payload)
	if err != nil || strings.TrimSpace(payload.PrepareReceiptID) != receipt.ID || fingerprint != receipt.RequestFingerprint ||
		payload.WorkspaceID != receipt.WorkspaceID || payload.RequesterUserID != receipt.RequesterUserID || payload.ReportKey != receipt.ReportKey || payload.ObjectKey != receipt.ObjectKey || payload.AuditID != receipt.AuditID || payload.RetryOfJobID != receipt.RetryOfJobID ||
		receipt.BusinessJobKey != reportExportBusinessJobKey(receipt) || payload.ArtifactIdempotencyKey != reportExportArtifactKey(receipt) {
		return payload, &apperror.AppError{Kind: apperror.KindConflict, Code: idempotency.ErrorCodeReceiptUnavailable, Err: err}
	}
	return payload, nil
}

func validateReportExportReceiptJob(job dataexchange.Job, receipt runtimereportmodel.ReportExportPrepareReceipt, expectedOptions []byte) error {
	if job.Provider != reportexport.DataExchangeProviderKey || job.Operation != "export" || job.WorkspaceID != receipt.WorkspaceID ||
		job.ActorID != receipt.RequesterUserID || job.ObjectKey != receipt.ObjectKey || job.ReferenceID != receipt.AuditID ||
		(strings.TrimSpace(receipt.JobID) != "" && job.ID != receipt.JobID) || !bytes.Equal(job.Options, expectedOptions) {
		return &apperror.AppError{Kind: apperror.KindConflict, Code: idempotency.ErrorCodeReceiptUnavailable}
	}
	return nil
}

func reportExportControlIncludesObject(control reportmodel.ReportExportControlSchema, objectKey string) bool {
	objectKey = strings.TrimSpace(objectKey)
	for _, source := range control.SourceObjects {
		if strings.TrimSpace(source) == objectKey && objectKey != "" {
			return true
		}
	}
	return false
}

func reportExportResultHash(rows []reportmodel.ReportResultRow) (string, error) {
	return reportcontract.CanonicalJSONSHA256(map[string]any{"rows": rows, "source_row_count": -1, "snapshot": nil})
}

func reportExportBusinessJobKey(receipt runtimereportmodel.ReportExportPrepareReceipt) string {
	parts := []string{
		"domainry-runtime/report-export-job/v1", receipt.WorkspaceID, receipt.RequesterUserID, receipt.UseCase, receipt.ReportKey, receipt.ObjectKey, receipt.AuditID,
	}
	if receipt.RetryOfJobID != "" {
		parts = append(parts, "retry", receipt.RetryOfJobID)
	}
	digest := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return "report-export-job:" + hex.EncodeToString(digest[:])
}

func reportExportArtifactKey(receipt runtimereportmodel.ReportExportPrepareReceipt) string {
	digest := sha256.Sum256([]byte("domainry-runtime/report-export-artifact/v1\x00" + receipt.ID))
	return "report-export-artifact:" + hex.EncodeToString(digest[:])
}

func reportExportPrepareLeaseOwner(principal principalmodel.Principal) (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	requestID := strings.TrimSpace(principal.RequestID)
	if requestID == "" {
		requestID = "request"
	}
	return requestID + ":report-export:" + hex.EncodeToString(random), nil
}

func (s *ReportExportApplicationService) releaseFreshReportExportPrepare(ctx context.Context, receipt runtimereportmodel.ReportExportPrepareReceipt, cause error) error {
	finalizeCtx, cancel := reportExportFinalizeContext(ctx)
	defer cancel()
	err := s.prepareReceipts.ReleaseReportExportPrepare(finalizeCtx, runtimereportmodel.ReportExportPrepareFailure{
		WorkspaceID: receipt.WorkspaceID, ReceiptID: receipt.ID, LeaseOwner: receipt.LeaseOwner, FencingToken: receipt.FencingToken, Now: s.clock().UTC(),
	})
	if err != nil {
		return &apperror.AppError{Kind: apperror.KindConflict, Code: idempotency.ErrorCodeLeaseLost, Err: errors.Join(cause, err)}
	}
	return cause
}

func (s *ReportExportApplicationService) failRetryableReportExportPrepare(ctx context.Context, receipt runtimereportmodel.ReportExportPrepareReceipt, cause error) error {
	now := s.clock().UTC()
	finalizeCtx, cancel := reportExportFinalizeContext(ctx)
	defer cancel()
	err := s.prepareReceipts.FailReportExportPrepareRetryable(finalizeCtx, runtimereportmodel.ReportExportPrepareFailure{
		WorkspaceID: receipt.WorkspaceID, ReceiptID: receipt.ID, LeaseOwner: receipt.LeaseOwner, FencingToken: receipt.FencingToken, Now: s.clock().UTC(),
		ErrorCode: apperror.CodeOf(cause), ExpiresAt: now.Add(reportExportReceiptRetention),
	})
	if err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func (s *ReportExportApplicationService) failTerminalReportExportPrepare(ctx context.Context, receipt runtimereportmodel.ReportExportPrepareReceipt, cause error) error {
	now := s.clock().UTC()
	code := apperror.CodeOf(cause)
	if code == "" {
		code = idempotency.ErrorCodeReceiptUnavailable
	}
	finalizeCtx, cancel := reportExportFinalizeContext(ctx)
	defer cancel()
	err := s.prepareReceipts.FailReportExportPrepareTerminal(finalizeCtx, runtimereportmodel.ReportExportPrepareFailure{
		WorkspaceID: receipt.WorkspaceID, ReceiptID: receipt.ID, LeaseOwner: receipt.LeaseOwner, FencingToken: receipt.FencingToken,
		ErrorCode: code, Now: now, ExpiresAt: now.Add(reportExportReceiptRetention),
	})
	if err != nil {
		return errors.Join(cause, err)
	}
	return cause
}

func reportExportFinalizeContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), reportExportFinalizeTimeout)
}

func reportExportDataExchangeError(err error) error {
	if errors.Is(err, dataexchange.ErrIdempotencyKeyReused) {
		return reportExportIdempotencyConflict(err)
	}
	return err
}

func reportExportIdempotencyConflict(err error) error {
	return &apperror.AppError{Kind: apperror.KindConflict, Code: idempotency.ErrorCodeKeyReused, Err: err, Params: map[string]string{"use_case": "report.export.async"}}
}
