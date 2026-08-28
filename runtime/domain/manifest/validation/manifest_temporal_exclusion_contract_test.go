package validation

import (
	"strings"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestManifestTemporalExclusionRequiresTypedIndexedContractAndRejectsOldAliases(t *testing.T) {
	fields := []definitionmodel.FieldSchema{{Key: "owner", Type: "relation"}, {Key: "starts_at", Type: "datetime"}, {Key: "ends_at", Type: "datetime"}, {Key: "status", Type: "select"}, {Key: "note", Type: "text"}}
	validPolicy := definitionmodel.ValidationSchema{Key: "schedule", Type: "temporal_exclusion", Config: map[string]any{"start_field": "starts_at", "end_field": "ends_at", "scope_fields": []any{"owner"}, "status_field": "status", "excluded_statuses": []any{"cancelled"}}}
	valid := newValidationState(manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: "booking", Fields: fields, Validations: []definitionmodel.ValidationSchema{validPolicy}}}}, nil)
	valid.validateObjects()
	if len(valid.errs) != 0 {
		t.Fatalf("valid temporal exclusion diagnostics=%v", valid.errs)
	}

	invalid := []definitionmodel.ValidationSchema{
		{Key: "missing", Type: "temporal_exclusion", Config: map[string]any{}},
		{Key: "same", Type: "temporal_exclusion", Config: map[string]any{"start_field": "starts_at", "end_field": "starts_at", "scope_fields": []any{"owner", "owner"}}},
		{Key: "wrong_type", Type: "temporal_exclusion", Config: map[string]any{"start_field": "note", "end_field": "ends_at", "scope_fields": []any{"missing"}, "excluded_statuses": []any{"cancelled"}, "ignored_statuses": []any{"void"}}},
		{Key: "old_one", Type: "time_overlap"}, {Key: "old_two", Type: "no_time_overlap"}, {Key: "old_three", Type: "time_conflict"},
	}
	state := newValidationState(manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{{Key: "booking", Fields: fields, Validations: invalid}}}, nil)
	state.validateObjects()
	diagnostics := state.errs.Error()
	for _, code := range []string{
		"backend.validation.temporal_exclusion_range_fields_required",
		"backend.validation.temporal_exclusion_range_fields_distinct",
		"backend.validation.temporal_exclusion_scope_field_duplicate",
		"backend.validation.temporal_exclusion_field_type_invalid",
		"backend.validation.temporal_exclusion_field_unknown",
		"backend.validation.temporal_exclusion_status_field_required",
		"backend.definition.validation_config_unsupported: ignored_statuses",
		"backend.definition.validation_type_unsupported: time_overlap",
		"backend.definition.validation_type_unsupported: no_time_overlap",
		"backend.definition.validation_type_unsupported: time_conflict",
	} {
		if !strings.Contains(diagnostics, code) {
			t.Fatalf("missing %q in diagnostics: %s", code, diagnostics)
		}
	}
}
