package runtimeext

import (
	"reflect"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestGeneratedActionOutputFieldsRemainWithoutObjectLineage(t *testing.T) {
	got := GeneratedActionOutputFields(GeneratedActionOutputField{
		Key: " initial_credential ", Type: " object ", Required: true,
	})
	want := []definitionmodel.ActionOutputField{{
		Key: "initial_credential", Type: "object", Required: true,
	}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("fields=%#v want=%#v", got, want)
	}
}
