package query

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	identitysdk "github.com/domainry/domainry-identity-sdk"
	reportmodel "github.com/domainry/domainry-report-sdk/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	reportcontract "github.com/domainry/domainry-runtime/runtime/domain/report/contract"
	reportservice "github.com/domainry/domainry-runtime/runtime/domain/report/query"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
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
	service := NewReportQueryApplicationService(ReportQueryApplicationDependencies{Domain: domain, CursorKey: []byte("bounded-page-key")})
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
	service := NewReportQueryApplicationService(ReportQueryApplicationDependencies{Domain: domain, CursorKey: []byte("bounded-page-key")})
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
	service := NewReportQueryApplicationService(ReportQueryApplicationDependencies{Domain: domain, CursorKey: []byte("bounded-page-key")})
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

type reportExportDatasetAccessStub struct {
	objects map[string]definitionmodel.ObjectSchema
}

func (s reportExportDatasetAccessStub) ReportObjectForAction(_ context.Context, _ principalmodel.Principal, key, _ string) (definitionmodel.ObjectSchema, error) {
	return s.objects[key], nil
}
func (reportExportDatasetAccessStub) NormalizeReportListQuery(_ context.Context, _ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery, _ principalmodel.Principal) recordmodel.RecordListQuery {
	return query
}
func (reportExportDatasetAccessStub) CanAccessReportRecord(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool {
	return true
}
func (reportExportDatasetAccessStub) AuthorizeReportObjectSQLField(context.Context, principalmodel.Principal, definitionmodel.ObjectSchema, string) error {
	return nil
}

type reportExportObjectSQLExecutorStub struct {
	requests []reportcontract.ReportObjectSQLExecutionRequest
	rows     []map[string]string
}

func (s *reportExportObjectSQLExecutorStub) ExecuteReportObjectSQL(_ context.Context, request reportcontract.ReportObjectSQLExecutionRequest) (reportcontract.ReportObjectSQLExecutionResult, error) {
	s.requests = append(s.requests, request)
	start := request.PagePosition
	if start > len(s.rows) {
		start = len(s.rows)
	}
	end := start + request.PageSize
	more := end < len(s.rows)
	if end > len(s.rows) {
		end = len(s.rows)
	}
	next := ""
	if more {
		next = fmt.Sprintf("cursor:%d", end)
	}
	return reportcontract.ReportObjectSQLExecutionResult{Rows: append([]map[string]string(nil), s.rows[start:end]...), HasMore: more, Total: len(s.rows), TotalKnown: true, NextCursor: next}, nil
}

type reportSnapshotSourceStub struct{}

func (reportSnapshotSourceStub) ReadReportSnapshotSourceVersion(context.Context, reportcontract.ReportSnapshotSourceVersionRequest) (reportmodel.ReportSnapshotSourceVersion, error) {
	return reportmodel.ReportSnapshotSourceVersion{Watermark: "stable", SourceVersions: map[string]string{"ledger": "v1"}}, nil
}
func reportPrincipal() principalmodel.Principal {
	bundle := accessfixture.Bundle{Key: "report-query", RecordScope: "all_records", Permissions: []string{"ledger.read"}, FieldPolicies: []accessfixture.FieldPolicyFixture{{ObjectKey: "ledger", FieldKey: "id", Read: true}}}
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, UserID: "operator-1", WorkspaceID: "workspace-a"}}, bundle)
}
