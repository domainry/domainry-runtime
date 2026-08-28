package report

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	recordapplication "github.com/domainry/domainry-runtime/runtime/application/record"
	reportexport "github.com/domainry/domainry-runtime/runtime/application/report/export"
	auditcontract "github.com/domainry/domainry-runtime/runtime/domain/audit/contract"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	reportservice "github.com/domainry/domainry-runtime/runtime/domain/report/service"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

const reportExportDownloadTTL = 15 * time.Minute

func reportExportRequiresAsync(exactAuthorizedTotal int) bool {
	return exactAuthorizedTotal < 0 || exactAuthorizedTotal > reportExportAsyncThreshold
}

func reportExportRoutesAsync(exactAuthorizedTotal int) bool {
	return reportExportRequiresAsync(exactAuthorizedTotal)
}

func reportApplicationError(err error) error {
	return &apperror.AppError{Kind: apperror.KindInternal, Code: "backend.internal_error", Err: err}
}

// reportExportAuditBinding resolves the immutable audit identity owned by the
// durable batch job. The canonical payload deliberately excludes audit_id so
// equivalent export requests can converge on one job, therefore recovery and
// finalization must never treat the payload as the source of truth.
func reportExportAuditBinding(job recordmodel.RecordBatchJob, payloadAuditID string) (string, error) {
	jobAuditID := strings.TrimSpace(job.AuditID)
	payloadAuditID = strings.TrimSpace(payloadAuditID)
	if jobAuditID == "" {
		return "", &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_audit_binding_invalid"}
	}
	if payloadAuditID != "" && payloadAuditID != jobAuditID {
		return "", &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_audit_binding_changed"}
	}
	return jobAuditID, nil
}

func reportWorkspaceError(err error) error {
	return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required", Err: err}
}

func reportCSVWithoutHeader(content []byte) []byte {
	if newline := bytes.IndexByte(content, '\n'); newline >= 0 {
		return content[newline+1:]
	}
	return nil
}

func reportCSVPages(content []byte, pageSize int) ([][]byte, int, error) {
	rows, err := csv.NewReader(bytes.NewReader(content)).ReadAll()
	if err != nil {
		return nil, 0, err
	}
	if len(rows) == 0 {
		return nil, 0, nil
	}
	header, data := rows[0], rows[1:]
	pages := make([][]byte, 0, (len(data)+pageSize-1)/pageSize)
	for start := 0; start < len(data); start += pageSize {
		end := start + pageSize
		if end > len(data) {
			end = len(data)
		}
		var output bytes.Buffer
		writer := csv.NewWriter(&output)
		if start == 0 {
			_ = writer.Write(header)
		}
		writer.WriteAll(data[start:end])
		if err := writer.Error(); err != nil {
			return nil, 0, err
		}
		pages = append(pages, output.Bytes())
	}
	return pages, len(data), nil
}

