package service

import (
	"reflect"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestRecordServiceValueHelpersRemainingShapes(t *testing.T) {
	if got := recordStringListFromAny([]string{"a", "b"}); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Fatalf("string list=%#v", got)
	}
	if got := recordStringListFromAny([]any{" a ", "", nil}); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("any list=%#v", got)
	}
	if got := recordStringListFromAny(" value "); !reflect.DeepEqual(got, []string{"value"}) {
		t.Fatalf("string=%#v", got)
	}
	if got := recordStringListFromAny(" "); len(got) != 0 {
		t.Fatalf("empty string=%#v", got)
	}
	if got := recordValueOrDefault(" value ", "fallback"); got != "value" {
		t.Fatalf("value=%q", got)
	}
	if got := recordValueOrDefault(" ", "fallback"); got != "fallback" {
		t.Fatalf("fallback=%q", got)
	}
	if err := recordServiceError(apperror.KindBadRequest, "code", nil, "", "ignored"); err == nil {
		t.Fatal("record service error helper returned nil")
	}
	if err := recordBadRequest("code"); err == nil {
		t.Fatal("record bad request helper returned nil")
	}
}

func TestRelationValidatorSkipsNonRelationsEmptyValuesAndMissingTargets(t *testing.T) {
	validator := NewRecordRelationValidator(RecordRelationValidationDependencies{})
	object := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{
		{Key: "name", Type: "text"},
		{Key: "empty", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "customer"}},
		{Key: "targetless", Type: "relation"},
	}}
	if err := validator.Validate(t.Context(), object, map[string]any{"name": "x", "empty": "", "targetless": "record-1"}, principalmodel.Principal{}); err != nil {
		t.Fatal(err)
	}
}

func TestReadAndReferenceRemainingExecutableEdges(t *testing.T) {
	service := referenceFailureService(&referenceFailureRepository{}, map[string]definitionmodel.ObjectSchema{}, nil)
	if _, err := service.Related(t.Context(), "missing", "record", "related", RecordRelatedRecordsRequest{}, principalmodel.Principal{}); err == nil {
		t.Fatal("parent object policy error was ignored")
	}
}
