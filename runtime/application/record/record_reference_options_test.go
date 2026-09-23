package record

import (
	"context"
	"reflect"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	accessfixture "github.com/domainry/domainry-runtime/testsupport/identitysdkfixture"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	recordservice "github.com/domainry/domainry-runtime/runtime/domain/record/service"
)

type referenceOptionRepository struct {
	recordrepository.RecordRepository
	query recordmodel.RecordListQuery
}

func (r *referenceOptionRepository) ListRecords(_ context.Context, workspaceID string, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	r.query = query
	if workspaceID != "workspace-a" || object.Key != "customer" {
		return recordmodel.RecordPageResult{}, nil
	}
	return recordmodel.RecordPageResult{
		Items:    []recordmodel.Record{{ID: "customer-1", Data: map[string]any{"name": "Acme"}}},
		Page:     query.Page,
		PageSize: query.PageSize,
		Total:    2,
		HasNext:  true,
	}, nil
}

func referenceOptionService(repository recordrepository.RecordRepository) *RecordApplicationService {
	objects := referenceOptionObjects()
	queryPolicy := recordservice.NewRecordQueryPolicyDomainService(recordservice.RecordQueryPolicyDependencies{
		Objects: func() []definitionmodel.ObjectSchema {
			return []definitionmodel.ObjectSchema{objects["order"], objects["customer"]}
		},
	})
	return NewRecordApplicationService(RecordApplicationDependencies{
		Repository:  repository,
		QueryPolicy: queryPolicy,
		SchemaMap:   func() map[string]definitionmodel.ObjectSchema { return objects },
	})
}

func referenceOptionObjects() map[string]definitionmodel.ObjectSchema {
	return map[string]definitionmodel.ObjectSchema{
		"order": {
			Key: "order",
			Fields: []definitionmodel.FieldSchema{
				{Key: "customer_id", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "customer"}},
				{Key: "status", Type: "select"},
			},
		},
		"customer": {
			Key: "customer",
			Fields: []definitionmodel.FieldSchema{
				{Key: "name", Type: "text"},
				{Key: "code", Type: "text"},
			},
		},
	}
}

func referenceOptionPrincipal(withPolicy bool) principalmodel.Principal {
	bundle := accessfixture.Bundle{Permissions: []string{"customer.read"}, DataPolicies: accessfixture.DataPoliciesForPermissions([]string{"customer.read"}, "all")}
	if withPolicy {
		bundle.ReferencePolicies = []accessfixture.ReferencePolicyFixture{{
			SourceObjectKey: "order", RelationFieldKey: "customer_id", TargetObjectKey: "customer", DisplayFields: []string{"name"},
		}}
	}
	return accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a", UserID: "operator"}}, bundle)
}

func TestReferenceOptionsReturnsStableAuthorizedIDNameProjection(t *testing.T) {
	repository := &referenceOptionRepository{}
	page, err := referenceOptionService(repository).ReferenceOptions(t.Context(), "order", "customer_id", RecordReferenceOptionRequest{
		Query: "  Ac%_~  ", Page: 2, Limit: 500, Locale: "zh-CN", FallbackLocale: "en-US",
	}, referenceOptionPrincipal(true))
	if err != nil {
		t.Fatalf("%#v query=%#v", err, repository.query)
	}
	if !reflect.DeepEqual(page.Items, []RecordReferenceOption{{ID: "customer-1", Name: "Acme"}}) || !page.HasMore {
		t.Fatalf("page=%#v", page)
	}
	if repository.query.Page != 2 || repository.query.PageSize != recordReferenceMaximumLimit || repository.query.Search != "Ac%_~" {
		t.Fatalf("pagination/search query=%#v", repository.query)
	}
	if !reflect.DeepEqual(repository.query.SearchFields, []string{"name"}) || !reflect.DeepEqual(repository.query.SelectFields, []string{"name"}) {
		t.Fatalf("fields query=%#v", repository.query)
	}
	if len(repository.query.Sort) != 2 || repository.query.Sort[0].Field != "name" || repository.query.Sort[1].Field != "id" {
		t.Fatalf("sort query=%#v", repository.query.Sort)
	}
}

