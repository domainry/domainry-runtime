package policy

import (
	"reflect"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestActionRecordValuePolicy(t *testing.T) {
	original := map[string]any{"name": "Order", "amount": 10}
	clone := ActionCloneRecordData(original)
	if !reflect.DeepEqual(clone, original) {
		t.Fatalf("clone = %#v, want %#v", clone, original)
	}
	clone["name"] = "Changed"
	if original["name"] != "Order" {
		t.Fatal("clone must isolate map assignments")
	}
	if got := ActionCloneRecordData(nil); got == nil || len(got) != 0 {
		t.Fatalf("nil clone = %#v, want non-nil empty map", got)
	}

	for _, test := range []struct {
		value any
		want  bool
	}{{nil, true}, {"", true}, {"  ", true}, {"value", false}, {0, false}, {false, false}} {
		if got := ActionRecordValueEmpty(test.value); got != test.want {
			t.Fatalf("ActionRecordValueEmpty(%#v) = %v, want %v", test.value, got, test.want)
		}
	}

	object := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{
		{Key: "name", Type: "text"},
		{Key: "customer_id", Type: "relation"},
	}}
	if !ActionObjectFieldExists(object, "name") || ActionObjectFieldExists(object, "missing") {
		t.Fatal("object field existence mismatch")
	}
	if field, ok := ActionRelationField(object, "customer_id"); !ok || field.Key != "customer_id" {
		t.Fatalf("relation field = %+v, %v", field, ok)
	}
	if _, ok := ActionRelationField(object, "name"); ok {
		t.Fatal("non-relation field must not match")
	}
	if _, ok := ActionRelationField(object, "missing"); ok {
		t.Fatal("missing relation field must not match")
	}
}

func TestActionRelationTargetFallbackOrder(t *testing.T) {
	tests := []struct {
		name  string
		field definitionmodel.FieldSchema
		want  string
	}{
		{name: "object key", field: definitionmodel.FieldSchema{Config: map[string]any{"object_key": " customer ", "target": "fallback"}}, want: "customer"},
		{name: "target", field: definitionmodel.FieldSchema{Config: map[string]any{"object_key": nil, "target": " account "}}, want: "account"},
		{name: "blank object key", field: definitionmodel.FieldSchema{Config: map[string]any{"object_key": "", "target": "account"}}, want: "account"},
		{name: "validation", field: definitionmodel.FieldSchema{Config: map[string]any{"target": nil}, Validation: definitionmodel.FieldValidation{Target: " contact "}}, want: "contact"},
		{name: "blank target", field: definitionmodel.FieldSchema{Config: map[string]any{"object_key": nil, "target": ""}}, want: ""},
		{name: "missing config", field: definitionmodel.FieldSchema{Validation: definitionmodel.FieldValidation{Target: "lead"}}, want: "lead"},
		{name: "empty", field: definitionmodel.FieldSchema{}, want: ""},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := ActionRelationTarget(test.field); got != test.want {
				t.Fatalf("target = %q, want %q", got, test.want)
			}
		})
	}
}

func TestActionTransitionEffectsShapesAndIsolation(t *testing.T) {
	anyEffects := []any{"notify", map[string]any{"type": "audit"}}
	gotAny := ActionTransitionEffects(map[string]any{"effects": anyEffects})
	if !reflect.DeepEqual(gotAny, anyEffects) {
		t.Fatalf("any effects = %#v", gotAny)
	}
	gotAny[0] = "changed"
	if anyEffects[0] != "notify" {
		t.Fatal("returned any effects must isolate source slice")
	}

	if got := ActionTransitionEffects(map[string]any{"effects": []string{"notify", "audit"}}); !reflect.DeepEqual(got, []any{"notify", "audit"}) {
		t.Fatalf("string effects = %#v", got)
	}
	single := map[string]any{"type": "webhook"}
	if got := ActionTransitionEffects(map[string]any{"effect": single}); !reflect.DeepEqual(got, []any{single}) {
		t.Fatalf("single effect = %#v", got)
	}
	for _, transition := range []map[string]any{nil, {}, {"effects": "invalid"}, {"effect": "invalid"}} {
		if got := ActionTransitionEffects(transition); got != nil {
			t.Fatalf("invalid transition %#v returned %#v", transition, got)
		}
	}
}
