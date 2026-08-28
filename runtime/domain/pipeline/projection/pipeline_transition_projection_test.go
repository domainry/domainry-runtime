package projection

import (
	"reflect"
	"strings"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestPipelineTransitionProjections(t *testing.T) {
	object := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{Key: "same"}, {Key: "changed"}, {Key: "added"}}}
	before := map[string]any{"same": map[string]any{"a": 1}, "changed": 1}
	after := map[string]any{"same": map[string]any{"a": 1}, "changed": 2, "added": "x"}
	encoded := PipelineTransitionFieldChangesJSON(object, before, after)
	if !strings.Contains(encoded, `"field":"changed"`) || !strings.Contains(encoded, `"field":"added"`) {
		t.Fatalf("changes JSON = %q", encoded)
	}
	if got := PipelineTransitionChangedPatch(object, before, after); !reflect.DeepEqual(got, map[string]any{"changed": 2, "added": "x"}) {
		t.Fatalf("patch = %#v", got)
	}
	if PipelineTransitionFieldChangesJSON(object, before, before) != "" || len(PipelineTransitionChangedPatch(object, before, before)) != 0 {
		t.Fatal("equivalent values reported change")
	}
}

func TestPipelineTransitionJSONFallbacks(t *testing.T) {
	shared := make(chan int)
	if !PipelineTransitionValuesEqual(shared, shared) {
		t.Fatal("same unencodable value should compare equal by fallback")
	}
	if PipelineTransitionValuesEqual(make(chan int), make(chan int)) {
		t.Fatal("different unencodable values should not compare equal")
	}
	if PipelineTransitionValuesEqual(make(chan int), "value") {
		t.Fatal("one-sided marshal error should not compare equal")
	}
	if PipelineTransitionValuesEqual("value", make(chan int)) {
		t.Fatal("right-sided marshal error should not compare equal")
	}
	object := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{Key: "channel"}}}
	if got := PipelineTransitionFieldChangesJSON(object, map[string]any{"channel": make(chan int)}, map[string]any{"channel": make(chan int)}); got != "" {
		t.Fatalf("unencodable changes JSON = %q", got)
	}
}