func TestReferenceOptionsFailsClosedWithoutExactReferencePolicy(t *testing.T) {
	service := referenceOptionService(&referenceOptionRepository{})
	if _, err := service.ReferenceOptions(t.Context(), "order", "customer_id", RecordReferenceOptionRequest{}, referenceOptionPrincipal(false)); apperror.CodeOf(err) != "backend.reference.permission_denied" {
		t.Fatalf("missing policy err=%v", err)
	}
	principal := referenceOptionPrincipal(true)
	accessfixture.Set(&principal, accessfixture.Bundle{
		Permissions: []string{"customer.read"}, DataPolicies: accessfixture.DataPoliciesForPermissions([]string{"customer.read"}, "all"),
		ReferencePolicies: []accessfixture.ReferencePolicyFixture{{SourceObjectKey: "order", RelationFieldKey: "customer_id", TargetObjectKey: "supplier", DisplayFields: []string{"name"}}},
	})
	if _, err := service.ReferenceOptions(t.Context(), "order", "customer_id", RecordReferenceOptionRequest{}, principal); apperror.CodeOf(err) != "backend.reference.permission_denied" {
		t.Fatalf("wrong target policy err=%v", err)
	}
}

func TestReferenceOptionsRejectsInvalidSourceFieldAndTarget(t *testing.T) {
	service := referenceOptionService(&referenceOptionRepository{})
	principal := referenceOptionPrincipal(true)
	tests := []struct {
		object string
		field  string
		code   string
	}{
		{object: "missing", field: "customer_id", code: "backend.object.not_found"},
		{object: "order", field: "status", code: "backend.reference.relation_field_invalid"},
		{object: "order", field: "missing", code: "backend.reference.relation_field_invalid"},
	}
	for _, test := range tests {
		if _, err := service.ReferenceOptions(t.Context(), test.object, test.field, RecordReferenceOptionRequest{}, principal); apperror.CodeOf(err) != test.code {
			t.Fatalf("%s.%s err=%v code=%s", test.object, test.field, err, apperror.CodeOf(err))
		}
	}
}

func TestReferenceOptionsCanSearchByTechnicalIDWhenItIsTheDisplayFallback(t *testing.T) {
	objects := referenceOptionObjects()
	target := objects["customer"]
	target.Fields = nil
	objects["customer"] = target
	repository := &referenceOptionRepository{}
	queryPolicy := recordservice.NewRecordQueryPolicyDomainService(recordservice.RecordQueryPolicyDependencies{
		Objects: func() []definitionmodel.ObjectSchema {
			return []definitionmodel.ObjectSchema{objects["order"], objects["customer"]}
		},
	})
	service := NewRecordApplicationService(RecordApplicationDependencies{Repository: repository, QueryPolicy: queryPolicy, SchemaMap: func() map[string]definitionmodel.ObjectSchema { return objects }})
	principal := accessfixture.Attach(principalmodel.Principal{Principal: identitysdk.Principal{Known: true, WorkspaceID: "workspace-a"}}, accessfixture.Bundle{
		Permissions: []string{"customer.read"}, DataPolicies: accessfixture.DataPoliciesForPermissions([]string{"customer.read"}, "all"),
		ReferencePolicies: []accessfixture.ReferencePolicyFixture{{SourceObjectKey: "order", RelationFieldKey: "customer_id", TargetObjectKey: "customer", DisplayFields: []string{"id"}}},
	})
	page, err := service.ReferenceOptions(t.Context(), "order", "customer_id", RecordReferenceOptionRequest{Query: "customer-1"}, principal)
	if err != nil || len(page.Items) != 1 || page.Items[0].Name != "customer-1" {
		t.Fatalf("page=%#v err=%v", page, err)
	}
	if !reflect.DeepEqual(repository.query.SearchFields, []string{"id"}) || len(repository.query.SelectFields) != 0 {
		t.Fatalf("id fallback query=%#v", repository.query)
	}
}
