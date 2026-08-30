package report

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	dataexchange "github.com/domainry/domainry-data-exchange-sdk"
	"github.com/domainry/domainry-foundation/apperror"
	reportexport "github.com/domainry/domainry-runtime/runtime/application/report/export"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	reportservice "github.com/domainry/domainry-runtime/runtime/domain/report/service"
)

const reportExportFingerprintDomain = "report_export"
const reportExportProbeRowLimit = 1000

type ReportExportPreparation struct {
	Job reportexport.ExchangeJob
}

func (s *ReportApplicationService) PrepareExportRouted(ctx context.Context, reportKey, objectKey, auditID, idempotencyKey string, scope reportmodel.ReportExportScopeRequest, principal principalmodel.Principal) (ReportExportPreparation, error) {
	payload, err := s.prepareReportExportPayload(ctx, reportKey, objectKey, auditID, idempotencyKey, scope, principal)
	if err != nil {
		return ReportExportPreparation{}, err
	}
	job, err := s.prepareExportJobFromPayload(ctx, payload, reportExportRequestFingerprint(payload), principal)
	return ReportExportPreparation{Job: job}, err
}

// PrepareExportJobScoped freezes the governed request after the bounded
// preflight and submits every export to Data Exchange. The provider never owns
// a second export-only query definition.
func (s *ReportApplicationService) PrepareExportJobScoped(ctx context.Context, reportKey, objectKey, auditID, idempotencyKey string, scope reportmodel.ReportExportScopeRequest, principal principalmodel.Principal) (reportexport.ExchangeJob, error) {
	payload, err := s.prepareReportExportPayload(ctx, reportKey, objectKey, auditID, idempotencyKey, scope, principal)
	if err != nil {
		return reportexport.ExchangeJob{}, err
	}
	return s.prepareExportJobFromPayload(ctx, payload, reportExportRequestFingerprint(payload), principal)
}

