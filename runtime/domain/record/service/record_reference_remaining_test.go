package service

import (
	"context"
	"errors"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
)

type referenceFailureRepository struct {
	recordrepository.RecordRepository
	record  recordmodel.Record
	found   bool
	getErr  error
	pages   []recordmodel.RecordPageResult
	listErr error
	calls   int
}

func (r *referenceFailureRepository) GetRecord(context.Context, string, definitionmodel.ObjectSchema, string) (recordmodel.Record, bool, error) {
	return r.record, r.found, r.getErr
}

func (r *referenceFailureRepository) ListRecords(context.Context, string, definitionmodel.ObjectSchema, recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	r.calls++
	if r.listErr != nil {
		return recordmodel.RecordPageResult{}, r.listErr
	}
	if r.calls <= len(r.pages) {
		return r.pages[r.calls-1], nil
	}
	return recordmodel.RecordPageResult{}, nil
}

func referenceFailureService(repository *referenceFailureRepository, objects map[string]definitionmodel.ObjectSchema, allow *bool) *RecordReferenceDomainService {
	canAccess := func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool {
		return allow == nil || *allow
	}
	return NewRecordReferenceDomainService(RecordReferenceDependencies{
		Repository: repository,
		Objects:    func() map[string]definitionmodel.ObjectSchema { return objects },
		ObjectForAction: func(_ principalmodel.Principal, key, _ string) (definitionmodel.ObjectSchema, error) {
			object, ok := objects[key]
			if !ok {
				return definitionmodel.ObjectSchema{}, errors.New("object denied")
			}
			return object, nil
		},
		CanAccess:   canAccess,
		ListRecords: scopedReferenceList(repository, canAccess),
	})
}

func TestReferenceServiceFailureAndEmptyAggregationMatrix(t *testing.T) {
	customer := definitionmodel.ObjectSchema{Key: "customer"}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace"}}
	for _, test := range []struct {
		name       string
		repository *referenceFailureRepository
		objects    map[string]definitionmodel.ObjectSchema
		allow      bool
	}{
		{name: "object", repository: &referenceFailureRepository{}, objects: map[string]definitionmodel.ObjectSchema{}, allow: true},
		{name: "get", repository: &referenceFailureRepository{getErr: errors.New("store")}, objects: map[string]definitionmodel.ObjectSchema{"customer": customer}, allow: true},
		{name: "missing", repository: &referenceFailureRepository{}, objects: map[string]definitionmodel.ObjectSchema{"customer": customer}, allow: true},
		{name: "scope", repository: &referenceFailureRepository{found: true}, objects: map[string]definitionmodel.ObjectSchema{"customer": customer}},
	} {
		t.Run(test.name, func(t *testing.T) {
			service := referenceFailureService(test.repository, test.objects, &test.allow)
			if _, err := service.References(t.Context(), "customer", "customer-1", principal); err == nil {
				t.Fatal("reference failure was ignored")
			}
		})
	}
	repository := &referenceFailureRepository{found: true, record: recordmodel.Record{ID: "customer-1"}}
	service := NewRecordReferenceDomainService(RecordReferenceDependencies{
		Repository: repository,
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return customer, nil
		},
		ListRecords: scopedReferenceList(repository, nil),
	})
	if summary, err := service.References(t.Context(), "customer", "customer-1", principal); err != nil || summary.Total != 0 {
		t.Fatalf("empty summary=%#v err=%v", summary, err)
	}
}

func TestReferenceServiceCountsSkipsAndPropagatesListFailure(t *testing.T) {
	customer := definitionmodel.ObjectSchema{Key: "customer"}
	invoice := definitionmodel.ObjectSchema{Key: "invoice", Fields: []definitionmodel.FieldSchema{
		{Key: "name", Type: "text"},
		{Key: "other", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "other"}},
		{Key: "customer_id", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "customer"}},
	}}
	objects := map[string]definitionmodel.ObjectSchema{"customer": customer, "invoice": invoice}
	allowed := true
	failing := &referenceFailureRepository{found: true, listErr: errors.New("store")}
	if _, err := referenceFailureService(failing, objects, &allowed).References(t.Context(), "customer", "customer-1", principalmodel.Principal{}); err == nil {
		t.Fatal("count failure was ignored")
	}
	repository := &referenceFailureRepository{found: true, pages: []recordmodel.RecordPageResult{{Items: []recordmodel.Record{{Data: map[string]any{"customer_id": "other"}}}}}}
	summary, err := referenceFailureService(repository, objects, &allowed).References(t.Context(), "customer", "customer-1", principalmodel.Principal{})
	if err != nil || summary.Total != 0 {
		t.Fatalf("zero summary=%#v err=%v", summary, err)
	}
}

func TestRelatedServiceFailureDependenciesAndHelpers(t *testing.T) {
	customer := definitionmodel.ObjectSchema{Key: "customer"}
	order := definitionmodel.ObjectSchema{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "customer_id", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "customer"}}}}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace"}}
	objects := map[string]definitionmodel.ObjectSchema{"customer": customer, "order": order}
	allowed := true
	for _, repository := range []*referenceFailureRepository{
		{getErr: errors.New("store")},
		{},
	} {
		service := referenceFailureService(repository, objects, &allowed)
		if _, err := service.Related(t.Context(), "customer", "customer-1", "order", RecordRelatedRecordsRequest{}, principal); err == nil {
			t.Fatal("related parent failure was ignored")
		}
	}
	repository := &referenceFailureRepository{found: true}
	denied := false
	if _, err := referenceFailureService(repository, objects, &denied).Related(t.Context(), "customer", "customer-1", "order", RecordRelatedRecordsRequest{}, principal); err == nil {
		t.Fatal("related scope failure was ignored")
	}
	service := referenceFailureService(repository, objects, &allowed)
	if _, err := service.Related(t.Context(), "customer", "customer-1", "missing", RecordRelatedRecordsRequest{}, principal); err == nil {
		t.Fatal("related object failure was ignored")
	}
	if _, err := service.Related(t.Context(), "customer", "customer-1", "order", RecordRelatedRecordsRequest{FieldKey: "missing"}, principal); err == nil {
		t.Fatal("invalid relation field was ignored")
	}
	serviceWithoutList := NewRecordReferenceDomainService(RecordReferenceDependencies{Repository: repository, Objects: func() map[string]definitionmodel.ObjectSchema { return objects }, ObjectForAction: func(_ principalmodel.Principal, key, _ string) (definitionmodel.ObjectSchema, error) {
		return objects[key], nil
	}})
	if _, err := serviceWithoutList.Related(t.Context(), "customer", "customer-1", "order", RecordRelatedRecordsRequest{}, principal); err == nil {
		t.Fatal("nil list dependency was ignored")
	}
	if _, err := NewRecordReferenceDomainService(RecordReferenceDependencies{}).objectForAction(principal, "customer", "read"); err == nil {
		t.Fatal("nil object policy was ignored")
	}
	if objects := NewRecordReferenceDomainService(RecordReferenceDependencies{}).objects(); objects != nil {
		t.Fatalf("nil objects dependency returned %#v", objects)
	}
	if _, err := NewRecordReferenceDomainService(RecordReferenceDependencies{}).count(t.Context(), principal, order, "customer_id", "customer-1"); err == nil {
		t.Fatal("nil count list dependency was ignored")
	}
	if _, err := RecordRelatedRelationField("customer", definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}, ""); err == nil {
		t.Fatal("missing relation was accepted")
	}
	if err := recordBadRequest("code", "dangling", "", "value"); err == nil {
		t.Fatal("bad request helper returned nil")
	}
}