func (s *ReportApplicationService) processReportExportBatch(ctx context.Context, job *recordmodel.RecordBatchJob, principal principalmodel.Principal, writer recordapplication.RecordBatchPageWriter) error {
	var payload reportExportBatchPayload
	if err := json.Unmarshal([]byte(job.PayloadJSON), &payload); err != nil {
		return &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.report.export_job_payload_invalid", Err: err}
	}
	auditID, err := reportExportAuditBinding(*job, payload.AuditID)
	if err != nil {
		return err
	}
	// Normalize the in-memory request once, before any resumed page or final
	// artifact work, so every downstream audit consumer sees the same binding.
	payload.AuditID = auditID
	authHash, err := reportservice.ReportAccessScopeHash(principal)
	if err != nil || authHash != payload.AuthorizationScopeSHA256 {
		return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.report.export_scope_changed", Err: err}
	}
	currentReport, err := s.domain.ReportForExport(ctx, payload.ReportKey, payload.ObjectKey, principal)
	if err != nil {
		return err
	}
	reportHash, _ := reportexport.CanonicalJSONSHA256(currentReport)
	sourceHash := reportHash
	if currentReport.ObjectSQLV1 != nil {
		sourceHash, _ = reportexport.CanonicalJSONSHA256(currentReport.ObjectSQLV1)
	}
	if reportHash != payload.ReportDefinitionSHA256 || sourceHash != payload.ReportSourceSHA256 {
		return &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_source_changed"}
	}
	control, ok := s.exportControl(ctx, payload.ReportKey, payload.ObjectKey, principal)
	if !ok {
		return &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_scope_changed"}
	}
	normalizedScope, scopedReport, maskedDimensions, err := reportexport.NormalizeScope(currentReport, payload.ObjectKey, control, payload.Scope, principal)
	if err != nil {
		return err
	}
	verifySourceVersion := func() error {
		// Payloads created before source-version fencing remain readable, but all
		// newly prepared jobs must carry the version hash.
		if strings.TrimSpace(payload.SourceVersionSHA256) == "" {
			return nil
		}
		version, versionErr := s.domain.ExportSourceVersion(ctx, scopedReport, principal)
		if versionErr != nil {
			return versionErr
		}
		versionHash, hashErr := reportexport.CanonicalJSONSHA256(version)
		if hashErr != nil {
			return hashErr
		}
		if versionHash != payload.SourceVersionSHA256 || !reportservice.ReportSourceVersionsEqual(version, payload.SourceVersion) {
			return &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_source_changed"}
		}
		return nil
	}
	// Resume the same frozen Report through its opaque server cursor. Every page
	// commit atomically stores the immutable chunk and next cursor under the
	// current lease/fencing token, so retry cannot duplicate or omit rows.
	for payload.ExactTotal < 0 || job.Checkpoint < payload.ExactTotal {
		if err := verifySourceVersion(); err != nil {
			return err
		}
		summary, executeErr := s.domain.ExecuteExportReportPage(ctx, scopedReport, normalizedScope.Parameters, job.Checkpoint, reportmodel.ReportPageMaximumSize, principal)
		if executeErr != nil {
			return executeErr
		}
		pageRows, rowsErr := reportExportRows(summary, normalizedScope.AnalysisKey)
		if rowsErr != nil {
			return rowsErr
		}
		if len(pageRows) == 0 && (summary.Truncated || payload.ExactTotal >= 0 && job.Checkpoint < payload.ExactTotal) {
			return &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_result_changed"}
		}
		if len(pageRows) == 0 {
			break
		}
		processed := job.Checkpoint + len(pageRows)
		if payload.MaxRows > 0 && processed > payload.MaxRows {
			return &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.report.export_too_many_rows", Params: map[string]string{"limit": fmt.Sprint(payload.MaxRows)}}
		}
		if payload.ExactTotal >= 0 && processed > payload.ExactTotal {
			return &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_result_changed"}
		}
		content, encodeErr := reportexport.EncodeCSV(pageRows, normalizedScope.FieldProjection, maskedDimensions)
		if encodeErr != nil {
			return encodeErr
		}
		if job.ResultChunks > 0 {
			content = reportCSVWithoutHeader(content)
		}
		hasMore := summary.Truncated
		if payload.ExactTotal >= 0 {
			hasMore = processed < payload.ExactTotal
		}
		nextCursor, total := "", processed
		if hasMore {
			nextCursor, total = fmt.Sprintf("offset:%d", processed), processed+1
		}
		if err := writer.CommitPage(ctx, job, string(content), nextCursor, processed, total); err != nil {
			return err
		}
		if !hasMore {
			break
		}
	}
	if err := verifySourceVersion(); err != nil {
		return err
	}
	chunks, err := writer.Chunks(ctx, *job)
	if err != nil {
		return err
	}
	var frozenContent bytes.Buffer
	for _, chunk := range chunks {
		frozenContent.WriteString(chunk.Content)
	}
	expectedContent := frozenContent.Bytes()
	if strings.TrimSpace(payload.CSVContentSHA256) != "" && reportexport.SHA256Hex(expectedContent) != payload.CSVContentSHA256 {
		return &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_result_changed"}
	}
	exactTotal := job.Checkpoint
	auditRecord, err := s.exportRecords.GetReportRecord(ctx, control.AuditObject, auditID, principal)
	if err != nil {
		return err
	}
	mapping := control.RecordMapping
	if strings.TrimSpace(fmt.Sprint(auditRecord.Data[mapping.AuditReportKeyField])) != payload.ReportKey || strings.TrimSpace(fmt.Sprint(auditRecord.Data[mapping.AuditRequesterField])) != payload.RequesterUserID || !reportExportStatusAllowed(strings.TrimSpace(fmt.Sprint(auditRecord.Data[mapping.AuditStatusField])), mapping.AuditPreparedStatuses) {
		return &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.export_scope_changed"}
	}
	now := s.clock().UTC()
	expiresAt := now.Add(reportExportTTL(control)).Format(time.RFC3339Nano)
	if control.Watermark {
		watermark := s.reportExportWatermark(payload.ReportKey, principal.UserID, expiresAt)
		expectedContent, _ = reportexport.ApplyWatermark(expectedContent, watermark, expiresAt)
	}
	token, err := newReportExportToken()
	if err != nil {
		return reportApplicationError(err)
	}
	scopeHash, _ := reportexport.CanonicalJSONSHA256(normalizedScope)
	controlHash, _ := reportexport.CanonicalJSONSHA256(control)
	artifact, created, err := s.exportArtifacts.CreateOrGetReportExportArtifact(ctx, reportmodel.ReportExportArtifact{
		WorkspaceID: principal.WorkspaceID, ReportKey: payload.ReportKey, ObjectKey: payload.ObjectKey, AuditID: auditID,
		RequesterUserID: payload.RequesterUserID, RoleKey: principal.RoleKey, IdempotencyKey: payload.ArtifactIdempotencyKey,
		Token: token, Filename: reportexport.SafeFilename(payload.ReportKey, payload.ObjectKey), Scope: normalizedScope, ScopeSHA256: scopeHash,
		AuthorizationScopeSHA256: payload.AuthorizationScopeSHA256, ReportDefinitionSHA256: payload.ReportDefinitionSHA256, ControlDefinitionSHA256: controlHash,
		Content: expectedContent, ContentSHA256: reportexport.SHA256Hex(expectedContent), RowCount: exactTotal, Watermarked: control.Watermark,
		CreatedAt: now.Format(time.RFC3339Nano), ExpiresAt: expiresAt,
	})
	if errors.Is(err, reportcontract.ErrReportExportIdempotencyConflict) {
		return &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.idempotency.key_conflict", Err: err}
	}
	if err != nil {
		return reportApplicationError(err)
	}
	download, err := s.completeReportExportArtifact(ctx, artifact, created, control, auditRecord, reportExportParametersSHA256(normalizedScope.Parameters), payload.ArtifactIdempotencyKey, principal)
	if err != nil {
		return err
	}
	job.ResultFilename = download.Filename
	job.ResultType = "text/csv"
	job.ResultArtifactID = artifact.ID
	return nil
}

