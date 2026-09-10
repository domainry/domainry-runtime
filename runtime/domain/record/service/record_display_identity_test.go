package service

import (
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestUniquenessValidatorAllowsRepeatedDisplayValues(t *testing.T) {
	for _, field := range []string{"title", "name"} {
		t.Run(field, func(t *testing.T) {
			// Another scheduled session already has this display value. A course
			// may recur without changing its public title or declaring it unique.
			validator := NewRecordUniquenessValidator(&uniquenessRepositoryProbe{uniqueExists: true})
			object := definitionmodel.ObjectSchema{Key: "class_session", Fields: []definitionmodel.FieldSchema{{Key: field, Type: "text"}}}
			data := map[string]any{field: "Strength basics"}
			for _, currentID := range []string{"", "session-next-week"} {
				if err := validator.ValidateUnique(t.Context(), "workspace", object.Key, object, currentID, data); err != nil {
					t.Fatalf("display value rejected by explicit constraints: %v", err)
				}
				if err := validator.ValidateDuplicateIdentity(t.Context(), "workspace", object, currentID, data); err != nil {
					t.Fatalf("repeated display value rejected for record %q: %v", currentID, err)
				}
			}
		})
	}
}

func TestUniquenessValidatorRetainsExplicitDisplayUniqueness(t *testing.T) {
	for _, field := range []string{"title", "name"} {
		t.Run(field, func(t *testing.T) {
			validator := NewRecordUniquenessValidator(&uniquenessRepositoryProbe{uniqueExists: true})
			object := definitionmodel.ObjectSchema{Key: "catalog_entry", Fields: []definitionmodel.FieldSchema{{Key: field, Type: "text", Unique: true}}}
			err := validator.ValidateUnique(t.Context(), "workspace", object.Key, object, "", map[string]any{field: "Unique catalog entry"})
			assertRecordAppError(t, err, apperror.KindBadRequest, "backend.unique.field", map[string]string{"field": field})
		})
	}
}
