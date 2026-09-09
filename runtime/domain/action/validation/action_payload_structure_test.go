package validation

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	apperror "github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func structuredFixtureAction() definitionmodel.ActionSchema {
	two := 2
	return definitionmodel.ActionSchema{Key: "customer.register_accounts", Label: "Register", PayloadFields: []definitionmodel.ActionPayloadField{
		{Key: "name", Type: "text", Required: true},
		{Key: "accounts", Type: "object", Repeated: true, Required: true, MaxItems: &two, Fields: []definitionmodel.ActionPayloadField{
			{Key: "bank", Type: "text", Required: true},
			{Key: "number", Type: "text", Required: true},
			{Key: "primary", Type: "boolean"},
		}},
		{Key: "steps", Type: "object", Repeated: true, Fields: []definitionmodel.ActionPayloadField{
			{Key: "title", Type: "text", Required: true},
			{Key: "required_approvals", Type: "integer", DefaultValue: 1},
			{Key: "assignees", Type: "user", Repeated: true, Required: true},
		}},
		{Key: "address", Type: "object", Fields: []definitionmodel.ActionPayloadField{
			{Key: "city", Type: "text", Required: true},
			{Key: "kind", Type: "select", Options: []string{"home", "office"}},
		}},
	}}
}

func structuredFailure(t *testing.T, err error) (string, map[string]string) {
	t.Helper()
	var coded *apperror.CodedError
	if !errors.As(err, &coded) {
		t.Fatalf("expected coded error, got %#v", err)
	}
	return coded.Code, coded.Params
}

func TestStructuredPayloadNormalizesNestedValuesAndOmitsOptionalContainers(t *testing.T) {
	action := structuredFixtureAction()
	normalized, err := ActionNormalizeStructuredPayload(action, map[string]any{
		"name":     "Acme",
		"accounts": []any{map[string]any{"bank": "First", "number": "1", "primary": true}},
		"steps":    []any{map[string]any{"title": "Review", "assignees": []any{"user-1", "user-2"}}},
	})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	want := map[string]any{
		"name":     "Acme",
		"accounts": []any{map[string]any{"bank": "First", "number": "1", "primary": true}},
		"steps":    []any{map[string]any{"title": "Review", "required_approvals": int64(1), "assignees": []any{"user-1", "user-2"}}},
	}
	if !reflect.DeepEqual(normalized, want) {
		t.Fatalf("normalized=%#v want=%#v", normalized, want)
	}
	if _, present := normalized["address"]; present {
		t.Fatal("optional missing object must be omitted")
	}
	normalized, err = ActionNormalizeStructuredPayload(action, map[string]any{
		"name": "Acme", "accounts": []any{map[string]any{"bank": "b", "number": "n"}}, "steps": []any{}, "address": nil,
	})
	if err != nil {
		t.Fatalf("normalize empty optional array: %v", err)
	}
	if steps, ok := normalized["steps"].([]any); !ok || len(steps) != 0 {
		t.Fatalf("optional empty array must be kept: %#v", normalized)
	}
	if _, present := normalized["address"]; present {
		t.Fatal("optional null object must be omitted")
	}
}

