// Delete-relation domain service tests.
package service

import (
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"

	"context"
	"errors"
	"reflect"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type deleteRelationRepositoryProbe struct {
	recordrepository.RecordRepository
	pages   map[string][]recordmodel.RecordPageResult
	err     error
	queries []recordmodel.RecordListQuery
}

func (r *deleteRelationRepositoryProbe) ListRecords(_ context.Context, _ string, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	r.queries = append(r.queries, query)
	if r.err != nil {
		return recordmodel.RecordPageResult{}, r.err
	}
	pages := r.pages[object.Key]
	if query.Page <= 0 || query.Page > len(pages) {
		return recordmodel.RecordPageResult{}, nil
	}
	return pages[query.Page-1], nil
}

func TestDeleteRelationServiceScansReferencesInStableObjectOrder(t *testing.T) {
	objects := map[string]definitionmodel.ObjectSchema{
		"z_order": {Key: "z_order", Fields: []definitionmodel.FieldSchema{{Key: "customer", Type: "relation", Config: map[string]any{"object_key": "customer", "on_delete": "set_null"}}}},
		"a_quote": {Key: "a_quote", Fields: []definitionmodel.FieldSchema{{Key: "customer", Type: "relation", Config: map[string]any{"target": "customer", "on_delete": "cascade"}}}},
	}
	repository := &deleteRelationRepositoryProbe{pages: map[string][]recordmodel.RecordPageResult{
		"a_quote": {{Items: []recordmodel.Record{{ID: "quote-1"}}, HasNext: true}, {Items: []recordmodel.Record{{ID: "quote-2"}}}},
		"z_order": {{Items: []recordmodel.Record{{ID: "order-1"}}}},
	}}
	service := NewRecordDeleteRelationDomainService(repository, func() map[string]definitionmodel.ObjectSchema { return objects })

	references, err := service.References(t.Context(), "default", "customer", "customer-1")
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, reference := range references {
		got = append(got, reference.Object.Key+":"+reference.Record.ID)
	}
	if !reflect.DeepEqual(got, []string{"a_quote:quote-1", "a_quote:quote-2", "z_order:order-1"}) {
		t.Fatalf("references = %#v", got)
	}
	for _, query := range repository.queries {
		if query.PageSize != 200 || query.Filters["customer"] != "customer-1" {
			t.Fatalf("reference query = %#v", query)
		}
	}
}

func TestDeleteRelationServiceAppliesSetNullAndCascade(t *testing.T) {
	objects := map[string]definitionmodel.ObjectSchema{
		"order": {Key: "order", Fields: []definitionmodel.FieldSchema{
			{Key: "billing_customer", Type: "relation", Config: map[string]any{"target": "customer", "on_delete": "set_null"}},
			{Key: "shipping_customer", Type: "relation", Config: map[string]any{"target": "customer", "on_delete": "cascade"}},
		}},
	}
	repository := &deleteRelationRepositoryProbe{pages: map[string][]recordmodel.RecordPageResult{
		"order": {
			{Items: []recordmodel.Record{{ID: "order-1"}}},
		},
	}}
	// Both fields query the same fixture page, so each callback receives one reference.
	service := NewRecordDeleteRelationDomainService(repository, func() map[string]definitionmodel.ObjectSchema { return objects })
	setNull, cascade := []string{}, []string{}
	err := service.Apply(t.Context(), "default", "customer", "customer-1", RecordDeleteRelationCallbacks{
		SetNull: func(_ context.Context, reference RecordDeleteReference) error {
			setNull = append(setNull, reference.Field.Key+":"+reference.Record.ID)
			return nil
		},
		Cascade: func(_ context.Context, reference RecordDeleteReference) error {
			cascade = append(cascade, reference.Field.Key+":"+reference.Record.ID)
			return nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(setNull, []string{"billing_customer:order-1"}) || !reflect.DeepEqual(cascade, []string{"shipping_customer:order-1"}) {
		t.Fatalf("callbacks set_null=%#v cascade=%#v", setNull, cascade)
	}
}

func TestDeleteRelationServiceRejectsRestrictBeforeSideEffects(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "order", Fields: []definitionmodel.FieldSchema{
		{Key: "customer", Type: "relation", Config: map[string]any{"target": "customer"}},
		{Key: "secondary_customer", Type: "relation", Config: map[string]any{"target": "customer", "on_delete": "set_null"}},
	}}
	repository := &deleteRelationRepositoryProbe{pages: map[string][]recordmodel.RecordPageResult{"order": {{Items: []recordmodel.Record{{ID: "order-1"}}}}}}
	service := NewRecordDeleteRelationDomainService(repository, func() map[string]definitionmodel.ObjectSchema {
		return map[string]definitionmodel.ObjectSchema{"order": object}
	})
	called := false
	err := service.Apply(t.Context(), "default", "customer", "customer-1", RecordDeleteRelationCallbacks{SetNull: func(context.Context, RecordDeleteReference) error {
		called = true
		return nil
	}})
	assertRecordAppError(t, err, apperror.KindConflict, "backend.relation.delete_restricted", map[string]string{
		"object": "customer", "record": "customer-1", "source_object": "order", "field": "customer",
	})
	if called {
		t.Fatal("side effect ran before restrict decision")
	}
}

func TestDeleteRelationServiceWrapsRepositoryErrorsAndUsesConstructorRepository(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "customer", Type: "relation", Config: map[string]any{"target": "customer"}}}}
	service := NewRecordDeleteRelationDomainService(&deleteRelationRepositoryProbe{err: errors.New("store unavailable")}, func() map[string]definitionmodel.ObjectSchema {
		return map[string]definitionmodel.ObjectSchema{"order": object}
	})
	_, err := service.References(t.Context(), "default", "customer", "customer-1")
	assertRecordAppError(t, err, apperror.KindInternal, "backend.internal", map[string]string{"operation": "list relation delete references"})

	service = NewRecordDeleteRelationDomainService(&deleteRelationRepositoryProbe{pages: map[string][]recordmodel.RecordPageResult{"order": {{}}}}, func() map[string]definitionmodel.ObjectSchema {
		return map[string]definitionmodel.ObjectSchema{"order": object}
	})
	if _, err := service.References(t.Context(), "default", "customer", "customer-1"); err != nil {
		t.Fatalf("constructor repository was not used: %v", err)
	}
}