func (s *ReportApplicationService) prepareReportExportPayload(ctx context.Context, reportKey, objectKey, auditID, idempotencyKey string, scope reportmodel.ReportExportScopeRequest, principal principalmodel.Principal) (reportexport.ExportPayload, error) {
	if _, err := principalmodel.CommandScopeForPrincipal(principal); err != nil {
		return reportexport.ExportPayload{}, reportWorkspaceError(err)
	}
	if s == nil || s.domain == nil || s.exportRecords == nil {
		return reportexport.ExportPayload{}, reportApplicationError(nil)
	}
	if strings.TrimSpace(idempotencyKey) == "" {
		return reportexport.ExportPayload{}, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.idempotency.key_required"}
	}
	report, err := s.domain.ReportForExport(ctx, reportKey, objectKey, principal)
	if err != nil {
		return reportexport.ExportPayload{}, err
	}
	control, ok := s.exportControl(ctx, report.Key, objectKey, principal)
	if !ok {
		return reportexport.ExportPayload{}, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.report.export_control_not_found"}
	}
	auditID = strings.TrimSpace(auditID)
	auditRecord, err := s.exportRecords.GetReportRecord(ctx, control.AuditObject, auditID, principal)
	if err != nil {
		return reportexport.ExportPayload{}, err
	}
	mapping := control.RecordMapping
	if strings.TrimSpace(fmt.Sprint(auditRecord.Data[mapping.AuditReportKeyField])) != report.Key {
		return reportexport.ExportPayload{}, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.report.export_audit_report_mismatch"}
	}
	if strings.TrimSpace(fmt.Sprint(auditRecord.Data[mapping.AuditRequesterField])) != strings.TrimSpace(principal.UserID) {
		return reportexport.ExportPayload{}, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.report.export_requester_mismatch"}
	}
	if !reportExportStatusAllowed(strings.TrimSpace(fmt.Sprint(auditRecord.Data[mapping.AuditStatusField])), mapping.AuditPreparedStatuses) {
		return reportexport.ExportPayload{}, &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.report.export_audit_status_invalid"}
	}
	normalizedScope, scopedReport, maskedDimensions, err := reportexport.NormalizeScope(report, objectKey, control, scope, principal)
	if err != nil {
		return reportexport.ExportPayload{}, err
	}
	if _, err = reportexport.ValidateFieldAccess(ctx, s.domain, scopedReport, control, principal); err != nil {
		return reportexport.ExportPayload{}, err
	}
	reportHash, _ := reportexport.CanonicalJSONSHA256(report)
	sourceHash := reportHash
	if report.ObjectSQLV1 != nil {
		sourceHash, _ = reportexport.CanonicalJSONSHA256(report.ObjectSQLV1)
	}
	authHash, err := reportservice.ReportAccessScopeHash(principal)
	if err != nil {
		return reportexport.ExportPayload{}, err
	}
	controlHash, err := reportexport.CanonicalJSONSHA256(control)
	if err != nil {
		return reportexport.ExportPayload{}, err
	}
	rows, sourceVersion, exactTotal, err := s.probeReportExport(ctx, report, scopedReport, normalizedScope, control.MaxRows, principal)
	if err != nil {
		return reportexport.ExportPayload{}, err
	}
	sourceVersionHash, err := reportexport.CanonicalJSONSHA256(sourceVersion)
	if err != nil {
		return reportexport.ExportPayload{}, err
	}
	resultHash, csvHash := "", ""
	if exactTotal >= 0 {
		resultHash, err = reportPageResultHash(reportmodel.ReportSummary{Rows: rows, SourceRowCount: -1})
		if err != nil {
			return reportexport.ExportPayload{}, err
		}
		csvContent, encodeErr := reportexport.EncodePreflightCSV(rows, normalizedScope.FieldProjection, maskedDimensions)
		if encodeErr != nil {
			return reportexport.ExportPayload{}, encodeErr
		}
		csvHash = reportexport.SHA256Hex(csvContent)
	}
	return reportexport.ExportPayload{WorkspaceID: strings.TrimSpace(principal.WorkspaceID), RequesterUserID: strings.TrimSpace(principal.UserID), ReportKey: report.Key, ObjectKey: strings.TrimSpace(objectKey), AuditID: auditID, Scope: normalizedScope, ReportDefinitionSHA256: reportHash, ReportSourceSHA256: sourceHash, AuthorizationScopeSHA256: authHash, ControlDefinitionSHA256: controlHash, SourceVersion: sourceVersion, SourceVersionSHA256: sourceVersionHash, ResultSHA256: resultHash, CSVContentSHA256: csvHash, ExactTotal: exactTotal, MaxRows: control.MaxRows, Format: "csv"}, nil
}

// reportExportRequestFingerprint intentionally excludes the caller supplied
// idempotency key and audit record identity. It represents the canonical export
// condition and frozen result version, so a second audit/request cannot create
// another file for the same requester, scope and result.
func reportExportRequestFingerprint(payload reportexport.ExportPayload) string {
	canonical := struct {
		WorkspaceID, RequesterUserID, ReportKey, ObjectKey, ReportDefinitionSHA256, ReportSourceSHA256, AuthorizationScopeSHA256, ControlDefinitionSHA256, SourceVersionSHA256, ResultSHA256, CSVContentSHA256, Format string
		ExactTotal                                                                                                                                                                                                     int
		MaxRows                                                                                                                                                                                                        int
		Scope                                                                                                                                                                                                          reportmodel.ReportExportScopeRequest
	}{payload.WorkspaceID, payload.RequesterUserID, payload.ReportKey, payload.ObjectKey, payload.ReportDefinitionSHA256, payload.ReportSourceSHA256, payload.AuthorizationScopeSHA256, payload.ControlDefinitionSHA256, payload.SourceVersionSHA256, payload.ResultSHA256, payload.CSVContentSHA256, payload.Format, payload.ExactTotal, payload.MaxRows, payload.Scope}
	raw, _ := json.Marshal(canonical)
	digest := sha256.Sum256(append([]byte(reportExportFingerprintDomain+"\x00"), raw...))
	return hex.EncodeToString(digest[:])
}

