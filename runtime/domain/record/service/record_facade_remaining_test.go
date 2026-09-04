package service

import (
	"context"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestRecordDomainServiceDelegatesOwnerOperations(t *testing.T) {
	var absent *RecordDomainService
	if absent.Repository() != nil || absent.IdentityProjection() != nil {
		t.Fatal("nil service exposed dependencies")
	}

	customer := definitionmodel.ObjectSchema{Key: "customer"}
	order := definitionmodel.ObjectSchema{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "customer_id", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "customer"}}}}
	repository := &readRepositoryProbe{
		page:   recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "customer-1"}}, Total: 1},
		record: recordmodel.Record{ID: "customer-1"},
		found:  true,
	}
	reader := NewRecordReadDomainService(RecordReadDependencies{
		Repository: repository,
		Policy:     readPolicyProbe{object: customer, allow: true},
	})
	referenceRepository := &referenceRepositoryProbe{records: map[string]recordmodel.Record{"customer:customer-1": {ID: "customer-1"}}}
	references := NewRecordReferenceDomainService(RecordReferenceDependencies{
		Repository: referenceRepository,
		Objects: func() map[string]definitionmodel.ObjectSchema {
			return map[string]definitionmodel.ObjectSchema{"customer": customer}
		},
		ObjectForAction: func(_ principalmodel.Principal, key, _ string) (definitionmodel.ObjectSchema, error) {
			if key == order.Key {
				return order, nil
			}
			return customer, nil
		},
		CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
		ListRecords: func(_ context.Context, key string, query recordmodel.RecordListQuery, _ principalmodel.Principal) (recordmodel.RecordPageResult, error) {
			if key == customer.Key && query.Filters["id__in"] != nil {
				return recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "customer-1"}}, Total: 1}, nil
			}
			if key != order.Key || query.Filters["customer_id"] != "customer-1" {
				t.Fatalf("related delegation key=%q query=%#v", key, query)
			}
			return recordmodel.RecordPageResult{Total: 2}, nil
		},
	})
	service := NewRecordDomainService(RecordDomainServiceDependencies{Repository: repository, Reader: reader, References: references})
	principal := principalmodel.Principal{Principal: identitysdk.Principal{WorkspaceID: "workspace", Known: true}}
	if page, err := service.ListRecords(t.Context(), customer.Key, recordmodel.RecordListQuery{}, principal); err != nil || page.Total != 1 {
		t.Fatalf("list page=%#v err=%v", page, err)
	}
	if page, err := service.ListRecordsForAction(t.Context(), customer.Key, recordmodel.RecordListQuery{}, principal); err != nil || page.Total != 1 {
		t.Fatalf("action list page=%#v err=%v", page, err)
	}
	if record, err := service.GetRecord(t.Context(), customer.Key, "customer-1", principal); err != nil || record.ID != "customer-1" {
		t.Fatalf("get record=%#v err=%v", record, err)
	}
	for name, call := range map[string]func() (recordmodel.Record, error){
		"update": func() (recordmodel.Record, error) {
			return service.GetRecordForUpdate(t.Context(), customer.Key, "customer-1", principal)
		},
		"action": func() (recordmodel.Record, error) {
			return service.GetRecordForAction(t.Context(), customer.Key, "customer-1", principal)
		},
		"update action": func() (recordmodel.Record, error) {
			return service.GetRecordForUpdateForAction(t.Context(), customer.Key, "customer-1", principal)
		},
	} {
		if record, err := call(); err != nil || record.ID != "customer-1" {
			t.Fatalf("%s record=%#v err=%v", name, record, err)
		}
	}
	if summary, err := service.RecordReferences(t.Context(), customer.Key, "customer-1", principal); err != nil || summary.RecordID != "customer-1" {
		t.Fatalf("references=%#v err=%v", summary, err)
	}
	if page, err := service.RelatedRecords(t.Context(), customer.Key, "customer-1", order.Key, RecordRelatedRecordsRequest{}, principal); err != nil || page.Total != 2 {
		t.Fatalf("related page=%#v err=%v", page, err)
	}
	if references, err := service.IdentityProfileReferences(t.Context(), "user-1", principal); err != nil || len(references) != 0 {
		t.Fatalf("identity references=%#v err=%v", references, err)
	}
}

func TestRecordValidationFacadeDelegatesEmptyPolicySet(t *testing.T) {
	repository := &uniquenessRepositoryProbe{}
	service := NewRecordValidationDomainService(RecordValidationDependencies{
		Repository: repository,
		Object: func(context.Context, string) (definitionmodel.ObjectSchema, bool) {
			return definitionmodel.ObjectSchema{}, false
		},
		CanAccess: func(principalmodel.Principal, definitionmodel.ObjectSchema, recordmodel.Record) bool { return true },
	})
	object := definitionmodel.ObjectSchema{Key: "customer"}
	principal := principalmodel.Principal{}
	if err := service.ValidateRelations(t.Context(), object, map[string]any{}, principal); err != nil {
		t.Fatal(err)
	}
	if err := service.ValidateDomainPolicies(t.Context(), object, nil, map[string]any{}, "customer-1", "update", principal); err != nil {
		t.Fatal(err)
	}
	if err := service.ValidateDuplicateIdentity(t.Context(), "workspace", object, "customer-1", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if err := service.ValidateUnique(t.Context(), "workspace", object.Key, object, "customer-1", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	if err := service.ValidateStateMachinePolicies(object, nil, map[string]any{}, principal); err != nil {
		t.Fatal(err)
	}
}
