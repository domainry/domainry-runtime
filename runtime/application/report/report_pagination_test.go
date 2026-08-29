package report

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	"github.com/domainry/domainry-foundation/apperror"
	reportexport "github.com/domainry/domainry-runtime/runtime/application/report/export"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	reportmodel "github.com/domainry/domainry-runtime/runtime/domain/report/model"
	reportservice "github.com/domainry/domainry-runtime/runtime/domain/report/service"
)

func TestObjectSQLPaginationPushesBoundedWindowIntoExecutor(t *testing.T) {
	report := reportmodel.ReportSchema{Key: "bounded", ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{
		SQL: "SELECT l.id AS id FROM ledger l ORDER BY l.id LIMIT 20", SourceObjects: []string{"ledger"},
		ResultSchema: []reportmodel.ReportResultColumnSchema{{Key: "id", Type: "text", Kind: "dimension"}},
	}}
	executor := &reportExportObjectSQLExecutorStub{rows: []map[string]string{{"id": "1"}, {"id": "2"}, {"id": "3"}, {"id": "4"}, {"id": "5"}}}
	domain := reportservice.NewReportDomainService(reportservice.ReportDependencies{
		Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
			return []reportmodel.ReportSchema{report}
		},
		Access:          reportExportDatasetAccessStub{objects: map[string]definitionmodel.ObjectSchema{"ledger": {Key: "ledger", Fields: []definitionmodel.FieldSchema{{Key: "id", Type: "text"}}}}},
		ObjectSQL:       executor,
		SnapshotSources: reportSnapshotSourceStub{},
	})
	service := NewReportApplicationService(ReportApplicationDependencies{Domain: domain, CursorKey: []byte("bounded-page-key")})
	principal := reportPrincipal()
	first, err := service.QueryObjectSQLPage(t.Context(), report.Key, nil, reportmodel.ReportPageRequest{PageSize: 2}, principal)
	if err != nil || len(first.Rows) != 2 || !first.Truncated || first.NextCursor == "" || first.Total != 5 || first.TotalSemantics != reportmodel.ReportTotalExact {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	second, err := service.QueryObjectSQLPage(t.Context(), report.Key, nil, reportmodel.ReportPageRequest{PageSize: 2, Cursor: first.NextCursor}, principal)
	if err != nil || len(second.Rows) != 2 || !second.Truncated || second.Rows[0].Dimensions["id"] != "3" {
		t.Fatalf("second=%+v err=%v", second, err)
	}
	third, err := service.QueryObjectSQLPage(t.Context(), report.Key, nil, reportmodel.ReportPageRequest{PageSize: 2, Cursor: second.NextCursor}, principal)
	if err != nil || len(third.Rows) != 1 || third.Truncated || third.NextCursor != "" || third.Total != 5 || third.TotalSemantics != reportmodel.ReportTotalExact {
		t.Fatalf("third=%+v err=%v", third, err)
	}
	if len(executor.requests) != 3 || executor.requests[0].PagePosition != 0 || executor.requests[1].PagePosition != 2 || executor.requests[2].PagePosition != 4 || executor.requests[0].PageCursor != "" || executor.requests[1].PageCursor != "cursor:2" || executor.requests[2].PageCursor != "cursor:4" {
		t.Fatalf("executor requests=%+v", executor.requests)
	}
	for _, request := range executor.requests {
		if request.PageSize != 2 {
			t.Fatalf("unbounded executor request=%+v", request)
		}
	}
}

type reportPaginationSourceVersions struct {
	versions []reportmodel.ReportSnapshotSourceVersion
	calls    int
}

func (s *reportPaginationSourceVersions) ReadReportSnapshotSourceVersion(context.Context, reportcontract.ReportSnapshotSourceVersionRequest) (reportmodel.ReportSnapshotSourceVersion, error) {
	index := s.calls
	s.calls++
	if index >= len(s.versions) {
		index = len(s.versions) - 1
	}
	return s.versions[index], nil
}

