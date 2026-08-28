package validation

import (
	"strings"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestManifestRejectsUnindexedHighRiskRelationAndViewFilter(t *testing.T) {
	manifest := manifestmodel.ManifestSchema{
		Objects: []definitionmodel.ObjectSchema{
			{Key: "account", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text", Config: map[string]any{"indexed": true}}}},
			{Key: "entry", Fields: []definitionmodel.FieldSchema{
				{Key: "account_id", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "account"}, Config: map[string]any{"indexed": false}},
				{Key: "status", Type: "select"},
				{Key: "category", Type: "select"},
			}},
		},
		Views: []definitionmodel.ViewSchema{{Key: "entries", ObjectKey: "entry", Config: map[string]any{"filters": []any{map[string]any{"field": "status"}}}}},
	}
	state := newValidationState(manifest, nil)
	state.validateRequiredIndexes()
	diagnostics := state.errs.Error()
	for _, required := range []string{
		"required_index_missing: relation entry.account_id",
		"required_index_missing: filter entry.status",
	} {
		if !strings.Contains(diagnostics, required) {
			t.Fatalf("missing %q in diagnostics: %s", required, diagnostics)
		}
	}

	manifest.Objects[1].Fields[0].Config["indexed"] = true
	manifest.Objects[1].Fields[1].Config = map[string]any{"indexed": true}
	manifest.Objects[1].Fields[2].Unique = true
	valid := newValidationState(manifest, nil)
	valid.validateRequiredIndexes()
	if len(valid.errs) != 0 {
		t.Fatalf("indexed contract rejected: %v", valid.errs)
	}
}
