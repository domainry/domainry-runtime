package action

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

type emptyActionCodedError struct{}

func (emptyActionCodedError) Error() string                  { return "empty coded error" }
func (emptyActionCodedError) ErrorCode() string              { return "" }
func (emptyActionCodedError) ErrorParams() map[string]string { return nil }

func TestActionNormalizePayloadWithoutContractClonesAndAppliesDefaults(t *testing.T) {
	action := definitionmodel.ActionSchema{
		Defaults: map[string]any{"status": "draft", "priority": "normal", "ignored_blank": "value", "": "ignored"},
	}
	input := map[string]any{"status": "provided", "custom": map[string]any{"ok": true}}
	got, err := ActionNormalizePayload(action, input)
	if err != nil {
		t.Fatal(err)
	}
	if got["status"] != "provided" || got["priority"] != "normal" || got["ignored_blank"] != "value" || got["custom"] == nil {
		t.Fatalf("normalized payload=%v", got)
	}
	got["status"] = "changed"
	if input["status"] != "provided" {
		t.Fatalf("normalization mutated input=%v", input)
	}
	empty, err := ActionNormalizePayload(definitionmodel.ActionSchema{}, nil)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("nil payload=%v error=%v", empty, err)
	}
}

func TestActionNormalizePayloadContractDefaultsExtrasAndValidation(t *testing.T) {
	action := definitionmodel.ActionSchema{
		Key: "order.submit",
		PayloadFields: []definitionmodel.ActionPayloadField{
			{Key: "amount", Type: "number", Required: true},
			{Key: "note", Type: "text", DefaultValue: "field-default"},
			{Key: "mode", Type: "select", Options: []string{"fast", "safe"}},
		},
		Defaults: map[string]any{"note": "action-default", "mode": "safe", "request_ref": "generated", "undeclared": "ignored", "also_undeclared": true},
	}
	input := map[string]any{
		"amount":   json.Number("12.5"),
		"note":     "",
		"approved": true,
	}
	got, err := ActionNormalizePayload(action, input)
	if err != nil {
		t.Fatal(err)
	}
	if got["amount"] != 12.5 || got["note"] != "action-default" || got["mode"] != "safe" || got["request_ref"] != "generated" {
		t.Fatalf("normalized declared/default payload=%#v", got)
	}
	for _, key := range []string{"approved"} {
		if got[key] != input[key] {
			t.Fatalf("extra key %q lost: %#v", key, got)
		}
	}
	if _, exists := got["undeclared"]; exists {
		t.Fatalf("undeclared default leaked into contract payload=%#v", got)
	}

	if _, err := ActionNormalizePayload(action, map[string]any{"amount": 1, "unknown": true}); apperror.CodeOf(err) != "backend.validation.unknown_field" || apperror.ParamsOf(err)["field"] != "unknown" {
		t.Fatalf("unknown field error=%v params=%v", err, apperror.ParamsOf(err))
	}
	if _, err := ActionNormalizePayload(action, map[string]any{"amount": 1, "idempotency_key": "technical"}); apperror.CodeOf(err) != "backend.validation.unknown_field" || apperror.ParamsOf(err)["field"] != "idempotency_key" {
		t.Fatalf("technical idempotency field error=%v params=%v", err, apperror.ParamsOf(err))
	}
	if _, err := ActionNormalizePayload(action, map[string]any{"mode": "invalid"}); apperror.KindOf(err) != apperror.KindBadRequest {
		t.Fatalf("invalid payload error=%v kind=%s", err, apperror.KindOf(err))
	}
	if _, err := ActionNormalizePayload(action, map[string]any{"amount": map[string]any{"invalid": true}}); apperror.KindOf(err) != apperror.KindBadRequest {
		t.Fatalf("normalization error=%v kind=%s", err, apperror.KindOf(err))
	}
}