func TestStructuredPayloadErrorMatrixCarriesFullPaths(t *testing.T) {
	action := structuredFixtureAction()
	valid := func() map[string]any {
		return map[string]any{"name": "Acme", "accounts": []any{map[string]any{"bank": "First", "number": "1"}}}
	}
	cases := []struct {
		name   string
		mutate func(map[string]any)
		code   string
		field  string
		key    string
		params map[string]string
	}{
		{name: "required object array missing", mutate: func(m map[string]any) { delete(m, "accounts") }, code: "backend.validation.required", field: "accounts", key: "accounts"},
		{name: "required array empty", mutate: func(m map[string]any) { m["accounts"] = []any{} }, code: "backend.validation.required", field: "accounts", key: "accounts"},
		{name: "array expected", mutate: func(m map[string]any) { m["accounts"] = "x" }, code: "backend.validation.array_expected", field: "accounts", key: "accounts"},
		{name: "object expected in array", mutate: func(m map[string]any) { m["accounts"] = []any{"x"} }, code: "backend.validation.object_expected", field: "accounts[0]", key: "accounts"},
		{name: "object expected", mutate: func(m map[string]any) { m["address"] = []any{} }, code: "backend.validation.object_expected", field: "address", key: "address"},
		{name: "max items", mutate: func(m map[string]any) {
			m["accounts"] = []any{map[string]any{"bank": "a", "number": "1"}, map[string]any{"bank": "b", "number": "2"}, map[string]any{"bank": "c", "number": "3"}}
		}, code: "backend.validation.max_items", field: "accounts", key: "accounts", params: map[string]string{"limit": "2", "actual": "3"}},
		{name: "nested unknown key", mutate: func(m map[string]any) {
			m["accounts"] = []any{map[string]any{"bank": "a", "number": "1", "nickname": "x"}}
		}, code: "backend.validation.unknown_field", field: "accounts[0].nickname", key: "nickname"},
		{name: "nested required leaf", mutate: func(m map[string]any) { m["accounts"] = []any{map[string]any{"bank": "a"}} }, code: "backend.validation.required", field: "accounts[0].number", key: "number"},
		{name: "nested required array in second item", mutate: func(m map[string]any) {
			m["steps"] = []any{map[string]any{"title": "a", "assignees": []any{"u"}}, map[string]any{"title": "b"}}
		}, code: "backend.validation.required", field: "steps[1].assignees", key: "assignees"},
		{name: "leaf type error keeps code", mutate: func(m map[string]any) {
			m["accounts"] = []any{map[string]any{"bank": "a", "number": "1", "primary": map[string]any{}}}
		}, code: "backend.validation.boolean", field: "accounts[0].primary", key: "primary"},
		{name: "leaf option error keeps params", mutate: func(m map[string]any) { m["address"] = map[string]any{"city": "Bonn", "kind": "farm"} }, code: "backend.validation.invalid_option", field: "address.kind", key: "kind", params: map[string]string{"options": "home, office"}},
		{name: "scalar array item null", mutate: func(m map[string]any) { m["steps"] = []any{map[string]any{"title": "a", "assignees": []any{nil}}} }, code: "backend.validation.required", field: "steps[0].assignees[0]", key: "assignees"},
		{name: "scalar array item wrong type", mutate: func(m map[string]any) { m["steps"] = []any{map[string]any{"title": "a", "assignees": []any{7}}} }, code: "backend.validation.string", field: "steps[0].assignees[0]", key: "assignees"},
		{name: "top-level unknown", mutate: func(m map[string]any) { m["extra"] = 1 }, code: "backend.validation.unknown_field", field: "extra", key: "extra"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			payload := valid()
			tc.mutate(payload)
			_, err := ActionNormalizeStructuredPayload(action, payload)
			code, params := structuredFailure(t, err)
			if code != tc.code || params["field"] != tc.field || params["field_key"] != tc.key || params["object"] != "customer.register_accounts" {
				t.Fatalf("code=%s params=%#v want code=%s field=%s key=%s", code, params, tc.code, tc.field, tc.key)
			}
			for key, value := range tc.params {
				if params[key] != value {
					t.Fatalf("param %s=%q want %q (params=%#v)", key, params[key], value, params)
				}
			}
		})
	}
}

func TestStructuredPayloadMinItemsAndRequiredImplyFloor(t *testing.T) {
	three := 3
	action := definitionmodel.ActionSchema{Key: "a", PayloadFields: []definitionmodel.ActionPayloadField{
		{Key: "tags", Type: "text", Repeated: true, MinItems: &three},
		{Key: "codes", Type: "text", Repeated: true, Required: true},
	}}
	_, err := ActionNormalizeStructuredPayload(action, map[string]any{"tags": []any{"a"}, "codes": []any{"x"}})
	code, params := structuredFailure(t, err)
	if code != "backend.validation.min_items" || params["limit"] != "3" || params["actual"] != "1" || params["field"] != "tags" {
		t.Fatalf("min items code=%s params=%#v", code, params)
	}
	if _, err := ActionNormalizeStructuredPayload(action, map[string]any{"codes": []any{}}); err == nil {
		t.Fatal("required array with zero items must fail")
	}
	normalized, err := ActionNormalizeStructuredPayload(action, map[string]any{"codes": []string{"x"}})
	if err != nil || !reflect.DeepEqual(normalized["codes"], []any{"x"}) {
		t.Fatalf("typed string slice normalized=%#v err=%v", normalized, err)
	}
}

func TestActionPayloadFieldIsStructuredDetectsAnyLevel(t *testing.T) {
	if definitionmodel.ActionPayloadFieldIsStructured(definitionmodel.ActionSchema{PayloadFields: []definitionmodel.ActionPayloadField{{Key: "a", Type: "text"}}}) {
		t.Fatal("scalar-only action must not be structured")
	}
	if !definitionmodel.ActionPayloadFieldIsStructured(definitionmodel.ActionSchema{PayloadFields: []definitionmodel.ActionPayloadField{{Key: "a", Type: "text", Repeated: true}}}) {
		t.Fatal("repeated scalar is structured")
	}
	if !definitionmodel.ActionPayloadFieldIsStructured(structuredFixtureAction()) {
		t.Fatal("nested object is structured")
	}
}

