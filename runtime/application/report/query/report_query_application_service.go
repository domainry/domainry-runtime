package query

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	reportexport "github.com/domainry/domainry-runtime/runtime/application/report/export"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	reportservice "github.com/domainry/domainry-runtime/runtime/domain/report/query"
)

type ReportQueryApplicationDependencies struct {
	Domain    *reportservice.ReportDomainService
	CursorKey []byte
	Audit     ReportCrossWorkspaceExecutionAudit
}

func reportApplicationError(err error) error {
	return &apperror.AppError{Kind: apperror.KindInternal, Code: "backend.internal_error", Err: err}
}

func reportWorkspaceError(err error) error {
	return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.workspace_scope_required", Err: err}
}

// ReportQueryApplicationService owns interactive report authorization,
// selector narrowing, and stable bounded pagination.
type ReportQueryApplicationService struct {
	domain    *reportservice.ReportDomainService
	cursorKey []byte
	audit     ReportCrossWorkspaceExecutionAudit
}

func NewReportQueryApplicationService(dependencies ReportQueryApplicationDependencies) *ReportQueryApplicationService {
	return &ReportQueryApplicationService{domain: dependencies.Domain, cursorKey: append([]byte(nil), dependencies.CursorKey...), audit: dependencies.Audit}
}

type reportPageCursor struct {
	Version     int    `json:"v"`
	Fingerprint string `json:"fingerprint"`
	LastRowKey  string `json:"last_row_key"`
	Occurrence  int    `json:"occurrence"`
	Checksum    string `json:"checksum"`
}

type reportExecutionCursor struct {
	Version             int    `json:"v"`
	Fingerprint         string `json:"fingerprint"`
	SourceVersionSHA256 string `json:"source_version_sha256"`
	StoreCursor         string `json:"store_cursor"`
	Position            int    `json:"position"`
	Checksum            string `json:"checksum"`
}

func reportPageFingerprint(report reportmodel.ReportSchema, selectors any, principal principalmodel.Principal) (string, error) {
	accessHash, err := reportservice.ReportAccessScopeHash(principal)
	if err != nil {
		return "", err
	}
	content, err := json.Marshal(map[string]any{"report": report, "selectors": selectors, "access_scope_sha256": accessHash})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:]), nil
}

func reportPageResultHash(summary reportmodel.ReportSummary) (string, error) {
	content, err := json.Marshal(map[string]any{"rows": summary.Rows, "analyses": summary.Analyses, "source_row_count": summary.SourceRowCount, "snapshot": summary.Snapshot})
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:]), nil
}

// paginateReportRows preserves the Report-owned order and uses the canonical
// complete row plus its occurrence as the unique tie-breaker. A stale cursor
// fails closed instead of silently skipping or duplicating rows.
func paginateReportRows(rows []reportmodel.ReportResultRow, request reportmodel.ReportPageRequest, fingerprint string, key []byte) ([]reportmodel.ReportResultRow, int, string, bool, error) {
	pageSize := request.PageSize
	if pageSize == 0 {
		pageSize = reportmodel.ReportPageDefaultSize
	}
	if pageSize < 1 || pageSize > reportmodel.ReportPageMaximumSize {
		return nil, 0, "", false, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.report.page_size_invalid", Params: map[string]string{"maximum": fmt.Sprint(reportmodel.ReportPageMaximumSize)}}
	}
	start := 0
	if strings.TrimSpace(request.Cursor) != "" {
		cursor, err := decodeReportPageCursor(request.Cursor, fingerprint, key)
		if err != nil {
			return nil, 0, "", false, err
		}
		start = reportRowAfterCursor(rows, cursor)
		if start < 0 {
			return nil, 0, "", false, &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.cursor_stale"}
		}
	}
	total := len(rows)
	end := start + pageSize
	if end > total {
		end = total
	}
	// A report list is always a JSON array. append(nil, empty...) preserves a
	// nil slice, which encodes as null and breaks otherwise valid list clients.
	page := make([]reportmodel.ReportResultRow, end-start)
	copy(page, rows[start:end])
	truncated := end < total
	next := ""
	if truncated && len(page) > 0 {
		rowKey := reportStableRowKey(page[len(page)-1])
		occurrence := 0
		for index := 0; index < end; index++ {
			if reportStableRowKey(rows[index]) == rowKey {
				occurrence++
			}
		}
		next = encodeReportPageCursor(reportPageCursor{Version: 1, Fingerprint: fingerprint, LastRowKey: rowKey, Occurrence: occurrence}, key)
	}
	return page, total, next, truncated, nil
}

func paginateReportSummaryStable(summary reportmodel.ReportSummary, request reportmodel.ReportPageRequest, fingerprint string, key []byte) (reportmodel.ReportSummary, error) {
	rows, total, next, truncated, err := paginateReportRows(summary.Rows, request, fingerprint, key)
	if err != nil {
		return reportmodel.ReportSummary{}, err
	}
	summary.Rows, summary.RowCount = rows, len(rows)
	summary.PageSize = request.PageSize
	if summary.PageSize == 0 {
		summary.PageSize = reportmodel.ReportPageDefaultSize
	}
	summary.NextCursor, summary.Truncated = next, truncated
	summary.Total, summary.TotalSemantics = total, reportmodel.ReportTotalExact
	return summary, nil
}

