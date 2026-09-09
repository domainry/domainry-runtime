package action

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	apperror "github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

// TestActionNormalizePayloadLegacyGolden pins the flat scalar path byte-for-byte
// for several legacy Actions: the structured branch must never change what a
// scalar-only contract produces.
func TestActionNormalizePayloadLegacyGolden(t *testing.T) {
	cases := []struct {
		name   string
		action definitionmodel.ActionSchema
		input  map[string]any
		golden string
	}{
		{
			name:   "no contract passes through with defaults",
			action: definitionmodel.ActionSchema{Key: "lead.qualify", Defaults: map[string]any{"channel": "web"}},
			input:  map[string]any{"anything": 1, "expected_version": 3},
			golden: `{"anything":1,"channel":"web","expected_version":3}`,
		},
		{
			name: "typed scalars with defaults and extras",
			action: definitionmodel.ActionSchema{Key: "opportunity.advance_stage", Defaults: map[string]any{"note": "auto"}, PayloadFields: []definitionmodel.ActionPayloadField{
				{Key: "stage", Type: "select", Required: true, Options: []string{"won", "lost"}},
				{Key: "amount", Type: "number"},
				{Key: "count", Type: "integer", DefaultValue: 2},
				{Key: "note", Type: "text"},
				{Key: "flag", Type: "boolean"},
			}},
			input:  map[string]any{"stage": "won", "amount": "12.5", "flag": "true", "expected_version": 7, "record_id": "r1"},
			golden: `{"amount":12.5,"count":2,"expected_version":7,"flag":true,"note":"auto","record_id":"r1","stage":"won"}`,
		},
		{
			name:   "empty contract accepts only extras",
			action: definitionmodel.ActionSchema{Key: "payment.mark_collected", PayloadFields: []definitionmodel.ActionPayloadField{}},
			input:  map[string]any{"approved": true},
			golden: `{"approved":true}`,
		},
		{
			name: "date and datetime strings are kept verbatim",
			action: definitionmodel.ActionSchema{Key: "contract.sign", PayloadFields: []definitionmodel.ActionPayloadField{
				{Key: "signed_on", Type: "date"}, {Key: "signed_at", Type: "datetime"}, {Key: "empty", Type: "text"},
			}},
			input:  map[string]any{"signed_on": "2026-09-09", "signed_at": "2026-09-09T10:00:00Z", "empty": ""},
			golden: `{"empty":"","signed_at":"2026-09-09T10:00:00Z","signed_on":"2026-09-09"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if definitionmodel.ActionPayloadFieldIsStructured(tc.action) {
				t.Fatal("legacy action must not take the structured path")
			}
			normalized, err := ActionNormalizePayload(tc.action, tc.input)
			if err != nil {
				t.Fatalf("normalize: %v", err)
			}
			encoded, _ := json.Marshal(normalized)
			if string(encoded) != tc.golden {
				t.Fatalf("golden mismatch:\n got %s\nwant %s", encoded, tc.golden)
			}
		})
	}
	legacyErrors := []struct {
		name   string
		action definitionmodel.ActionSchema
		input  map[string]any
		code   string
		field  string
	}{
		{"unknown key", definitionmodel.ActionSchema{Key: "a", PayloadFields: []definitionmodel.ActionPayloadField{{Key: "x"}}}, map[string]any{"y": 1}, "backend.validation.unknown_field", "y"},
		{"required", definitionmodel.ActionSchema{Key: "a", PayloadFields: []definitionmodel.ActionPayloadField{{Key: "x", Required: true}}}, map[string]any{}, "backend.validation.required", "x"},
		{"map rejected by scalar leaf", definitionmodel.ActionSchema{Key: "a", PayloadFields: []definitionmodel.ActionPayloadField{{Key: "x"}}}, map[string]any{"x": map[string]any{}}, "backend.validation.string", "x"},
	}
	for _, tc := range legacyErrors {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ActionNormalizePayload(tc.action, tc.input)
			var appErr *apperror.AppError
			if !errors.As(err, &appErr) || appErr.Kind != apperror.KindBadRequest || appErr.Code != tc.code || appErr.Params["field"] != tc.field {
				t.Fatalf("legacy error=%#v", err)
			}
		})
	}
}

func structuredWiringAction() definitionmodel.ActionSchema {
	return definitionmodel.ActionSchema{Key: "customer.register_accounts", Defaults: map[string]any{"name": "Default Co"}, PayloadFields: []definitionmodel.ActionPayloadField{
		{Key: "name", Type: "text", Required: true},
		{Key: "accounts", Type: "object", Repeated: true, Required: true, Fields: []definitionmodel.ActionPayloadField{
			{Key: "bank", Type: "text", Required: true}, {Key: "number", Type: "text", Required: true}, {Key: "primary", Type: "boolean"},
		}},
		{Key: "steps", Type: "object", Repeated: true, Fields: []definitionmodel.ActionPayloadField{
			{Key: "title", Type: "text", Required: true}, {Key: "required_approvals", Type: "integer"}, {Key: "assignees", Type: "user", Repeated: true, Required: true},
		}},
	}}
}

func TestActionNormalizePayloadStructuredHonorsExtrasAndTopLevelDefaults(t *testing.T) {
	action := structuredWiringAction()
	normalized, err := ActionNormalizePayload(action, map[string]any{
		"accounts":         []any{map[string]any{"bank": "First", "number": "1", "primary": "true"}},
		"expected_version": 4,
		"request_ref":      "ref-1",
	})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	want := map[string]any{
		"name":             "Default Co",
		"accounts":         []any{map[string]any{"bank": "First", "number": "1", "primary": true}},
		"expected_version": 4,
		"request_ref":      "ref-1",
	}
	if !reflect.DeepEqual(normalized, want) {
		t.Fatalf("normalized=%#v want=%#v", normalized, want)
	}
	input := businessHandlerInput(action, normalized)
	if _, leaked := input["expected_version"]; leaked || !reflect.DeepEqual(input["accounts"], want["accounts"]) {
		t.Fatalf("handler input=%#v", input)
	}
	raw, err := json.Marshal(input)
	if err != nil || string(raw) != `{"accounts":[{"bank":"First","number":"1","primary":true}],"name":"Default Co"}` {
		t.Fatalf("handler input json=%s err=%v", raw, err)
	}
}

func TestActionNormalizePayloadStructuredErrorsAreBadRequestsWithFieldPaths(t *testing.T) {
	action := structuredWiringAction()
	cases := []struct {
		name  string
		input map[string]any
		code  string
		field string
	}{
		{"top-level unknown", map[string]any{"name": "a", "accounts": []any{map[string]any{"bank": "b", "number": "1"}}, "nickname": "x"}, "backend.validation.unknown_field", "nickname"},
		{"nested unknown", map[string]any{"name": "a", "accounts": []any{map[string]any{"bank": "b", "number": "1", "nickname": "x"}}}, "backend.validation.unknown_field", "accounts[0].nickname"},
		{"array expected", map[string]any{"name": "a", "accounts": "x"}, "backend.validation.array_expected", "accounts"},
		{"nested required array", map[string]any{"name": "a", "accounts": []any{map[string]any{"bank": "b", "number": "1"}}, "steps": []any{map[string]any{"title": "t", "assignees": []any{"u"}}, map[string]any{"title": "t2"}}}, "backend.validation.required", "steps[1].assignees"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ActionNormalizePayload(action, tc.input)
			var appErr *apperror.AppError
			if !errors.As(err, &appErr) || appErr.Kind != apperror.KindBadRequest || appErr.Code != tc.code || appErr.Params["field"] != tc.field || appErr.Params["object"] != action.Key {
				t.Fatalf("structured error=%#v", err)
			}
		})
	}
}
