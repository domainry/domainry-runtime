// Reference domain service tests.
package service

import (
	identitysdk "github.com/domainry/domainry-identity-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"context"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

type referenceRepositoryProbe struct {
	recordrepository.RecordRepository
	records map[string]recordmodel.Record
	pages   map[string]map[int]recordmodel.RecordPageResult
}

func (r *referenceRepositoryProbe) GetRecord(_ context.Context, _ string, object definitionmodel.ObjectSchema, recordID string) (recordmodel.Record, bool, error) {
	record, found := r.records[object.Key+":"+recordID]
	return record, found, nil
}

func (r *referenceRepositoryProbe) ListRecords(_ context.Context, _ string, object definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	return r.pages[object.Key][query.Page], nil
}

func scopedReferenceList(repository recordrepository.RecordRepository, allow func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool) func(context.Context, string, recordmodel.RecordListQuery, principalmodel.Principal) (recordmodel.RecordPageResult, error) {
	return func(ctx context.Context, objectKey string, query recordmodel.RecordListQuery, principal principalmodel.Principal) (recordmodel.RecordPageResult, error) {
		object := definitionmodel.ObjectSchema{Key: objectKey}
		if raw, ok := query.Filters["id__in"].([]any); ok && len(raw) == 1 {
			record, found, err := repository.GetRecord(ctx, principal.WorkspaceID, object, raw[0].(string))
			if err != nil || !found || (allow != nil && !allow(principal, object, record)) {
				return recordmodel.RecordPageResult{}, err
			}
			return recordmodel.RecordPageResult{Items: []recordmodel.Record{record}, Total: 1, Page: 1, PageSize: 1}, nil
		}
		page, err := repository.ListRecords(ctx, principal.WorkspaceID, object, query)
		if err != nil {
			return recordmodel.RecordPageResult{}, err
		}
		for field, expected := range query.Filters {
			filtered := make([]recordmodel.Record, 0, len(page.Items))
			for _, candidate := range page.Items {
				if candidate.Data[field] == expected {
					filtered = append(filtered, candidate)
				}
			}
			page.Items, page.Total = filtered, len(filtered)
		}
		return page, nil
	}
}

func TestRelatedRelationFieldRequiresDisambiguation(t *testing.T) {
	related := definitionmodel.ObjectSchema{Key: "line_item", Fields: []definitionmodel.FieldSchema{
		{Key: "primary_order", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "order"}},
		{Key: "replacement_order", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "order"}},
	}}
	if _, err := RecordRelatedRelationField("order", related, ""); err == nil {
		t.Fatal("expected ambiguous relation to require a field")
	}
	field, err := RecordRelatedRelationField("order", related, "replacement_order")
	if err != nil || field != "replacement_order" {
		t.Fatalf("field=%q err=%v", field, err)
	}
}

func TestDuplicateIdentityFieldsRequiresDeclaredUniqueness(t *testing.T) {
	object := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{Key: "name"}, {Key: "email"}, {Key: "external_id", Unique: true}}}
	fields := recordvalidation.RecordDuplicateIdentityFields(object)
	if len(fields) != 1 || fields[0].Key != "external_id" {
		t.Fatalf("fields=%#v", fields)
	}
}

