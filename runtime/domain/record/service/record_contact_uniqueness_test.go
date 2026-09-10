package service

import (
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestRecordContactUniquenessIsDeclaredOnly(t *testing.T) {
	for _, key := range []string{"phone", "email"} {
		for _, currentID := range []string{"", "existing-vendor"} {
			t.Run(key+"/"+currentID, func(t *testing.T) {
				object := definitionmodel.ObjectSchema{Key: "vendor", Fields: []definitionmodel.FieldSchema{{Key: key, Type: "text"}}}
				data := map[string]any{key: "shared-contact"}
				validator := NewRecordUniquenessValidator(&uniquenessRepositoryProbe{uniqueExists: true})
				if err := validator.ValidateDuplicateIdentity(t.Context(), "workspace", object, currentID, data); err != nil {
					t.Fatal(err)
				}
				if err := validator.ValidateUnique(t.Context(), "workspace", object.Key, object, currentID, data); err != nil {
					t.Fatal(err)
				}
				object.Fields[0].Unique = true
				if err := validator.ValidateUnique(t.Context(), "workspace", object.Key, object, currentID, data); apperror.CodeOf(err) != "backend.unique.field" {
					t.Fatalf("declared unique constraint lost: %v", err)
				}
			})
		}
	}
}
