package validation

import (
	"reflect"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestManifestStateMachineEffectCollectionHelpersCoverEveryInputShape(t *testing.T) {
	mapped := map[string]any{"type": "patch_self"}
	if got := validationMapSlice([]map[string]any{mapped}); len(got) != 1 {
		t.Fatalf("map slice=%#v", got)
	}
	if got := validationMapSlice([]any{mapped, "ignored", map[string]any{}}); len(got) != 1 {
		t.Fatalf("any slice=%#v", got)
	}
	if got := validationMapSlice("invalid"); got != nil {
		t.Fatalf("invalid map slice=%#v", got)
	}
	if got := validationMap(mapped); !reflect.DeepEqual(got, mapped) {
		t.Fatalf("mapped=%#v", got)
	}
	if got := validationMap("invalid"); len(got) != 0 {
		t.Fatalf("invalid map=%#v", got)
	}

	if got := validationStringList([]string{"a"}); !reflect.DeepEqual(got, []string{"a"}) {
		t.Fatalf("string slice=%#v", got)
	}
	if got := validationStringList([]any{" valid ", "", nil}); !reflect.DeepEqual(got, []string{"valid"}) {
		t.Fatalf("any string slice=%#v", got)
	}
	if got := validationStringList(1); got != nil {
		t.Fatalf("invalid string list=%#v", got)
	}
}

func TestManifestStateMachineEffectContractRejectsUnsafeShapesAndAcceptsSelfPatches(t *testing.T) {
	code := func(transitions any) string {
		return manifestStateMachineEffectContractCode(definitionmodel.ValidationSchema{
			Config: map[string]any{"transitions": transitions},
		})
	}
	for _, transitions := range []any{
		[]any{map[string]any{"patch": "invalid"}},
		[]any{map[string]any{"self_patch": []any{"invalid"}}},
	} {
		if got := code(transitions); got != "backend.transition.self_patch_invalid" {
			t.Fatalf("patch code=%q", got)
		}
	}
	for _, transitions := range []any{
		[]any{map[string]any{"effects": []any{"notify"}}},
		[]any{map[string]any{"effects": []any{map[string]any{}}}},
		[]any{map[string]any{"effects": []any{map[string]any{"type": "notify"}}}},
	} {
		if got := code(transitions); got != "backend.transition.effect_requires_action" {
			t.Fatalf("effect code=%q", got)
		}
	}
	for _, transitions := range []any{
		[]any{map[string]any{"patch": map[string]any{"status": "done"}}},
		[]any{map[string]any{"effects": []any{map[string]any{"type": "patch_self"}}}},
		[]any{map[string]any{"effects": []any{map[string]any{"kind": "update_self"}}}},
		[]any{map[string]any{"effect": map[string]any{"type": "set_fields"}}},
	} {
		if got := code(transitions); got != "" {
			t.Fatalf("valid effect code=%q", got)
		}
	}
}

func TestManifestTransitionEffectsSupportsPluralStringAndSingularForms(t *testing.T) {
	for _, test := range []struct {
		transition map[string]any
		want       []any
	}{
		{transition: map[string]any{"effects": []any{map[string]any{"type": "patch_self"}}}, want: []any{map[string]any{"type": "patch_self"}}},
		{transition: map[string]any{"effects": []string{"patch_self", "set_fields"}}, want: []any{"patch_self", "set_fields"}},
		{transition: map[string]any{"effect": map[string]any{"type": "patch_self"}}, want: []any{map[string]any{"type": "patch_self"}}},
		{transition: map[string]any{"effects": "invalid"}},
	} {
		if got := manifestTransitionEffects(test.transition); !reflect.DeepEqual(got, test.want) {
			t.Fatalf("transition=%#v effects=%#v want=%#v", test.transition, got, test.want)
		}
	}
}
