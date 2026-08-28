package service

import (
	"reflect"
	"testing"
)

func TestActionServiceValueHelpersCoverNilTypedAndFallbackValues(t *testing.T) {
	if cloneMap(nil) != nil {
		t.Fatal("nil map clone was not nil")
	}
	source := map[string]any{"name": "Acme", "count": 2}
	cloned := cloneMap(source)
	source["name"] = "Changed"
	if cloned["name"] != "Acme" || cloned["count"] != 2 {
		t.Fatalf("clone=%#v", cloned)
	}

	if got := mapValue(cloned); !reflect.DeepEqual(got, cloned) {
		t.Fatalf("map value=%#v", got)
	}
	if got := mapValue("not-a-map"); got != nil {
		t.Fatalf("non-map value=%#v", got)
	}

	for _, tc := range []struct {
		name  string
		value any
		want  bool
	}{
		{name: "boolean true", value: true, want: true},
		{name: "boolean false", value: false},
		{name: "trimmed string true", value: " TRUE ", want: true},
		{name: "string false", value: "false"},
		{name: "unsupported", value: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := boolValue(tc.value); got != tc.want {
				t.Fatalf("got=%t want=%t", got, tc.want)
			}
		})
	}

	if got := firstNonNil(nil, nil, "value", "later"); got != "value" {
		t.Fatalf("first non-nil=%#v", got)
	}
	if got := firstNonNil(nil, nil); got != nil {
		t.Fatalf("all-nil=%#v", got)
	}

	if got := relatedMap(cloned); !reflect.DeepEqual(got, cloned) {
		t.Fatalf("related map=%#v", got)
	}
	if got := relatedMap([]any{}); got != nil {
		t.Fatalf("non-related map=%#v", got)
	}
}

func TestRelatedMapSliceCoversEveryInputShape(t *testing.T) {
	typed := []map[string]any{{"id": "one"}}
	cloned := relatedMapSlice(typed)
	typed[0] = map[string]any{"id": "changed"}
	if len(cloned) != 1 || cloned[0]["id"] != "one" {
		t.Fatalf("typed clone=%#v", cloned)
	}

	mixed := relatedMapSlice([]any{
		map[string]any{"id": "one"},
		map[string]any{},
		"ignored",
		map[string]any{"id": "two"},
	})
	if !reflect.DeepEqual(mixed, []map[string]any{{"id": "one"}, {"id": "two"}}) {
		t.Fatalf("mixed=%#v", mixed)
	}
	if got := relatedMapSlice("unsupported"); got != nil {
		t.Fatalf("unsupported=%#v", got)
	}
}
