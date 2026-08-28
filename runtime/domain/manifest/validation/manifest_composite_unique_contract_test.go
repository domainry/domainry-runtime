package validation

import (
	"strings"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestManifestCompositeUniqueUsesOneStrictPublicationContract(t *testing.T) {
	manifest := manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{
		Key: "booking",
		Fields: []definitionmodel.FieldSchema{
			{Key: "session_id", Type: "relation"},
			{Key: "attendee_id", Type: "relation"},
			{Key: "disabled", Type: "text", DisabledAt: "2026-07-21T00:00:00Z"},
		},
		Validations: []definitionmodel.ValidationSchema{
			{Key: "valid", Type: "composite_unique", Fields: []string{"session_id", "attendee_id"}},
			{Key: "too_short", Type: "composite_unique", Fields: []string{"session_id"}},
			{Key: "duplicate", Type: "composite_unique", Fields: []string{"session_id", "session_id"}},
			{Key: "disabled", Type: "composite_unique", Fields: []string{"session_id", "disabled"}},
			{Key: "old_one", Type: "unique_combination", Fields: []string{"session_id", "attendee_id"}},
			{Key: "old_two", Type: "unique_composite", Fields: []string{"session_id", "attendee_id"}},
		},
	}}}
	state := newValidationState(manifest, nil)
	state.validateObjects()
	diagnostics := state.errs.Error()
	for _, code := range []string{
		"backend.definition.composite_unique_fields_required",
		"backend.definition.composite_unique_field_duplicate",
		"backend.definition.composite_unique_field_disabled",
		"backend.definition.validation_type_unsupported: unique_combination",
		"backend.definition.validation_type_unsupported: unique_composite",
	} {
		if !strings.Contains(diagnostics, code) {
			t.Fatalf("missing %q in diagnostics: %s", code, diagnostics)
		}
	}

	valid := newValidationState(manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: "booking", Fields: manifest.Objects[0].Fields[:2], Validations: manifest.Objects[0].Validations[:1]}}}, nil)
	valid.validateObjects()
	if len(valid.errs) != 0 {
		t.Fatalf("valid composite unique diagnostics=%v", valid.errs)
	}
}