func TestObjectSQLPaginationRejectsCursorAfterSourceChange(t *testing.T) {
	report := reportmodel.ReportSchema{Key: "bounded", ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{
		SQL: "SELECT l.id AS id FROM ledger l ORDER BY l.id LIMIT 20", SourceObjects: []string{"ledger"},
		ResultSchema: []reportmodel.ReportResultColumnSchema{{Key: "id", Type: "text", Kind: "dimension"}},
	}}
	v1 := reportmodel.ReportSnapshotSourceVersion{Watermark: "v1", SourceVersions: map[string]string{"ledger": "1:v1"}}
	v2 := reportmodel.ReportSnapshotSourceVersion{Watermark: "v2", SourceVersions: map[string]string{"ledger": "2:v2"}}
	versions := &reportPaginationSourceVersions{versions: []reportmodel.ReportSnapshotSourceVersion{v1, v1, v2}}
	executor := &reportExportObjectSQLExecutorStub{rows: []map[string]string{{"id": "1"}, {"id": "2"}, {"id": "3"}}}
	domain := reportservice.NewReportDomainService(reportservice.ReportDependencies{
		Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
			return []reportmodel.ReportSchema{report}
		},
		Access:    reportExportDatasetAccessStub{objects: map[string]definitionmodel.ObjectSchema{"ledger": {Key: "ledger", Fields: []definitionmodel.FieldSchema{{Key: "id", Type: "text"}}}}},
		ObjectSQL: executor, SnapshotSources: versions,
	})
	service := NewReportApplicationService(ReportApplicationDependencies{Domain: domain, CursorKey: []byte("bounded-page-key")})
	first, err := service.QueryObjectSQLPage(t.Context(), report.Key, nil, reportmodel.ReportPageRequest{PageSize: 2}, reportPrincipal())
	if err != nil || first.NextCursor == "" {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	_, err = service.QueryObjectSQLPage(t.Context(), report.Key, nil, reportmodel.ReportPageRequest{PageSize: 2, Cursor: first.NextCursor}, reportPrincipal())
	if apperror.CodeOf(err) != "backend.report.cursor_stale" || len(executor.requests) != 1 {
		t.Fatalf("stale cursor err=%v requests=%d", err, len(executor.requests))
	}
}

func TestObjectSQLPaginationRetriesFirstPageSourceChange(t *testing.T) {
	report := reportmodel.ReportSchema{Key: "bounded", ObjectSQLV1: &reportmodel.ReportObjectSQLSchema{
		SQL: "SELECT l.id AS id FROM ledger l ORDER BY l.id LIMIT 20", SourceObjects: []string{"ledger"},
		ResultSchema: []reportmodel.ReportResultColumnSchema{{Key: "id", Type: "text", Kind: "dimension"}},
	}}
	v1 := reportmodel.ReportSnapshotSourceVersion{Watermark: "v1", SourceVersions: map[string]string{"ledger": "1:v1"}}
	v2 := reportmodel.ReportSnapshotSourceVersion{Watermark: "v2", SourceVersions: map[string]string{"ledger": "2:v2"}}
	versions := &reportPaginationSourceVersions{versions: []reportmodel.ReportSnapshotSourceVersion{v1, v2, v2, v2}}
	executor := &reportExportObjectSQLExecutorStub{rows: []map[string]string{{"id": "1"}, {"id": "2"}, {"id": "3"}}}
	domain := reportservice.NewReportDomainService(reportservice.ReportDependencies{
		Reports: func(context.Context, principalmodel.Principal) []reportmodel.ReportSchema {
			return []reportmodel.ReportSchema{report}
		},
		Access:    reportExportDatasetAccessStub{objects: map[string]definitionmodel.ObjectSchema{"ledger": {Key: "ledger", Fields: []definitionmodel.FieldSchema{{Key: "id", Type: "text"}}}}},
		ObjectSQL: executor, SnapshotSources: versions,
	})
	service := NewReportApplicationService(ReportApplicationDependencies{Domain: domain, CursorKey: []byte("bounded-page-key")})
	first, err := service.QueryObjectSQLPage(t.Context(), report.Key, nil, reportmodel.ReportPageRequest{PageSize: 2}, reportPrincipal())
	if err != nil || first.NextCursor == "" || len(executor.requests) != 2 || versions.calls != 4 {
		t.Fatalf("first=%+v err=%v requests=%d version_calls=%d", first, err, len(executor.requests), versions.calls)
	}
}

