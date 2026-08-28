package binding

import (
	"reflect"
	"testing"
)

func TestEnvironmentIntersectionAndTypesFailClosed(t *testing.T) {
	left := NewEnvironment(Fact{Reference: "$workflow.value", Type: TypeInteger, Producer: "left"}, Fact{Reference: "$workflow.shared", Type: TypeText, Values: []string{"approved"}})
	right := NewEnvironment(Fact{Reference: "$workflow.value", Type: TypeText, Producer: "right"}, Fact{Reference: "$workflow.shared", Type: TypeText, Values: []string{"rejected"}})
	joined := Intersect(left, right)
	if _, exists := joined["$workflow.value"]; exists {
		t.Fatal("conflicting producer types survived a graph join")
	}
	if joined["$workflow.shared"].Type != TypeText {
		t.Fatalf("shared fact missing: %#v", joined)
	}
	if !reflect.DeepEqual(joined["$workflow.shared"].Values, []string{"approved", "rejected"}) {
		t.Fatalf("joined finite values=%#v", joined["$workflow.shared"].Values)
	}
	if !Compatible(TypeInteger, TypeNumber) || Compatible(TypeText, TypeUser) || Compatible(TypeDateTime, TypeDate) || Compatible(TypeUnknown, TypeText) || !Compatible(TypeText, TypeUnknown) {
		t.Fatal("lossless compatibility contract drifted")
	}
}

func TestWalkAndValueTypeCoverNestedTemplates(t *testing.T) {
	value := map[string]any{"nested": []any{"prefix $workflow.name", "$workflow.count", "$workflow.missing"}}
	occurrences := []Occurrence{}
	Walk(value, "/input", func(occurrence Occurrence) { occurrences = append(occurrences, occurrence) })
	want := []Occurrence{
		{Reference: "$workflow.name", Path: "/input/nested/0", Exact: false},
		{Reference: "$workflow.count", Path: "/input/nested/1", Exact: true},
		{Reference: "$workflow.missing", Path: "/input/nested/2", Exact: true},
	}
	if !reflect.DeepEqual(occurrences, want) {
		t.Fatalf("occurrences=%#v want=%#v", occurrences, want)
	}
	environment := NewEnvironment(Fact{Reference: "$workflow.name", Type: TypeText}, Fact{Reference: "$workflow.count", Type: TypeInteger})
	if valueType, ok := ValueTypeOf("prefix $workflow.name", environment); !ok || valueType != TypeText {
		t.Fatalf("template type=%s ok=%v", valueType, ok)
	}
	if _, ok := ValueTypeOf("$workflow.missing", environment); ok {
		t.Fatal("unknown exact reference resolved")
	}
}