func (s *ReportApplicationService) auditExport(ctx context.Context, event, objectKey string, principal principalmodel.Principal, auditID string, metadata map[string]any) {
	if s == nil || s.audit == nil {
		return
	}
	_ = s.audit.AppendAudit(ctx, auditcontract.AuditAppendRequest{Event: event, ObjectKey: objectKey, RecordID: auditID, Principal: principal, Summary: event, Metadata: metadata})
}

func (s *ReportApplicationService) auditExportRequired(ctx context.Context, event, objectKey string, principal principalmodel.Principal, auditID string, metadata map[string]any) error {
	if s == nil || s.audit == nil {
		return errors.New("report export audit unavailable")
	}
	return s.audit.AppendAudit(ctx, auditcontract.AuditAppendRequest{Event: event, ObjectKey: objectKey, RecordID: auditID, Principal: principal, Summary: event, Metadata: metadata})
}

func (s *ReportApplicationService) auditExportRequiredIdempotent(ctx context.Context, idempotencyKey, event, objectKey string, principal principalmodel.Principal, auditID string, metadata map[string]any) error {
	if s == nil || s.audit == nil {
		return errors.New("report export audit unavailable")
	}
	return s.audit.AppendAudit(ctx, auditcontract.AuditAppendRequest{IdempotencyKey: idempotencyKey, Event: event, ObjectKey: objectKey, RecordID: auditID, Principal: principal, Summary: event, Metadata: metadata})
}