func (s *ReportApplicationService) prepareExportJobFromPayload(ctx context.Context, payload reportexport.ExportPayload, fingerprint string, principal principalmodel.Principal) (reportexport.ExchangeJob, error) {
	if s == nil || s.dataExchange == nil || s.dataExchangeProvider == nil {
		return reportexport.ExchangeJob{}, reportApplicationError(nil)
	}
	baseFingerprint := fingerprint
	for generation := 0; generation < 32; generation++ {
		candidate := payload
		candidate.ArtifactIdempotencyKey = "report-export-business:" + fingerprint
		canonical := candidate
		canonical.AuditID = ""
		raw, _ := json.Marshal(canonical)
		job, replayed, err := s.dataExchange.SubmitExport(ctx, dataexchange.ExportRequest{
			Scope: reportexport.Scope(principal), Provider: reportexport.DataExchangeProviderKey,
			ObjectKey: payload.ObjectKey, IdempotencyKey: fingerprint, ReferenceID: payload.AuditID, Options: raw,
		})
		if err != nil {
			return reportexport.ExchangeJob{}, err
		}
		var stored reportexport.ExportPayload
		if err := json.Unmarshal(job.Options, &stored); err != nil || reportExportRequestFingerprint(stored) != baseFingerprint {
			return reportexport.ExchangeJob{}, &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.idempotency.key_reused", Err: err, Params: map[string]string{"use_case": "report.export.async"}}
		}
		projection, projectionErr := s.dataExchangeProvider.ProjectJob(ctx, job, principal)
		if projectionErr != nil {
			return reportexport.ExchangeJob{}, projectionErr
		}
		if !replayed || s.reusableReportExportExchangeJob(projection) {
			if replayed && strings.TrimSpace(payload.AuditID) != strings.TrimSpace(job.ReferenceID) {
				if err := s.dataExchangeProvider.CloseReplayAudit(ctx, payload, job, projection, principal); err != nil {
					return reportexport.ExchangeJob{}, err
				}
			}
			return projection, nil
		}
		fingerprint = baseFingerprint + ":renew:" + job.UpdatedAt.UTC().Format(time.RFC3339Nano)
	}
	return reportexport.ExchangeJob{}, &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_replay_generation_exhausted"}
}

func (s *ReportApplicationService) reusableReportExportExchangeJob(job reportexport.ExchangeJob) bool {
	switch job.Status {
	case "accepted", "running":
		return true
	case "completed":
		expiresAt, err := time.Parse(time.RFC3339Nano, job.ExpiresAt)
		return err == nil && s.clock().UTC().Before(expiresAt) && job.ArtifactID != "" && job.ContentSHA256 != ""
	default:
		return false
	}
}

func (s *ReportApplicationService) GetExportJob(ctx context.Context, jobID string, principal principalmodel.Principal) (reportexport.ExchangeJob, error) {
	if s == nil || s.dataExchange == nil || s.dataExchangeProvider == nil {
		return reportexport.ExchangeJob{}, reportApplicationError(nil)
	}
	job, err := s.dataExchange.Job(ctx, dataexchange.JobRequest{Scope: reportexport.Scope(principal), JobID: strings.TrimSpace(jobID)})
	if err != nil {
		return reportexport.ExchangeJob{}, err
	}
	return s.dataExchangeProvider.ProjectJob(ctx, job, principal)
}

func (s *ReportApplicationService) CancelExportJob(ctx context.Context, jobID string, principal principalmodel.Principal) (reportexport.ExchangeJob, error) {
	if s == nil || s.dataExchange == nil || s.dataExchangeProvider == nil {
		return reportexport.ExchangeJob{}, reportApplicationError(nil)
	}
	job, err := s.dataExchange.Cancel(ctx, dataexchange.JobRequest{Scope: reportexport.Scope(principal), JobID: strings.TrimSpace(jobID)})
	if err != nil {
		return reportexport.ExchangeJob{}, err
	}
	return s.dataExchangeProvider.ProjectJob(ctx, job, principal)
}
