package service

import (
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordrepository "github.com/domainry/domainry-runtime/runtime/domain/record/repository"

	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

type uniquenessRepositoryProbe struct {
	recordrepository.RecordRepository
	uniqueExists bool
	uniqueErr    error
	page         recordmodel.RecordPageResult
	listErr      error
	lastFilters  map[string]any
}

func (r *uniquenessRepositoryProbe) UniqueExists(context.Context, string, string, string, string, any) (bool, error) {
	return r.uniqueExists, r.uniqueErr
}

func (r *uniquenessRepositoryProbe) ListRecords(_ context.Context, _ string, _ definitionmodel.ObjectSchema, query recordmodel.RecordListQuery) (recordmodel.RecordPageResult, error) {
	r.lastFilters = query.Filters
	return r.page, r.listErr
}

func TestUniquenessValidatorRejectsUniqueFieldAndComposite(t *testing.T) {
	object := definitionmodel.ObjectSchema{
		Key: "customer",
		Fields: []definitionmodel.FieldSchema{
			{Key: "email", Unique: true},
			{Key: "country"},
		},
		Validations: []definitionmodel.ValidationSchema{{Key: "customer_country", Type: "composite_unique", Fields: []string{"email", "country"}}},
	}
	repository := &uniquenessRepositoryProbe{uniqueExists: true}
	validator := NewRecordUniquenessValidator(repository)
	err := validator.ValidateUnique(t.Context(), "workspace-primary", object.Key, object, "", map[string]any{"email": "ada@example.com", "country": "CN"})
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.unique.field", map[string]string{"field": "email"})

	repository.uniqueExists = false
	repository.page = recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "customer-2"}}}
	err = validator.ValidateUnique(t.Context(), "workspace-primary", object.Key, object, "customer-1", map[string]any{"email": "ada@example.com", "country": "CN"})
	assertRecordAppError(t, err, apperror.KindBadRequest, "backend.unique.combination", map[string]string{"validation": "customer_country"})
	if !reflect.DeepEqual(repository.lastFilters, map[string]any{"email": "ada@example.com", "country": "CN"}) {
		t.Fatalf("composite filters = %#v", repository.lastFilters)
	}

	repository.page = recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "customer-1"}}}
	if err := validator.ValidateUnique(t.Context(), "workspace-primary", object.Key, object, "customer-1", map[string]any{"email": "ada@example.com", "country": "CN"}); err != nil {
		t.Fatalf("current record rejected by composite validation: %v", err)
	}
}

func TestUniquenessValidatorChecksEveryConditionalStateAndAllowsInactiveHistory(t *testing.T) {
	object := definitionmodel.ObjectSchema{
		Key: "class_booking",
		Fields: []definitionmodel.FieldSchema{
			{Key: "class_id"}, {Key: "member_id"}, {Key: "status"},
		},
		Validations: []definitionmodel.ValidationSchema{{
			Key: "one_active_booking", Type: "conditional_unique", Fields: []string{"class_id", "member_id"}, Message: "gym.duplicate_active_booking",
			Config: map[string]any{"condition_field": "status", "condition_values": []any{"booked", "waitlisted"}},
		}},
	}
	repository := &uniquenessRepositoryProbe{}
	validator := NewRecordUniquenessValidator(repository)
	inactive := map[string]any{"class_id": "class-1", "member_id": "member-1", "status": "cancelled"}
	if err := validator.ValidateUnique(t.Context(), "workspace", object.Key, object, "", inactive); err != nil {
		t.Fatalf("inactive history was rejected: %v", err)
	}
	if repository.lastFilters != nil {
		t.Fatalf("inactive history reached uniqueness query: %#v", repository.lastFilters)
	}

	repository.page = recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "existing"}}}
	active := map[string]any{"class_id": "class-1", "member_id": "member-1", "status": "booked"}
	err := validator.ValidateUnique(t.Context(), "workspace", object.Key, object, "candidate", active)
	assertRecordAppError(t, err, apperror.KindBadRequest, "gym.duplicate_active_booking", map[string]string{"validation": "one_active_booking"})
	if !reflect.DeepEqual(repository.lastFilters, map[string]any{"class_id": "class-1", "member_id": "member-1", "status": "booked"}) {
		t.Fatalf("conditional filters = %#v", repository.lastFilters)
	}
}

func TestUniquenessValidatorAllowsUndeclaredContactIdentity(t *testing.T) {
	repository := &uniquenessRepositoryProbe{uniqueExists: true}
	validator := NewRecordUniquenessValidator(repository)
	object := definitionmodel.ObjectSchema{Key: "contact", Fields: []definitionmodel.FieldSchema{{Key: "email"}}}
	err := validator.ValidateDuplicateIdentity(t.Context(), "workspace-primary", object, "", map[string]any{"email": "ada@example.com"})
	if err != nil {
		t.Fatalf("ordinary email field rejected: %v", err)
	}

	document := definitionmodel.ObjectSchema{Key: "document", Fields: []definitionmodel.FieldSchema{{Key: "name"}}}
	if err := validator.ValidateDuplicateIdentity(t.Context(), "workspace-primary", document, "", map[string]any{"name": "Policy", "previous_version_id": "doc-1"}); err != nil {
		t.Fatalf("document version duplicate check was not skipped: %v", err)
	}
}

func TestUniquenessValidatorWrapsRepositoryErrors(t *testing.T) {
	repository := &uniquenessRepositoryProbe{uniqueErr: errors.New("store unavailable")}
	validator := NewRecordUniquenessValidator(repository)
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "email", Unique: true}}}
	err := validator.ValidateUnique(t.Context(), "workspace-primary", object.Key, object, "", map[string]any{"email": "ada@example.com"})
	assertRecordAppError(t, err, apperror.KindInternal, "backend.internal", map[string]string{"operation": "check unique field"})
}

func TestUniquenessValidatorUsesConstructorRepository(t *testing.T) {
	validator := NewRecordUniquenessValidator(&uniquenessRepositoryProbe{})
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "email", Unique: true}}}
	if err := validator.ValidateUnique(t.Context(), "workspace-primary", object.Key, object, "", map[string]any{"email": "ada@example.com"}); err != nil {
		t.Fatalf("constructor repository was not used: %v", err)
	}
}