func reportRowAfterCursor(rows []reportmodel.ReportResultRow, cursor reportPageCursor) int {
	occurrence := 0
	for index, row := range rows {
		if reportStableRowKey(row) != cursor.LastRowKey {
			continue
		}
		occurrence++
		if occurrence == cursor.Occurrence {
			return index + 1
		}
	}
	return -1
}

func reportStableRowKey(row reportmodel.ReportResultRow) string {
	content, _ := json.Marshal(row)
	digest := sha256.Sum256(content)
	return hex.EncodeToString(digest[:])
}

func encodeReportPageCursor(cursor reportPageCursor, key []byte) string {
	cursor.Checksum = reportCursorChecksum(cursor, key)
	content, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(content)
}

func decodeReportPageCursor(value, fingerprint string, key []byte) (reportPageCursor, error) {
	content, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return reportPageCursor{}, reportCursorError(err)
	}
	var cursor reportPageCursor
	if err := json.Unmarshal(content, &cursor); err != nil || cursor.Version != 1 || cursor.Fingerprint != fingerprint || cursor.Occurrence < 1 || cursor.LastRowKey == "" || !hmac.Equal([]byte(cursor.Checksum), []byte(reportCursorChecksum(cursor, key))) {
		return reportPageCursor{}, reportCursorError(err)
	}
	return cursor, nil
}

func reportCursorChecksum(cursor reportPageCursor, key []byte) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(fmt.Sprintf("%d\x00%s\x00%s\x00%d", cursor.Version, cursor.Fingerprint, cursor.LastRowKey, cursor.Occurrence)))
	return hex.EncodeToString(mac.Sum(nil))
}

func reportCursorError(err error) error {
	return &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.report.cursor_invalid", Err: err}
}

func encodeReportExecutionCursor(cursor reportExecutionCursor, key []byte) string {
	cursor.Checksum = reportExecutionCursorChecksum(cursor, key)
	content, _ := json.Marshal(cursor)
	return base64.RawURLEncoding.EncodeToString(content)
}

func decodeReportExecutionCursor(value, fingerprint string, key []byte) (reportExecutionCursor, error) {
	content, err := base64.RawURLEncoding.DecodeString(strings.TrimSpace(value))
	if err != nil {
		return reportExecutionCursor{}, reportCursorError(err)
	}
	var cursor reportExecutionCursor
	if err := json.Unmarshal(content, &cursor); err != nil || cursor.Version != 4 || cursor.Fingerprint != fingerprint || strings.TrimSpace(cursor.SourceVersionSHA256) == "" || strings.TrimSpace(cursor.StoreCursor) == "" || cursor.Position < 1 || !hmac.Equal([]byte(cursor.Checksum), []byte(reportExecutionCursorChecksum(cursor, key))) {
		return reportExecutionCursor{}, reportCursorError(err)
	}
	return cursor, nil
}

func reportExecutionCursorChecksum(cursor reportExecutionCursor, key []byte) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(fmt.Sprintf("%d\x00%s\x00%s\x00%s\x00%d", cursor.Version, cursor.Fingerprint, cursor.SourceVersionSHA256, cursor.StoreCursor, cursor.Position)))
	return hex.EncodeToString(mac.Sum(nil))
}

// executeStableReportKeysetPage keeps bounded keyset traversal honest across
// separate HTTP requests. The signed cursor is tied to the authorized source
// version, and every page is fenced by a before/after version read. A first
// page may retry a short concurrent-write window; a continued traversal fails
// closed because returning it against a new version could duplicate or omit
// rows already delivered to the client.
func (s *ReportQueryApplicationService) executeStableReportKeysetPage(
	ctx context.Context,
	report reportmodel.ReportSchema,
	fingerprint string,
	page reportmodel.ReportPageRequest,
	pageSize int,
	principal principalmodel.Principal,
	execute func(storeCursor string, position, size int) (reportmodel.ReportSummary, error),
) (reportmodel.ReportSummary, error) {
	const consistencyRetries = 3
	position := 0
	storeCursor := ""
	expectedSourceVersion := ""
	continued := strings.TrimSpace(page.Cursor) != ""
	if continued {
		cursor, err := decodeReportExecutionCursor(page.Cursor, fingerprint, s.cursorKey)
		if err != nil {
			return reportmodel.ReportSummary{}, err
		}
		storeCursor, position, expectedSourceVersion = cursor.StoreCursor, cursor.Position, cursor.SourceVersionSHA256
	}
	for attempt := 0; attempt < consistencyRetries; attempt++ {
		before, err := s.domain.ExportSourceVersion(ctx, report, principal)
		if err != nil {
			return reportmodel.ReportSummary{}, err
		}
		beforeHash, err := reportexport.CanonicalJSONSHA256(before)
		if err != nil {
			return reportmodel.ReportSummary{}, reportApplicationError(err)
		}
		if continued && beforeHash != expectedSourceVersion {
			return reportmodel.ReportSummary{}, &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.cursor_stale"}
		}
		summary, err := execute(storeCursor, position, pageSize)
		if err != nil {
			return reportmodel.ReportSummary{}, err
		}
		if len(summary.Rows) > pageSize || summary.Truncated && len(summary.Rows) == 0 {
			return reportmodel.ReportSummary{}, &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.page_result_changed"}
		}
		after, err := s.domain.ExportSourceVersion(ctx, report, principal)
		if err != nil {
			return reportmodel.ReportSummary{}, err
		}
		if reportservice.ReportSourceVersionsEqual(before, after) {
			summary.NextCursor = ""
			if summary.Truncated {
				if strings.TrimSpace(summary.ExecutionCursor) == "" {
					return reportmodel.ReportSummary{}, &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.page_cursor_missing"}
				}
				summary.NextCursor = encodeReportExecutionCursor(reportExecutionCursor{Version: 4, Fingerprint: fingerprint, SourceVersionSHA256: beforeHash, StoreCursor: summary.ExecutionCursor, Position: position + len(summary.Rows)}, s.cursorKey)
			}
			return summary, nil
		}
		if continued {
			return reportmodel.ReportSummary{}, &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.cursor_stale"}
		}
	}
	return reportmodel.ReportSummary{}, &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.report.source_changed"}
}

