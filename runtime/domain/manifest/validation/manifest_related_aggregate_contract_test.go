package validation

import (
	"strings"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
)

func TestManifestRelatedAggregateInvariantValidatesRelationTypesAndLimit(t *testing.T) {
	payment := definitionmodel.ObjectSchema{Key: "payment", Fields: []definitionmodel.FieldSchema{{Key: "paid_amount", Type: "currency"}, {Key: "capacity", Type: "number"}}}
	refundFields := []definitionmodel.FieldSchema{{Key: "payment_id", Type: "relation", Validation: definitionmodel.FieldValidation{Target: "payment"}}, {Key: "amount", Type: "currency"}, {Key: "status", Type: "select"}, {Key: "note", Type: "text"}}
	validPolicy := definitionmodel.ValidationSchema{Key: "refund_limit", Type: "related_aggregate_invariant", Config: map[string]any{"relation_field": "payment_id", "aggregate": "sum", "value_field": "amount", "limit_field": "paid_amount", "operator": "lte", "status_field": "status", "included_statuses": []any{"approved"}}}
	valid := newValidationState(manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{payment, {Key: "refund", Fields: refundFields, Validations: []definitionmodel.ValidationSchema{validPolicy}}}}, nil)
	valid.validateObjects()
	if len(valid.errs) != 0 {
		t.Fatalf("valid aggregate invariant diagnostics=%v", valid.errs)
	}

	invalid := []definitionmodel.ValidationSchema{
		{Key: "relation", Type: "related_aggregate_invariant", Config: map[string]any{"relation_field": "note"}},
		{Key: "aggregate", Type: "related_aggregate_invariant", Config: map[string]any{"relation_field": "payment_id", "aggregate": "avg", "limit_field": "paid_amount", "operator": "lte"}},
		{Key: "value", Type: "related_aggregate_invariant", Config: map[string]any{"relation_field": "payment_id", "aggregate": "sum", "value_field": "note", "limit_field": "paid_amount", "operator": "lte"}},
		{Key: "limit", Type: "related_aggregate_invariant", Config: map[string]any{"relation_field": "payment_id", "aggregate": "sum", "value_field": "amount", "limit_field": "missing", "operator": "lte"}},
		{Key: "operator", Type: "related_aggregate_invariant", Config: map[string]any{"relation_field": "payment_id", "aggregate": "count", "value_field": "amount", "limit_field": "paid_amount", "operator": "between", "included_statuses": []any{"approved"}}},
	}
	state := newValidationState(manifestmodel.ManifestSchema{Objects: []definitionmodel.ObjectSchema{payment, {Key: "refund", Fields: refundFields, Validations: invalid}}}, nil)
	state.validateObjects()
	diagnostics := state.errs.Error()
	for _, code := range []string{
		"backend.validation.related_aggregate_relation_required",
		"backend.validation.related_aggregate_operation_invalid",
		"backend.validation.related_aggregate_value_type_invalid",
		"backend.validation.related_aggregate_limit_field_required",
		"backend.validation.related_aggregate_count_value_forbidden",
		"backend.validation.related_aggregate_count_limit_type_invalid",
		"backend.validation.related_aggregate_operator_invalid",
		"backend.validation.related_aggregate_status_field_required",
	} {
		if !strings.Contains(diagnostics, code) {
			t.Fatalf("missing %q in diagnostics: %s", code, diagnostics)
		}
	}
}
