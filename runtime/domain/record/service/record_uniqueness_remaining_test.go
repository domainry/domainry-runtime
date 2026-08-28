package service

import (
	"errors"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestUniquenessValidatorDuplicateIdentityRemainingEdges(t *testing.T) {
	validator := NewRecordUniquenessValidator(&uniquenessRepositoryProbe{})
	if err := validator.ValidateDuplicateIdentity(t.Context(), "workspace", definitionmodel.ObjectSchema{
		Key: "contact", Fields: []definitionmodel.FieldSchema{{Key: "email", Unique: true}},
	}, "", map[string]any{"email": "ada@example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := validator.ValidateDuplicateIdentity(t.Context(), "workspace", definitionmodel.ObjectSchema{
		Key: "contact", Fields: []definitionmodel.FieldSchema{{Key: "email"}},
	}, "", map[string]any{"email": ""}); err != nil {
		t.Fatal(err)
	}
	repository := &uniquenessRepositoryProbe{uniqueErr: errors.New("store failed")}
	validator = NewRecordUniquenessValidator(repository)
	if err := validator.ValidateDuplicateIdentity(t.Context(), "workspace", definitionmodel.ObjectSchema{
		Key: "contact", Fields: []definitionmodel.FieldSchema{{Key: "email"}},
	}, "", map[string]any{"email": "ada@example.com"}); err == nil {
		t.Fatal("duplicate identity repository error was ignored")
	}
	repository.uniqueErr = nil
	if err := validator.ValidateDuplicateIdentity(t.Context(), "workspace", definitionmodel.ObjectSchema{
		Key: "contact", Fields: []definitionmodel.FieldSchema{{Key: "email"}},
	}, "", map[string]any{"email": "ada@example.com"}); err != nil {
		t.Fatal(err)
	}
}

func TestUniquenessValidatorConditionalRemainingEdges(t *testing.T) {
	base := definitionmodel.ObjectSchema{Key: "booking", Fields: []definitionmodel.FieldSchema{{Key: "member"}, {Key: "status"}}, Validations: []definitionmodel.ValidationSchema{{
		Key: "active-member", Type: "conditional_unique", Fields: []string{"member"},
		Config: map[string]any{"condition_field": "status", "condition_values": []any{"active"}},
	}}}
	validator := NewRecordUniquenessValidator(&uniquenessRepositoryProbe{})
	if err := validator.ValidateUnique(t.Context(), "workspace", base.Key, base, "candidate", map[string]any{"member": "", "status": "active"}); err != nil {
		t.Fatalf("incomplete conditional values err=%v", err)
	}

	malformed := base
	malformed.Validations = append([]definitionmodel.ValidationSchema(nil), base.Validations...)
	malformed.Validations[0].Config = map[string]any{}
	if err := validator.ValidateUnique(t.Context(), "workspace", malformed.Key, malformed, "candidate", map[string]any{"member": "member-1", "status": "active"}); err == nil {
		t.Fatal("malformed conditional policy accepted")
	}

	repository := &uniquenessRepositoryProbe{listErr: errors.New("conditional store failed")}
	validator = NewRecordUniquenessValidator(repository)
	if err := validator.ValidateUnique(t.Context(), "workspace", base.Key, base, "candidate", map[string]any{"member": "member-1", "status": "active"}); err == nil {
		t.Fatal("conditional list failure ignored")
	}

	repository.listErr = nil
	repository.page = recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "candidate"}}}
	if err := validator.ValidateUnique(t.Context(), "workspace", base.Key, base, "candidate", map[string]any{"member": "member-1", "status": "active"}); err != nil {
		t.Fatalf("current conditional record rejected: %v", err)
	}
	repository.page = recordmodel.RecordPageResult{Items: []recordmodel.Record{{ID: "other"}}}
	if err := validator.ValidateUnique(t.Context(), "workspace", base.Key, base, "candidate", map[string]any{"member": "member-1", "status": "active"}); err == nil {
		t.Fatal("default conditional duplicate accepted")
	}
}

func TestUniquenessValidatorCompositeRemainingEdges(t *testing.T) {
	object := definitionmodel.ObjectSchema{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "email", Unique: true}}, Validations: []definitionmodel.ValidationSchema{
		{Key: "other", Type: "required"},
		{Key: "empty", Type: "composite_unique"},
		{Key: "blank", Type: "composite_unique", Fields: []string{""}},
		{Key: "missing", Type: "composite_unique", Fields: []string{"country"}},
	}}
	validator := NewRecordUniquenessValidator(&uniquenessRepositoryProbe{})
	if err := validator.ValidateUnique(t.Context(), "workspace", object.Key, object, "", map[string]any{"email": ""}); err != nil {
		t.Fatal(err)
	}
	repository := &uniquenessRepositoryProbe{listErr: errors.New("store failed")}
	validator = NewRecordUniquenessValidator(repository)
	object.Validations = []definitionmodel.ValidationSchema{{Key: "composite", Type: "composite_unique", Fields: []string{"email", "country"}}}
	if err := validator.ValidateUnique(t.Context(), "workspace", object.Key, object, "", map[string]any{"email": "ada@example.com", "country": "CN"}); err == nil {
		t.Fatal("composite repository error was ignored")
	}
}