func TestReportPaginationContractStableOpaqueCursor(t *testing.T) {
	rows := []reportmodel.ReportResultRow{
		{Dimensions: map[string]string{"id": "1"}},
		{Dimensions: map[string]string{"id": "2"}},
		{Dimensions: map[string]string{"id": "3"}},
	}
	key := []byte("cursor-key")
	first, total, cursor, truncated, err := paginateReportRows(rows, reportmodel.ReportPageRequest{PageSize: 2}, "query-a", key)
	if err != nil || len(first) != 2 || total != 3 || !truncated || cursor == "" {
		t.Fatalf("first page rows=%d total=%d cursor=%q truncated=%v err=%v", len(first), total, cursor, truncated, err)
	}
	second, total, next, truncated, err := paginateReportRows(rows, reportmodel.ReportPageRequest{PageSize: 2, Cursor: cursor}, "query-a", key)
	if err != nil || len(second) != 1 || total != 3 || truncated || next != "" || second[0].Dimensions["id"] != "3" {
		t.Fatalf("second page=%+v total=%d next=%q truncated=%v err=%v", second, total, next, truncated, err)
	}
	if _, _, _, _, err := paginateReportRows(rows, reportmodel.ReportPageRequest{PageSize: 2, Cursor: cursor + "x"}, "query-a", key); apperror.CodeOf(err) != "backend.report.cursor_invalid" {
		t.Fatalf("tampered cursor error=%v", err)
	}
	if _, _, _, _, err := paginateReportRows(rows, reportmodel.ReportPageRequest{PageSize: 2, Cursor: cursor}, "query-b", key); apperror.CodeOf(err) != "backend.report.cursor_invalid" {
		t.Fatalf("scope changed cursor error=%v", err)
	}
	changed := append([]reportmodel.ReportResultRow(nil), rows...)
	changed[1] = reportmodel.ReportResultRow{Dimensions: map[string]string{"id": "changed"}}
	if _, _, _, _, err := paginateReportRows(changed, reportmodel.ReportPageRequest{PageSize: 2, Cursor: cursor}, "query-a", key); apperror.CodeOf(err) != "backend.report.cursor_stale" {
		t.Fatalf("source changed cursor error=%v", err)
	}
}

func TestReportPaginationRejectsUnboundedPage(t *testing.T) {
	_, _, _, _, err := paginateReportRows(nil, reportmodel.ReportPageRequest{PageSize: reportmodel.ReportPageMaximumSize + 1}, "query", []byte("key"))
	if apperror.CodeOf(err) != "backend.report.page_size_invalid" {
		t.Fatalf("oversize page error=%v", err)
	}
}

func TestReportPaginationKeepsEmptyRowsAsJSONArray(t *testing.T) {
	rows, total, cursor, truncated, err := paginateReportRows(nil, reportmodel.ReportPageRequest{}, "query", []byte("key"))
	if err != nil {
		t.Fatal(err)
	}
	if rows == nil || len(rows) != 0 || total != 0 || cursor != "" || truncated {
		t.Fatalf("empty page=%#v total=%d cursor=%q truncated=%v", rows, total, cursor, truncated)
	}
	encoded, err := json.Marshal(reportmodel.ReportSummary{Rows: rows})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(encoded, []byte(`"rows":[]`)) {
		t.Fatalf("empty rows must encode as JSON array: %s", encoded)
	}
}

func TestReportCSVPagesPreserveHeaderAndRows(t *testing.T) {
	pages, total, err := reportCSVPages([]byte("id,value\n1,a\n2,b\n3,c\n"), 2)
	if err != nil || total != 3 || len(pages) != 2 {
		t.Fatalf("pages=%d total=%d err=%v", len(pages), total, err)
	}
	if string(pages[0]) != "id,value\n1,a\n2,b\n" || string(pages[1]) != "3,c\n" {
		t.Fatalf("page contents=%q / %q", pages[0], pages[1])
	}
}

