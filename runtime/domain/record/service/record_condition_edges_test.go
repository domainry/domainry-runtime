package service

import (
	"context"
	profilebindingmodel "github.com/domainry/domainry-runtime/runtime/domain/profilebinding/model"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestReadRemainingConditionOperands(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "employee_profile", UX: map[string]any{"kind": "identity_profile_extension", "config": map[string]any{"identity_relation_field": "identity_user"}}}
	service := NewRecordReadDomainService(RecordReadDependencies{
		Repository: &readRepositoryProbe{}, Policy: readPolicyProbe{object: object, allow: true},
		IdentityProfileExtensions: func() []profilebindingmodel.Binding {
			return []profilebindingmodel.Binding{{ObjectKey: object.Key, IdentityRelationField: "identity_user"}}
		},
	})
	if refs, err := service.IdentityProfileReferences(t.Context(), " ", principalmodel.Principal{}); err != nil || len(refs) != 0 {
		t.Fatalf("empty user refs=%#v err=%v", refs, err)
	}
	if refs, err := service.IdentityProfileReferences(t.Context(), "user", principalmodel.Principal{}); err != nil || len(refs) != 0 {
		t.Fatalf("zero refs=%#v err=%v", refs, err)
	}
	repository := &readRepositoryProbe{page: recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "hidden"}}}}
	reader := NewRecordReadDomainService(RecordReadDependencies{Repository: repository, Policy: readPolicyProbe{object: definitionmodel.ObjectSchema{Key: "customer"}, allow: false}})
	if page, err := reader.ListRecords(t.Context(), "customer", recordmodel.RecordListQuery{}, principalmodel.Principal{}); err != nil || len(page.Items) != 0 {
		t.Fatalf("denied page=%#v err=%v", page, err)
	}
}

func TestReferenceAndUniquenessRemainingConditionOperands(t *testing.T) {
	customer := definitionmodel.ObjectSchema{Key: "customer"}
	order := definitionmodel.ObjectSchema{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "customer_id", Type: "relation", Config: map[string]any{"object_key": "", "target": "customer"}}}}
	repository := &referenceFailureRepository{found: true}
	service := NewRecordReferenceDomainService(RecordReferenceDependencies{
		Repository: repository,
		ObjectForAction: func(_ principalmodel.Principal, key, _ string) (definitionmodel.ObjectSchema, error) {
			if key == "order" {
				return order, nil
			}
			return customer, nil
		},
		ListRecords: func(_ context.Context, key string, query recordmodel.RecordListQuery, _ principalmodel.Principal) (recordmodel.RecordPageResult, error) {
			if key == "customer" && query.Filters["id__in"] != nil {
				return recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "customer-1"}}, Total: 1}, nil
			}
			return recordmodel.RecordPageResult{}, nil
		},
	})
	if _, err := service.Related(t.Context(), "customer", "customer-1", "order", RecordRelatedRecordsRequest{}, principalmodel.Principal{}); err != nil {
		t.Fatal(err)
	}
	other := definitionmodel.ObjectSchema{Key: "order", Fields: []definitionmodel.FieldSchema{{Key: "other", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "other"}}}}
	if _, err := RecordRelatedRelationField("customer", other, ""); err == nil {
		t.Fatal("unrelated relation was accepted")
	}
	if err := recordBadRequest("code", "", "value"); err == nil {
		t.Fatal("blank-key request error missing")
	}
	if got := relationTarget(definitionmodel.FieldSchema{Config: map[string]any{"target": ""}}); got != "" {
		t.Fatalf("empty relation target=%q", got)
	}
	validator := NewRecordUniquenessValidator(&uniquenessRepositoryProbe{})
	if err := validator.ValidateDuplicateIdentity(t.Context(), "workspace", definitionmodel.ObjectSchema{Key: "document"}, "", map[string]any{"previous_version_id": ""}); err != nil {
		t.Fatal(err)
	}
}

func TestDomainPolicyErrorWithoutDeniedObserver(t *testing.T) {
	validator := NewRecordRelatedPolicyValidator(RecordRelatedPolicyDependencies{})
	object := definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{{
		Type: "state_machine", FieldKey: "status",
		Config: map[string]any{"transitions": []any{map[string]any{"from": "draft", "to": "done"}}},
	}}}
	err := validator.ValidateDomainPolicies(t.Context(), object, map[string]any{"status": "draft"}, map[string]any{"status": "cancelled"}, "record", "update", principalmodel.Principal{}, nil)
	if err == nil {
		t.Fatal("invalid transition was accepted")
	}
}
