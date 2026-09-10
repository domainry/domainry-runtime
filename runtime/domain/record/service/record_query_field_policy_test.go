package service

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"
)

type fieldQueryPolicyProbe struct{ readPolicyProbe }

func (p fieldQueryPolicyProbe) NormalizeListQuery(object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery, _ principalmodel.Principal) recordmodel.RecordListQuery {
	return recordvalidation.RecordNormalizeListQuery(object, query)
}

type fieldQueryRepositoryProbe struct {
	readRepositoryProbe
	calls int
}

func (p *fieldQueryRepositoryProbe) ListRecords(ctx context.Context, workspace string, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	p.calls++
	return p.readRepositoryProbe.ListRecords(ctx, workspace, object, query)
}

func fieldQueryFixture() (definitionmodel.ObjectSchema, principalmodel.Principal, *fieldQueryRepositoryProbe, *RecordReadDomainService) {
	object := definitionmodel.ObjectSchema{Key: "follow_up", Fields: []definitionmodel.FieldSchema{
		{Key: "message", Type: "text"}, {Key: "reason", Type: "text"},
		{Key: "phone", Type: "phone"}, {Key: "conditional", Type: "text"},
	}}
	principal := accessfixture.Attach(principalmodel.Principal{}, accessfixture.Bundle{
		DataPolicies: []accessfixture.DataPolicyFixture{{ObjectKey: object.Key, Read: true, Scope: "all"}},
		FieldPolicies: []accessfixture.FieldPolicyFixture{
			{ObjectKey: object.Key, FieldKey: "message", Read: true},
			{ObjectKey: object.Key, FieldKey: "reason", Read: false},
			{ObjectKey: object.Key, FieldKey: "phone", Read: true, Masked: true},
			{ObjectKey: object.Key, FieldKey: "conditional", Read: true, Policies: []accessfixture.FieldRuleFixture{{
				Key: "owner-only", Actions: []string{"read"}, Effect: "hide", Priority: 100,
				Predicate: &accessfixture.PredicateFixture{Operator: "eq", FieldKey: "message", ValueSource: "actor_claim", ClaimKey: "user_id"},
			}}},
		},
	})
	repository := &fieldQueryRepositoryProbe{readRepositoryProbe: readRepositoryProbe{page: recordmodel.RecordPageResult{
		Items: []recordmodel.Record{{ID: "one", Data: map[string]any{"message": "public", "reason": "private", "phone": "12345678901"}}}, Total: 1,
	}}}
	service := NewRecordReadDomainService(RecordReadDependencies{Repository: repository, Policy: fieldQueryPolicyProbe{readPolicyProbe{object: object, allow: true}}})
	return object, principal, repository, service
}

func TestReadQueryRejectsProtectedPredicatesBeforeStorage(t *testing.T) {
	queries := map[string]recordmodel.RecordListQuery{
		"hidden equality":   {Filters: map[string]any{"reason": "private"}},
		"hidden membership": {Filters: map[string]any{"reason__in": []any{"private"}}},
		"hidden range":      {Filters: map[string]any{"reason__gte": "a"}},
		"hidden sort":       {Sort: []recordmodel.RecordSortRule{{Field: "reason", Direction: "desc"}}},
		"masked equality":   {Filters: map[string]any{"phone": "12345678901"}},
		"conditional sort":  {Sort: []recordmodel.RecordSortRule{{Field: "conditional", Direction: "asc"}}},
		"nested expression": {FilterExpression: &recordmodel.RecordFilterExpression{Operator: "or", Children: []recordmodel.RecordFilterExpression{
			{Operator: "eq", Field: "message", Value: "public"}, {Operator: "and", Children: []recordmodel.RecordFilterExpression{{Operator: "eq", Field: "reason", Value: "private"}}},
		}}},
	}
	for name, query := range queries {
		t.Run(name, func(t *testing.T) {
			object, principal, repository, service := fieldQueryFixture()
			_, err := service.ListRecords(t.Context(), object.Key, query, principal)
			var appErr *apperror.AppError
			if !errors.As(err, &appErr) || appErr.Kind != apperror.KindForbidden || appErr.Code != "backend.record.field_not_queryable" || repository.calls != 0 {
				t.Fatalf("protected query reached storage: calls=%d error=%v", repository.calls, err)
			}
		})
	}
}

func TestReadQuerySearchUsesOnlyUnconditionallyClearFields(t *testing.T) {
	object, principal, repository, service := fieldQueryFixture()
	page, err := service.ListRecords(t.Context(), object.Key, recordmodel.RecordListQuery{Search: "private"}, principal)
	if err != nil || repository.calls != 1 || !reflect.DeepEqual(repository.lastQuery.SearchFields, []string{"message"}) || repository.lastQuery.Search != "private" {
		t.Fatalf("search used protected values: query=%#v error=%v", repository.lastQuery, err)
	}
	if _, exists := page.Items[0].Data["reason"]; exists {
		t.Fatal("hidden reason returned")
	}
}

func TestReadQuerySearchWithNoClearFieldsFailsClosed(t *testing.T) {
	object, principal, repository, service := fieldQueryFixture()
	_, err := service.ListRecords(t.Context(), object.Key, recordmodel.RecordListQuery{Search: "private", SearchFields: []string{"reason", "phone", "conditional"}}, principal)
	var appErr *apperror.AppError
	if !errors.As(err, &appErr) || appErr.Kind != apperror.KindForbidden || repository.calls != 0 {
		t.Fatalf("unsearchable request became a broad query: calls=%d error=%v", repository.calls, err)
	}
}

func TestReadQueryPreservesPublicFiltersMetadataAndActionAuthority(t *testing.T) {
	object, principal, repository, service := fieldQueryFixture()
	public := recordmodel.RecordListQuery{Filters: map[string]any{"message": "public", "id__in": []any{"one"}}, Sort: []recordmodel.RecordSortRule{{Field: "created_at", Direction: "desc"}}}
	if _, err := service.ListRecords(t.Context(), object.Key, public, principal); err != nil {
		t.Fatal(err)
	}
	if repository.calls != 1 || repository.lastQuery.Filters["message"] != "public" || repository.lastQuery.Sort[0].Field != "created_at" {
		t.Fatal("public query changed")
	}
	internal := recordmodel.RecordListQuery{Search: "private", Filters: map[string]any{"reason": "private"}, Sort: []recordmodel.RecordSortRule{{Field: "reason", Direction: "asc"}}}
	page, err := service.ListRecordsForAction(t.Context(), object.Key, internal, principal)
	if err != nil || repository.calls != 2 || len(repository.lastQuery.SearchFields) != 4 || page.Items[0].Data["reason"] != "private" {
		t.Fatalf("Action business authority changed: %v", err)
	}
}
