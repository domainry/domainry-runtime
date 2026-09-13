package reportmodulehost

import (
	"context"
	"errors"
	"testing"

	model "github.com/domainry/domainry-report-sdk/model"
	definition "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principal "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	record "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

type resultReadScopeAccess struct {
	objectSQLSourceAccessProbe
	mode     record.RecordQueryAuthorizationMode
	scope    *record.RecordScopeExpression
	ownerOrg string
	deny     bool
}

func (p *resultReadScopeAccess) NormalizeReportListQuery(_ context.Context, _ definition.ObjectSchema, q record.RecordListQuery, _ principal.Principal) record.RecordListQuery {
	q.AuthorizationMode = p.mode
	q.ScopeExpression = p.scope
	q.OwnerOrganizationScopeID = p.ownerOrg
	return q
}
func (p *resultReadScopeAccess) AuthorizeReportObjectSQLField(context.Context, principal.Principal, definition.ObjectSchema, string) error {
	if p.deny {
		return errors.New("field denied")
	}
	return nil
}

func TestReportResultReadScopeIncludesHiddenPredicatesButNotExecutionRevision(t *testing.T) {
	p := &resultReadScopeAccess{objectSQLSourceAccessProbe: objectSQLSourceAccessProbe{objects: map[string]definition.ObjectSchema{"sale": {Key: "sale"}}}, mode: record.RecordQueryAuthorizationUnrestricted}
	host := NewReportModuleQueryHost(ReportModuleQueryHostDependencies{Access: p})
	r := model.ReportSchema{ObjectSQLV1: &model.ReportObjectSQLSchema{SQL: `SELECT s.id AS id FROM sale s LIMIT 10`, ResultSchema: []model.ReportResultColumnSchema{{Key: "id", Type: "text", Kind: "dimension"}}}}
	var subject model.ReportSubject
	subject.Principal.WorkspaceID = "workspace"
	subject.Principal.UserID = "user"
	subject.AccessScopeHash = "revision-one"
	initial, err := host.ReadReportResultScope(t.Context(), r, subject)
	if err != nil {
		t.Fatal(err)
	}
	subject.AccessScopeHash = "revision-two"
	subject.Principal.AuthorizationRevision = "new"
	if value, err := host.ReadReportResultScope(t.Context(), r, subject); err != nil || value != initial {
		t.Fatal("execution revision affected same projection", value, err)
	}
	p.scope = &record.RecordScopeExpression{Operator: "eq", FieldKey: "owner_user_id", Values: []string{"user"}}
	p.mode = record.RecordQueryAuthorizationPredicate
	limited, err := host.ReadReportResultScope(t.Context(), r, subject)
	if err != nil || limited == initial {
		t.Fatal("hidden scope excluded", err)
	}
	p.scope.RelationExists = true
	if value, err := host.ReadReportResultScope(t.Context(), r, subject); err != nil || value == limited {
		t.Fatal("hidden relation scope excluded", err)
	}
	p.ownerOrg = "organization"
	organization, err := host.ReadReportResultScope(t.Context(), r, subject)
	if err != nil || organization == limited {
		t.Fatal("organization scope excluded", err)
	}
	p.deny = true
	if _, err := host.ReadReportResultScope(t.Context(), r, subject); err == nil {
		t.Fatal("field denial ignored")
	}
}
