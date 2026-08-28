package policy

import (
	"encoding/json"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestPipelineRuntimeActionAndExpectedVersionPolicy(t *testing.T) {
	for _, test := range []struct {
		action definitionmodel.ActionSchema
		want   bool
	}{{definitionmodel.ActionSchema{ObjectKey: "pipeline_item", Key: "pipeline_item.advance"}, true}, {definitionmodel.ActionSchema{ObjectKey: "pipeline_item", Key: "pipeline_item.reopen"}, true}, {definitionmodel.ActionSchema{ObjectKey: "pipeline_item", Key: "pipeline_item.other"}, false}, {definitionmodel.ActionSchema{ObjectKey: "other", Key: "pipeline_item.advance"}, false}} {
		if got := PipelineIsRuntimeAction(test.action); got != test.want {
			t.Fatalf("runtime action %#v = %v", test.action, got)
		}
	}
	for _, test := range []struct {
		name       string
		recordData map[string]any
		data       map[string]any
		code       string
	}{
		{name: "missing"},
		{name: "empty", data: map[string]any{"expected_version": ""}},
		{name: "invalid", data: map[string]any{"expected_version": "bad"}, code: "backend.pipeline.invalid_expected_version"},
		{name: "fallback current", recordData: map[string]any{"version": "bad"}, data: map[string]any{"version": 1}},
		{name: "equal", recordData: map[string]any{"version": 2}, data: map[string]any{"expected_version": 2}},
		{name: "conflict", recordData: map[string]any{"version": 2}, data: map[string]any{"expected_version": 1}, code: "backend.pipeline.version_conflict"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := apperror.CodeOf(PipelineValidateExpectedVersion(test.recordData, test.data)); test.code == "" && got != "backend.internal" || test.code != "" && got != test.code {
				t.Fatalf("code = %q, want %q", got, test.code)
			}
		})
	}
}

func TestPipelineActionExpectedVersionPolicyMatrix(t *testing.T) {
	versionedObject := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{Key: "name"}, {Key: "version"}}}
	required := definitionmodel.ActionSchema{OptimisticConcurrency: true}
	for _, test := range []struct {
		name       string
		action     definitionmodel.ActionSchema
		object     definitionmodel.ObjectSchema
		recordData map[string]any
		data       map[string]any
		code       string
	}{
		{name: "not required", object: versionedObject},
		{name: "no version field", action: required},
		{name: "missing", action: required, object: versionedObject, code: "backend.action.expected_version_required"},
		{name: "empty", action: required, object: versionedObject, data: map[string]any{"expected_version": ""}, code: "backend.action.expected_version_required"},
		{name: "invalid", action: required, object: versionedObject, data: map[string]any{"expected_version": "bad"}, code: "backend.action.invalid_expected_version"},
		{name: "fallback current", action: required, object: versionedObject, recordData: map[string]any{"version": "bad"}, data: map[string]any{"expectedVersion": 1}},
		{name: "equal", action: required, object: versionedObject, recordData: map[string]any{"version": 2}, data: map[string]any{"expected_version": 2}},
		{name: "conflict", action: required, object: versionedObject, recordData: map[string]any{"version": 2}, data: map[string]any{"expected_version": 1}, code: "backend.action.version_conflict"},
	} {
		t.Run(test.name, func(t *testing.T) {
			err := PipelineValidateActionExpectedVersion(test.action, test.object, test.recordData, test.data)
			if test.code == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if got := apperror.CodeOf(err); got != test.code {
				t.Fatalf("code = %q, want %q", got, test.code)
			}
		})
	}
}

func TestPipelinePolicyValueHelpers(t *testing.T) {
	for _, test := range []struct {
		action definitionmodel.ActionSchema
		want   bool
	}{{definitionmodel.ActionSchema{OptimisticConcurrency: true}, true}, {definitionmodel.ActionSchema{ConcurrencyField: " version "}, true}, {definitionmodel.ActionSchema{}, false}} {
		if got := PipelineActionRequiresExpectedVersion(test.action); got != test.want {
			t.Fatalf("required %#v = %v", test.action, got)
		}
	}
	object := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{Key: "one"}, {Key: "two"}}}
	if !PipelineObjectHasField(object, "two") || PipelineObjectHasField(object, "missing") {
		t.Fatal("object field lookup mismatch")
	}
	if value, ok := firstPresent(map[string]any{"second": 2}, "first", "second"); !ok || value != 2 {
		t.Fatalf("first present = %#v/%v", value, ok)
	}
	if value, ok := firstPresent(nil, "missing"); ok || value != nil {
		t.Fatalf("missing first present = %#v/%v", value, ok)
	}
	for _, test := range []struct {
		value any
		want  int
		ok    bool
	}{{int(1), 1, true}, {int64(2), 2, true}, {float64(3.9), 3, true}, {json.Number("4"), 4, true}, {json.Number("bad"), 0, false}, {" 5 ", 5, true}, {"bad", 0, false}, {true, 0, false}} {
		got, ok := PipelineIntValue(test.value)
		if got != test.want || ok != test.ok {
			t.Fatalf("int %#v = %d/%v", test.value, got, ok)
		}
	}
	for _, test := range []struct {
		value any
		want  bool
		ok    bool
	}{{true, true, true}, {false, false, true}, {" true ", true, true}, {"bad", false, false}, {1, false, false}} {
		got, ok := boolValue(test.value)
		if got != test.want || ok != test.ok {
			t.Fatalf("bool %#v = %v/%v", test.value, got, ok)
		}
	}
	if err := pipelinePolicyError(apperror.KindBadRequest, "code", nil); apperror.CodeOf(err) != "code" {
		t.Fatalf("policy error = %v", err)
	}
}