func TestReportExportThresholdUsesExactAuthorizedTotal(t *testing.T) {
	for _, test := range []struct {
		total int
		async bool
	}{{0, false}, {1000, false}, {1001, true}} {
		if actual := reportExportRequiresAsync(test.total); actual != test.async {
			t.Fatalf("total=%d async=%v want=%v", test.total, actual, test.async)
		}
	}
	if reportExportRoutesAsync(200) {
		t.Fatal("ordinary 200-row export left the synchronous production threshold path")
	}
	rows := "id\n"
	for index := 0; index < 1001; index++ {
		rows += fmt.Sprintf("%d\n", index)
	}
	pages, total, err := reportCSVPages([]byte(rows), reportmodel.ReportPageMaximumSize)
	if err != nil || total != 1001 || len(pages) != 6 {
		t.Fatalf("1001-row paging total=%d pages=%d err=%v", total, len(pages), err)
	}
}

func TestReportExportFingerprintBindsCanonicalConditionsNotRequestIdentity(t *testing.T) {
	base := reportExportBatchPayload{
		WorkspaceID: "workspace-a", RequesterUserID: "user-a", ReportKey: "orders", ObjectKey: "order",
		Scope:                  reportmodel.ReportExportScopeRequest{Parameters: map[string]any{"status": "paid"}},
		ReportDefinitionSHA256: "definition", ReportSourceSHA256: "source", AuthorizationScopeSHA256: "scope", ResultSHA256: "result", ExactTotal: 12, Format: "csv",
	}
	base.AuditID = "audit-a"
	base.ArtifactIdempotencyKey = "caller-a"
	first := reportExportRequestFingerprint(base)
	base.AuditID = "audit-b"
	base.ArtifactIdempotencyKey = "caller-b"
	if replay := reportExportRequestFingerprint(base); replay != first {
		t.Fatalf("request identity changed canonical fingerprint: %s != %s", replay, first)
	}
	for name, mutate := range map[string]func(*reportExportBatchPayload){
		"sql":   func(value *reportExportBatchPayload) { value.ReportSourceSHA256 = "source-v2" },
		"scope": func(value *reportExportBatchPayload) { value.AuthorizationScopeSHA256 = "scope-v2" },
		"parameters": func(value *reportExportBatchPayload) {
			value.Scope.Parameters = map[string]any{"status": "refunded"}
		},
		"format": func(value *reportExportBatchPayload) { value.Format = "xlsx" },
	} {
		changed := base
		mutate(&changed)
		if got := reportExportRequestFingerprint(changed); got == first {
			t.Fatalf("%s change reused fingerprint %s", name, got)
		}
	}
}

func TestSynchronousReportExportReplayRenewsOnlyExpiredOrCorruptArtifact(t *testing.T) {
	now := time.Date(2026, 8, 16, 10, 0, 0, 0, time.UTC)
	content := []byte("id\n1\n")
	store := &reportExportArtifactStoreStub{artifact: reportmodel.ReportExportArtifact{
		WorkspaceID: "workspace-a", RequesterUserID: "user-a", ReportKey: "orders", IdempotencyKey: "fingerprint",
		Content: content, ContentSHA256: reportexport.SHA256Hex(content), ExpiresAt: now.Add(time.Minute).Format(time.RFC3339Nano),
	}}
	service := &ReportApplicationService{exportArtifacts: store, clock: func() time.Time { return now }}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace-a", UserID: "user-a"}}
	if key, err := service.reportExportSynchronousReplayKey(t.Context(), "orders", "fingerprint", principal); err != nil || key != "fingerprint" {
		t.Fatalf("valid replay key=%q err=%v", key, err)
	}
	store.artifact.ExpiresAt = now.Add(-time.Second).Format(time.RFC3339Nano)
	if key, err := service.reportExportSynchronousReplayKey(t.Context(), "orders", "fingerprint", principal); err != nil || key == "fingerprint" || !strings.HasPrefix(key, "fingerprint:renew:") {
		t.Fatalf("expired replay key=%q err=%v", key, err)
	}
	store.artifact.ExpiresAt = now.Add(time.Minute).Format(time.RFC3339Nano)
	store.artifact.Content = []byte("corrupt")
	if key, err := service.reportExportSynchronousReplayKey(t.Context(), "orders", "fingerprint", principal); err != nil || key == "fingerprint" {
		t.Fatalf("corrupt replay key=%q err=%v", key, err)
	}
}