func (s *ReportApplicationService) reportExportJob(ctx context.Context, job recordmodel.RecordBatchJob, principal principalmodel.Principal) (ReportExportJob, error) {
	var payload reportExportBatchPayload
	_ = json.Unmarshal([]byte(job.PayloadJSON), &payload)
	status := job.Status
	switch status {
	case "queued", "retrying":
		status = "accepted"
	case "quarantined":
		status = "failed"
	}
	total := job.Total
	if total == 0 && payload.ExactTotal > 0 {
		total = payload.ExactTotal
	}
	result := ReportExportJob{ID: job.ID, BatchJobID: job.ID, AuditID: job.AuditID, ReportKey: payload.ReportKey, ObjectKey: payload.ObjectKey, Status: status, PagesCompleted: job.ResultChunks, RowsExported: job.Checkpoint, Total: total, Scope: payload.Scope, ArtifactID: job.ResultArtifactID, ErrorCode: job.ErrorCode, CreatedAt: job.CreatedAt, UpdatedAt: job.UpdatedAt}
	if status == "completed" {
		if job.ResultArtifactID == "" || s.exportRecords == nil {
			return ReportExportJob{}, reportExportProjectionIncomplete()
		}
		control, ok := s.exportControl(ctx, payload.ReportKey, payload.ObjectKey, principal)
		if !ok {
			return ReportExportJob{}, reportExportProjectionIncomplete()
		}
		artifactReader, readable := s.exportArtifacts.(reportcontract.ReportExportArtifactRequestReader)
		if !readable {
			return ReportExportJob{}, reportExportProjectionIncomplete()
		}
		artifact, found, artifactErr := artifactReader.ReportExportArtifactByIdempotency(ctx, principal.WorkspaceID, principal.UserID, payload.ReportKey, payload.ArtifactIdempotencyKey)
		if artifactErr != nil {
			return ReportExportJob{}, artifactErr
		}
		if !found || artifact.ID != job.ResultArtifactID || artifact.BusinessDownloadID == "" {
			return ReportExportJob{}, reportExportProjectionIncomplete()
		}
		record, err := s.exportRecords.GetReportRecord(ctx, control.DownloadObject, artifact.BusinessDownloadID, principal)
		if err != nil {
			return ReportExportJob{}, err
		}
		result.ArtifactID = artifact.ID
		result.DownloadToken = reportExportRecordString(record.Data, control.RecordMapping.DownloadTokenField)
		result.ExpiresAt = reportExportRecordString(record.Data, control.RecordMapping.DownloadExpiresAtField)
		result.ContentSHA256 = strings.TrimPrefix(reportExportRecordString(record.Data, control.RecordMapping.DownloadContentHashField), "sha256:")
		if result.DownloadToken == "" || result.ExpiresAt == "" || result.ContentSHA256 == "" {
			return ReportExportJob{}, reportExportProjectionIncomplete()
		}
	}
	return result, nil
}

func reportExportProjectionIncomplete() error {
	return &apperror.AppError{Kind: apperror.KindUnavailable, Code: "backend.report.export_projection_incomplete"}
}

func reportExportRecordString(data map[string]any, field string) string {
	if data == nil || strings.TrimSpace(field) == "" {
		return ""
	}
	value, found := data[field]
	if !found || value == nil {
		return ""
	}
	text, ok := value.(string)
	if !ok {
		return strings.TrimSpace(fmt.Sprint(value))
	}
	return strings.TrimSpace(text)
}