func TestReferenceServiceOwnsReverseRelationAggregation(t *testing.T) {
	customer := definitionmodel.ObjectSchema{Key: "customer", Name: "Customer"}
	invoice := definitionmodel.ObjectSchema{Key: "invoice", Name: "Invoice", Fields: []definitionmodel.FieldSchema{{Key: "customer_id", Name: "Customer", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "customer"}}}}
	order := definitionmodel.ObjectSchema{Key: "order", Name: "Order", Fields: []definitionmodel.FieldSchema{{Key: "customer_id", Name: "Customer", Type: "relation", Config: map[string]any{"object_key": "customer"}}}}
	objects := map[string]definitionmodel.ObjectSchema{"order": order, "customer": customer, "invoice": invoice}
	repository := &referenceRepositoryProbe{
		records: map[string]recordmodel.Record{"customer:c1": {ID: "c1"}},
		pages: map[string]map[int]recordmodel.RecordPageResult{
			"invoice": {1: {Items: []recordmodel.Record{{ID: "i1", Data: map[string]any{"customer_id": "c1"}}}}},
			"order": {
				1: {Items: []recordmodel.Record{{ID: "o1", Data: map[string]any{"customer_id": "c1"}}}, HasNext: true},
				2: {Items: []recordmodel.Record{{ID: "o2", Data: map[string]any{"customer_id": "other"}}, {ID: "o3", Data: map[string]any{"customer_id": "c1"}}}},
			},
		},
	}
	service := NewRecordReferenceDomainService(RecordReferenceDependencies{
		Repository: repository,
		Objects:    func() map[string]definitionmodel.ObjectSchema { return objects },
		ObjectForAction: func(_ principalmodel.Principal, objectKey, action string) (definitionmodel.ObjectSchema, error) {
			if action != "read" {
				t.Fatalf("action = %q", action)
			}
			return objects[objectKey], nil
		},
		CanAccess:   func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
		ListRecords: scopedReferenceList(repository, func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true }),
	})

	summary, err := service.References(t.Context(), "customer", " c1 ", principalmodel.Principal{Principal: identitysdk.Principal{Known: true}})
	if err != nil {
		t.Fatal(err)
	}
	if summary.ObjectKey != "customer" || summary.RecordID != "c1" || summary.Total != 3 || len(summary.Groups) != 2 {
		t.Fatalf("summary = %#v", summary)
	}
	if summary.Groups[0].ObjectKey != "invoice" || summary.Groups[0].Count != 1 || summary.Groups[1].ObjectKey != "order" || summary.Groups[1].Count != 2 {
		t.Fatalf("groups = %#v", summary.Groups)
	}
}

func TestReferenceServiceOwnsRelatedRecordQuery(t *testing.T) {
	parent := definitionmodel.ObjectSchema{Key: "order"}
	related := definitionmodel.ObjectSchema{Key: "line_item", Fields: []definitionmodel.FieldSchema{{Key: "order_id", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "order"}}}}
	objects := map[string]definitionmodel.ObjectSchema{"order": parent, "line_item": related}
	repository := &referenceRepositoryProbe{records: map[string]recordmodel.Record{"order:o1": {ID: "o1"}}}
	var listedObject string
	var listedQuery recordmodel.RecordListQuery
	service := NewRecordReferenceDomainService(RecordReferenceDependencies{
		Repository: repository,
		ObjectForAction: func(_ principalmodel.Principal, objectKey, _ string) (definitionmodel.ObjectSchema, error) {
			return objects[objectKey], nil
		},
		CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
		ListRecords: func(_ context.Context, objectKey string, query recordmodel.RecordListQuery, _ principalmodel.Principal) (recordmodel.RecordPageResult, error) {
			if objectKey == parent.Key {
				return recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "o1"}}, Total: 1}, nil
			}
			listedObject, listedQuery = objectKey, query
			return recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "line-1"}}}, nil
		},
	})

	page, err := service.Related(t.Context(), "order", "o1", "line_item", RecordRelatedRecordsRequest{Page: 2, PageSize: 25}, principalmodel.Principal{Principal: identitysdk.Principal{Known: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || listedObject != "line_item" || listedQuery.Page != 2 || listedQuery.PageSize != 25 || listedQuery.Filters["order_id"] != "o1" {
		t.Fatalf("page=%#v object=%q query=%#v", page, listedObject, listedQuery)
	}
}

func TestReferenceServiceRejectsParentOutsideScope(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer"}
	service := NewRecordReferenceDomainService(RecordReferenceDependencies{
		Repository: &referenceRepositoryProbe{records: map[string]recordmodel.Record{"customer:c1": {ID: "c1"}}},
		ObjectForAction: func(principalmodel.Principal, string, string) (definitionmodel.ObjectSchema, error) {
			return object, nil
		},
		CanAccess:   func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return false },
		ListRecords: scopedReferenceList(&referenceRepositoryProbe{records: map[string]recordmodel.Record{"customer:c1": {ID: "c1"}}}, func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return false }),
	})

	_, err := service.References(t.Context(), "customer", "c1", principalmodel.Principal{Principal: identitysdk.Principal{Known: true}})
	assertRecordAppError(t, err, apperror.KindNotFound, "backend.record.not_found", nil)
}
