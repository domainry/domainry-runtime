package reportmodulehost

import (
	"context"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	model "github.com/domainry/domainry-report-sdk/model"
	definition "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principal "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	record "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

type sharedResultScopeAccess struct {
	objectSQLSourceAccessProbe
	queries    map[string]record.RecordListQuery
	deniedUser string
}

func (p *sharedResultScopeAccess) NormalizeReportListQuery(_ context.Context, _ definition.ObjectSchema, q record.RecordListQuery, a principal.Principal) record.RecordListQuery {
	s := p.queries[a.UserID]
	q.AuthorizationMode, q.ScopeExpression, q.OwnerOrganizationScopeID = s.AuthorizationMode, s.ScopeExpression, s.OwnerOrganizationScopeID
	q.Locale, q.AfterID = s.Locale, s.AfterID
	return q
}
func (p *sharedResultScopeAccess) AuthorizeReportObjectSQLField(_ context.Context, a principal.Principal, _ definition.ObjectSchema, _ string) error {
	if a.UserID == p.deniedUser {
		return &apperror.AppError{Kind: apperror.KindForbidden, Code: "backend.permission.denied"}
	}
	return nil
}
func TestSharedReportSourceScopeCoversOriginalRowsWithActualReaderFields(t *testing.T) {
	owner := &record.RecordScopeExpression{Operator: "eq", FieldKey: "owner_user_id", Values: []string{"producer"}}
	p := &sharedResultScopeAccess{objectSQLSourceAccessProbe: objectSQLSourceAccessProbe{objects: map[string]definition.ObjectSchema{"sale": {Key: "sale"}}}, queries: map[string]record.RecordListQuery{"producer": {AuthorizationMode: record.RecordQueryAuthorizationPredicate, ScopeExpression: owner}, "reader": {AuthorizationMode: record.RecordQueryAuthorizationUnrestricted}}}
	h := NewReportModuleQueryHost(ReportModuleQueryHostDependencies{Access: p})
	r := model.ReportSchema{ObjectSQLV1: &model.ReportObjectSQLSchema{SQL: `SELECT s.id AS id FROM sale s LIMIT 10`, ResultSchema: []model.ReportResultColumnSchema{{Key: "id", Type: "text", Kind: "dimension"}}}}
	var source, reader model.ReportSubject
	source.Principal.Known, reader.Principal.Known = true, true
	source.Principal.WorkspaceID, reader.Principal.WorkspaceID = "workspace", "workspace"
	source.Principal.UserID, reader.Principal.UserID = "producer", "reader"
	if err := h.AuthorizeSharedReportResultScope(t.Context(), r, source, reader); err != nil {
		t.Fatal("broader reader could not read producer's rows", err)
	}
	p.queries["reader"] = record.RecordListQuery{AuthorizationMode: record.RecordQueryAuthorizationPredicate, ScopeExpression: &record.RecordScopeExpression{Operator: "eq", FieldKey: "owner_user_id", Values: []string{"reader"}}}
	if err := h.AuthorizeSharedReportResultScope(t.Context(), r, source, reader); err == nil {
		t.Fatal("different owner scope accepted original rows")
	}
	p.queries["reader"] = record.RecordListQuery{AuthorizationMode: record.RecordQueryAuthorizationUnrestricted}
	p.deniedUser = "reader"
	if err := h.AuthorizeSharedReportResultScope(t.Context(), r, source, reader); err == nil {
		t.Fatal("reader field denial ignored")
	}
	p.deniedUser = ""
	p.queries["reader"] = record.RecordListQuery{AuthorizationMode: record.RecordQueryAuthorizationUnrestricted, AfterID: "private-hidden-cursor"}
	if err := h.AuthorizeSharedReportResultScope(t.Context(), r, source, reader); err == nil {
		t.Fatal("query field excluded from JSON escaped comparison")
	}
	reader.Principal.WorkspaceID = "other"
	if err := h.AuthorizeSharedReportResultScope(t.Context(), r, source, reader); err == nil {
		t.Fatal("foreign workspace accepted")
	}
}
func TestSharedRecordScopeContainmentHandlesSetsAndDependenciesConservatively(t *testing.T) {
	atom := func(values ...string) *record.RecordScopeExpression {
		return &record.RecordScopeExpression{Operator: "in", FieldKey: "id", Values: values}
	}
	and := func(a, b *record.RecordScopeExpression) *record.RecordScopeExpression {
		return &record.RecordScopeExpression{Operator: "and", Children: []record.RecordScopeExpression{*a, *b}}
	}
	or := func(a, b *record.RecordScopeExpression) *record.RecordScopeExpression {
		return &record.RecordScopeExpression{Operator: "or", Children: []record.RecordScopeExpression{*a, *b}}
	}
	for _, tc := range []struct {
		name           string
		source, reader *record.RecordScopeExpression
		allow          bool
	}{
		{"set-superset", atom("a", "b"), atom("b", "a", "c"), true},
		{"set-narrower", atom("a", "b"), atom("a"), false},
		{"empty-original", atom(), atom("a"), true},
		{"intersection-original", and(atom("a"), atom("b")), atom("a"), true},
		{"intersection-reader", atom("a"), and(atom("a"), atom("b")), false},
		{"union-original", or(atom("a"), atom("b")), atom("a", "b"), true},
		{"union-narrower", or(atom("a"), atom("b")), atom("a"), false},
		{"union-reader", atom("a"), or(atom("b"), atom("a")), true},
		{"unknown-extension", &record.RecordScopeExpression{Operator: "unknown"}, &record.RecordScopeExpression{Operator: "unknown"}, false},
		{"context-dependent-relation", &record.RecordScopeExpression{Operator: "in", FieldKey: "id", RelationExists: true}, &record.RecordScopeExpression{Operator: "in", FieldKey: "id", RelationExists: true}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if actual := sharedRecordScopeCovered(tc.source, tc.reader, 0); actual != tc.allow {
				t.Fatal(actual, "want", tc.allow)
			}
		})
	}
	deep := atom("a")
	for i := 0; i < 40; i++ {
		deep = and(deep, atom("a"))
	}
	if sharedRecordScopeCovered(atom("a"), deep, 0) {
		t.Fatal("unbounded reader expression accepted")
	}
}