// SummaryScoped executes the same Report-owned query/tag predicate contract
// used by governed export, so a page and its CSV cannot disagree on selector
// semantics. Scoped snapshots are rejected because their bytes were computed
// before the request selectors existed.
func (s *ReportQueryApplicationService) SummaryScoped(ctx context.Context, reportKey, mode, queryKey string, tags []string, principal principalmodel.Principal) (reportmodel.ReportSummary, error) {
	if _, err := principalmodel.QueryScopeForPrincipal(principal); err != nil {
		return reportmodel.ReportSummary{}, reportWorkspaceError(err)
	}
	if strings.TrimSpace(queryKey) == "" && len(tags) == 0 {
		return s.domain.SummaryMode(ctx, reportKey, mode, principal)
	}
	if value := strings.TrimSpace(mode); value != "" && value != "realtime" {
		return reportmodel.ReportSummary{}, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.report.snapshot_scope_unsupported"}
	}
	report, err := s.domain.ReportForSummary(ctx, reportKey, principal)
	if err != nil {
		return reportmodel.ReportSummary{}, err
	}
	scoped, _, _, err := reportexport.ApplyDeclaredPredicates(report, queryKey, tags, nil, nil, false)
	if err != nil {
		return reportmodel.ReportSummary{}, err
	}
	return s.domain.ExecuteExportReport(ctx, scoped, nil, principal)
}

func (s *ReportQueryApplicationService) SummaryScopedPage(ctx context.Context, reportKey, mode, queryKey string, tags []string, page reportmodel.ReportPageRequest, principal principalmodel.Principal) (reportmodel.ReportSummary, error) {
	if _, err := principalmodel.QueryScopeForPrincipal(principal); err != nil {
		return reportmodel.ReportSummary{}, reportWorkspaceError(err)
	}
	report, err := s.domain.ReportForSummary(ctx, reportKey, principal)
	if err != nil {
		return reportmodel.ReportSummary{}, err
	}
	if value := strings.TrimSpace(mode); value != "" && value != "realtime" {
		if strings.TrimSpace(queryKey) != "" || len(tags) > 0 {
			return reportmodel.ReportSummary{}, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.report.snapshot_scope_unsupported"}
		}
		return reportmodel.ReportSummary{}, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.report.bounded_page_unavailable"}
	}
	scoped := report
	if strings.TrimSpace(queryKey) != "" || len(tags) > 0 {
		if report.ObjectSQLV1 != nil {
			return reportmodel.ReportSummary{}, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.report.object_sql_scope_unsupported"}
		}
		scoped, _, _, err = reportexport.ApplyDeclaredPredicates(report, queryKey, tags, nil, nil, false)
		if err != nil {
			return reportmodel.ReportSummary{}, err
		}
	}
	pageSize := page.PageSize
	if pageSize == 0 {
		pageSize = reportmodel.ReportPageDefaultSize
	}
	if pageSize < 1 || pageSize > reportmodel.ReportPageMaximumSize {
		return reportmodel.ReportSummary{}, &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.report.page_size_invalid", Params: map[string]string{"maximum": fmt.Sprint(reportmodel.ReportPageMaximumSize)}}
	}
	fingerprint, err := reportPageFingerprint(report, map[string]any{"mode": "realtime", "query_key": strings.TrimSpace(queryKey), "tags": tags}, principal)
	if err != nil {
		return reportmodel.ReportSummary{}, reportApplicationError(err)
	}
	return s.executeStableReportKeysetPage(ctx, scoped, fingerprint, page, pageSize, principal, func(cursor string, position, size int) (reportmodel.ReportSummary, error) {
		return s.domain.ExecuteExportReportPage(ctx, scoped, nil, cursor, position, size, principal)
	})
}