func TestActionPayloadFieldStructureDefinitionMatrix(t *testing.T) {
	objects := []definitionmodel.ObjectSchema{{Key: "customer", Fields: []definitionmodel.FieldSchema{{Key: "name", Type: "text"}}}}
	if issues := ActionValidatePayloadFieldStructure(structuredFixtureAction(), objects); len(issues) != 0 {
		t.Fatalf("valid structured action issues=%#v", issues)
	}
	negative, zero, big := -1, 0, definitionmodel.ActionPayloadMaxItems+1
	deep := definitionmodel.ActionPayloadField{Key: "l1", Type: "object", Fields: []definitionmodel.ActionPayloadField{{Key: "l2", Type: "object", Fields: []definitionmodel.ActionPayloadField{{Key: "l3", Type: "object", Fields: []definitionmodel.ActionPayloadField{{Key: "l4", Type: "object", Fields: []definitionmodel.ActionPayloadField{{Key: "leaf", Type: "text"}}}}}}}}}
	cases := []struct {
		name   string
		fields []definitionmodel.ActionPayloadField
		path   string
		reason string
	}{
		{"object without fields", []definitionmodel.ActionPayloadField{{Key: "a", Type: "object"}}, "payload_fields[0].fields", "at least one nested field"},
		{"scalar with fields", []definitionmodel.ActionPayloadField{{Key: "a", Type: "text", Fields: []definitionmodel.ActionPayloadField{{Key: "b"}}}}, "payload_fields[0].fields", "require type object"},
		{"duplicate nested key", []definitionmodel.ActionPayloadField{{Key: "a", Type: "object", Fields: []definitionmodel.ActionPayloadField{{Key: "b"}, {Key: "b"}}}}, "payload_fields[0].fields[1].key", "duplicate key"},
		{"invalid identifier", []definitionmodel.ActionPayloadField{{Key: "1-bad"}}, "payload_fields[0].key", "identifier"},
		{"depth", []definitionmodel.ActionPayloadField{deep}, "payload_fields[0].fields[0].fields[0].fields[0].fields", "maximum depth"},
		{"min items without repeated", []definitionmodel.ActionPayloadField{{Key: "a", MinItems: &zero}}, "payload_fields[0].min_items", "require repeated"},
		{"negative min", []definitionmodel.ActionPayloadField{{Key: "a", Repeated: true, MinItems: &negative}}, "payload_fields[0].min_items", "negative"},
		{"max over ceiling", []definitionmodel.ActionPayloadField{{Key: "a", Repeated: true, MaxItems: &big}}, "payload_fields[0].max_items", "must not exceed"},
		{"min over max", []definitionmodel.ActionPayloadField{{Key: "a", Repeated: true, MinItems: &big, MaxItems: &zero}}, "payload_fields[0].min_items", "must not exceed max_items"},
		{"options on text", []definitionmodel.ActionPayloadField{{Key: "a", Type: "text", Options: []string{"x"}}}, "payload_fields[0].options", "select"},
		{"relation without target", []definitionmodel.ActionPayloadField{{Key: "a", Type: "object", Fields: []definitionmodel.ActionPayloadField{{Key: "r", Type: "relation"}}}}, "payload_fields[0].fields[0].target_object_key", "target_object_key"},
		{"unknown type", []definitionmodel.ActionPayloadField{{Key: "a", Type: "blob"}}, "payload_fields[0].type", "unknown payload field type"},
		{"lineage pair", []definitionmodel.ActionPayloadField{{Key: "a", SourceObjectKey: "customer"}}, "payload_fields[0].source_object_key", "declared together"},
		{"lineage unknown field", []definitionmodel.ActionPayloadField{{Key: "a", Type: "object", Fields: []definitionmodel.ActionPayloadField{{Key: "n", SourceObjectKey: "customer", SourceFieldKey: "missing"}}}}, "payload_fields[0].fields[0].source_field_key", "unknown field"},
		{"lineage unknown object", []definitionmodel.ActionPayloadField{{Key: "a", SourceObjectKey: "ghost", SourceFieldKey: "name"}}, "payload_fields[0].source_object_key", "unknown object"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			issues := ActionValidatePayloadFieldStructure(definitionmodel.ActionSchema{Key: "a", PayloadFields: tc.fields}, objects)
			found := false
			for _, issue := range issues {
				if issue.Path == tc.path && strings.Contains(issue.Reason, tc.reason) {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing issue path=%s reason~%q in %#v", tc.path, tc.reason, issues)
			}
		})
	}
	wide := make([]definitionmodel.ActionPayloadField, 0, 201)
	for index := 0; index < definitionmodel.ActionPayloadMaxLeafFields+1; index++ {
		wide = append(wide, definitionmodel.ActionPayloadField{Key: fmt.Sprintf("f%d", index)})
	}
	issues := ActionValidatePayloadFieldStructure(definitionmodel.ActionSchema{Key: "a", PayloadFields: wide}, nil)
	if len(issues) != 1 || issues[0].Path != "payload_fields" || !strings.Contains(issues[0].Reason, "leaf fields") {
		t.Fatalf("leaf ceiling issues=%#v", issues)
	}
	published := ActionValidateDefinitionIssuesWithObjects(definitionmodel.ActionSchema{Key: "customer.a", ObjectKey: "customer", Kind: definitionmodel.ActionKindObjectOperation, PayloadFields: []definitionmodel.ActionPayloadField{{Key: "a", Type: "object"}}}, objects)
	if len(published) != 1 || published[0].ErrorCode != ActionPayloadFieldInvalidCode || published[0].FieldPath != "payload_fields[0].fields" || published[0].Params["action"] != "customer.a" || published[0].Params["path"] != "payload_fields[0].fields" || published[0].Params["reason"] == "" {
		t.Fatalf("definition issues=%#v", published)
	}
}
