package validation

import (
	"strings"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func atomicMutationTestObject() definitionmodel.ObjectSchema {
	return definitionmodel.ObjectSchema{
		Key: "order",
		Fields: []definitionmodel.FieldSchema{
			{Key: " name ", Type: "text"},
			{Key: "amount", Type: "number"},
			{Key: "ratio", Type: "percent"},
			{Key: "total", Type: "currency"},
		},
	}
}

func TestValidateAtomicMutationBatchCoversMutationAndReturningContracts(t *testing.T) {
	state := &validationState{objects: map[string]definitionmodel.ObjectSchema{"order": atomicMutationTestObject()}}
	action := definitionmodel.ActionSchema{ObjectKey: "order"}
	state.validateAtomicMutationBatch(action, map[string]any{}, "actions[0].steps[0]")
	if len(state.errs) != 1 || !strings.Contains(state.errs[0].Message, "atomic_mutations_required") {
		t.Fatalf("empty mutation diagnostics=%#v", state.errs)
	}

	state.errs = nil
	state.validateAtomicMutationBatch(action, map[string]any{"mutations": []map[string]any{
		{
			"operation": "update",
			"data":      map[string]any{"missing": "value", "name": "$steps.later.id"},
			"patch":     map[string]any{"amount": "$steps.later.amount"},
			"where":     map[string]any{"name": "plain"},
			"increment": map[string]any{"missing": 1, "amount": "$steps.later.ratio", "total": 1},
			"decrement": map[string]any{"name": 1, "ratio": "$steps.no_such.id"},
			"preconditions": []map[string]any{
				{"field": "missing", "operator": "unknown", "value": 1},
				{"field": "amount", "operator": "eq", "value": "$steps.later.too.many"},
			},
			"precondition": map[string]any{"field": "ratio", "operator": "gte", "value": "$steps.later.amount"},
			"returning":    []string{"id", "name", "missing"},
			"as":           "first",
		},
		{"operation": "create", "object_key": "$input.object", "data": map[string]any{"name": "ignored"}},
		{"operation": "delete", "object_key": "missing"},
		{"operation": "create", "returning": []string{"id"}},
		{"operation": "create", "returning": []string{"id"}, "as": "first"},
		{"operation": "create", "returning": []string{"id", "amount", "ratio"}, "key": "later"},
		{"operation": "create", "data": map[string]any{"name": "without returning"}},
	}}, "actions[0].steps[0]")
	if len(state.errs) == 0 {
		t.Fatal("invalid atomic mutation batch produced no diagnostics")
	}
	codes := map[string]bool{}
	for _, diagnostic := range state.errs {
		codes[diagnostic.Message] = true
	}
	for _, expected := range []string{
		"backend.action.atomic_field_unknown",
		"backend.action.atomic_arithmetic_type_invalid",
		"backend.action.atomic_predicate_operator_invalid",
		"backend.action.atomic_dynamic_object_forbidden",
		"backend.action.atomic_operation_invalid",
		"backend.action.atomic_returning_alias_required",
		"backend.action.atomic_returning_alias_duplicate",
		"backend.action.atomic_returning_reference_forward",
		"backend.action.atomic_returning_reference_invalid",
		"backend.action.atomic_returning_field_invalid",
	} {
		found := false
		for message := range codes {
			if strings.Contains(message, expected) {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing %s in diagnostics=%#v", expected, state.errs)
		}
	}

	fields := manifestAtomicFields(atomicMutationTestObject())
	if len(fields) != 4 || fields["name"].Type != "text" {
		t.Fatalf("atomic fields=%#v", fields)
	}
}

func TestValidateAtomicReturningReferenceCoversReferenceOutcomes(t *testing.T) {
	state := &validationState{}
	aliases := map[string]map[string]definitionmodel.FieldSchema{
		"source": {
			"text":   {Key: "text", Type: "text"},
			"number": {Key: "number", Type: "number"},
			"ratio":  {Key: "ratio", Type: "percent"},
		},
	}
	allAliases := map[string]bool{"future": true}
	textTarget := definitionmodel.FieldSchema{Key: "name", Type: "text"}
	numberTarget := definitionmodel.FieldSchema{Key: "amount", Type: "number"}

	for _, value := range []any{42, "plain", "$steps.missing.id"} {
		state.validateAtomicReturningReference(textTarget, aliases, allAliases, value, "value")
	}
	state.validateAtomicReturningReference(textTarget, aliases, allAliases, "$steps.future.id", "forward")
	state.validateAtomicReturningReference(textTarget, aliases, allAliases, "$steps.source", "invalid")
	state.validateAtomicReturningReference(textTarget, aliases, allAliases, "$steps.source.missing", "missing")
	state.validateAtomicReturningReference(textTarget, aliases, allAliases, "$steps.source.number", "mismatch")
	state.validateAtomicReturningReference(textTarget, aliases, allAliases, "$steps.source.text", "same")
	state.validateAtomicReturningReference(numberTarget, aliases, allAliases, "$steps.source.ratio", "numeric")
	state.validateAtomicMutationFieldReference(map[string]definitionmodel.FieldSchema{}, aliases, allAliases, "missing", "value", "field")
	state.validateAtomicMutationFieldReference(map[string]definitionmodel.FieldSchema{"name": textTarget}, aliases, allAliases, "name", "$steps.source.text", "field")

	for _, expectedPath := range []string{"forward", "invalid", "missing", "mismatch", "field"} {
		found := false
		for _, diagnostic := range state.errs {
			if diagnostic.Path == expectedPath {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing diagnostic path %q in %#v", expectedPath, state.errs)
		}
	}
}