func TestActionNormalizePayloadPreservesExactAndInexactNumericSemantics(t *testing.T) {
	action := definitionmodel.ActionSchema{
		Key: "settings.publish",
		PayloadFields: []definitionmodel.ActionPayloadField{
			{Key: "required_rate", Type: "percent", Required: true},
			{Key: "optional_amount", Type: "currency"},
			{Key: "inexact_score", Type: "number", Required: true},
		},
	}
	normalized, err := ActionNormalizePayload(action, map[string]any{
		"required_rate":   "7.5",
		"optional_amount": "12.3",
		"inexact_score":   json.Number("1.25"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if normalized["required_rate"] != "7.50" || normalized["optional_amount"] != "12.30" {
		t.Fatalf("exact values were not canonical strings: %#v", normalized)
	}
	if score, ok := normalized["inexact_score"].(float64); !ok || score != 1.25 {
		t.Fatalf("number did not remain inexact float64: %#v", normalized["inexact_score"])
	}
}

func TestActionNormalizePayloadRejectsCallerScalarCoercion(t *testing.T) {
	action := definitionmodel.ActionSchema{Key: "payroll.calculate", PayloadFields: []definitionmodel.ActionPayloadField{
		{Key: "window", Type: "integer", Required: true},
		{Key: "rate", Type: "number", Required: true},
		{Key: "amount", Type: "currency", Required: true},
		{Key: "enabled", Type: "boolean", Required: true},
		{Key: "note", Type: "text"},
	}}
	tests := []struct {
		name, field, code string
		value             any
	}{
		{name: "quoted integer", field: "window", value: "0", code: "backend.validation.integer"},
		{name: "quoted number", field: "rate", value: "0", code: "backend.validation.number"},
		{name: "numeric decimal", field: "amount", value: json.Number("0"), code: "backend.decimal.value_invalid"},
		{name: "quoted boolean", field: "enabled", value: "false", code: "backend.validation.boolean"},
		{name: "numeric boolean", field: "enabled", value: json.Number("1"), code: "backend.validation.boolean"},
		{name: "numeric text", field: "note", value: json.Number("1"), code: "backend.validation.string"},
	}
	valid := map[string]any{"window": json.Number("0"), "rate": json.Number("0"), "amount": "0", "enabled": false}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			input := actionCloneMap(valid)
			input[test.field] = test.value
			_, err := ActionNormalizePayload(action, input)
			if apperror.CodeOf(err) != test.code || apperror.ParamsOf(err)["field"] != test.field {
				t.Fatalf("error=%v params=%v", err, apperror.ParamsOf(err))
			}
		})
	}
	normalized, err := ActionNormalizePayload(action, valid)
	if err != nil || normalized["window"] != int64(0) || normalized["rate"] != float64(0) || normalized["amount"] != "0.00" || normalized["enabled"] != false {
		t.Fatalf("zero/false payload=%#v error=%v", normalized, err)
	}
}

func TestActionPayloadErrorMappingAndHelpers(t *testing.T) {
	existing := &apperror.AppError{Kind: apperror.KindConflict, Code: "backend.test.conflict"}
	if got := actionPayloadBadRequestFromError(existing); got != existing {
		t.Fatalf("existing AppError changed: %v", got)
	}
	coded := &apperror.CodedError{Code: "backend.test.invalid", Params: map[string]string{"field": "amount"}}
	got := actionPayloadBadRequestFromError(coded)
	if apperror.KindOf(got) != apperror.KindBadRequest || apperror.CodeOf(got) != coded.Code || apperror.ParamsOf(got)["field"] != "amount" || !errors.Is(got, coded) {
		t.Fatalf("coded mapping=%v params=%v", got, apperror.ParamsOf(got))
	}
	plain := errors.New("plain failure")
	got = actionPayloadBadRequestFromError(plain)
	if apperror.CodeOf(got) != "backend.bad_request" || !errors.Is(got, plain) {
		t.Fatalf("plain mapping=%v", got)
	}
	got = actionPayloadBadRequestFromError(emptyActionCodedError{})
	if apperror.CodeOf(got) != "backend.bad_request" {
		t.Fatalf("empty coded mapping=%v", got)
	}
	got = actionPayloadBadRequest("backend.test.invalid", nil, " ", "ignored", "field", " amount ", "odd")
	if apperror.ParamsOf(got)["field"] != " amount " || len(apperror.ParamsOf(got)) != 1 {
		t.Fatalf("bad request params=%v", apperror.ParamsOf(got))
	}
	if actionCloneMap(nil) != nil {
		t.Fatal("nil clone must remain nil")
	}
}
