package validation

import (
	"testing"
	"time"

	apperror "github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func TestValidationSegmentBasicHelpers(t *testing.T) {
	object := definitionmodel.ObjectSchema{Fields: []definitionmodel.FieldSchema{{Key: "status", Type: "select"}}}
	if field, ok := findFieldSchema(object, "status"); !ok || field.Key != "status" {
		t.Fatal("field not found")
	}
	if _, ok := findFieldSchema(object, "missing"); ok {
		t.Fatal("missing field found")
	}
	if !stateMachineAllowsTransition(nil, "draft", "active") {
		t.Fatal("nil transition config should allow")
	}
	transitions := []any{"ignored", map[string]any{"from": "draft", "to": []any{"active", "closed"}}, map[string]any{"from": "other", "to": []string{"x"}}}
	if !stateMachineAllowsTransition(transitions, "draft", "active") || stateMachineAllowsTransition(transitions, "draft", "missing") {
		t.Fatal("transition matching mismatch")
	}
	if isBlockingValidation(definitionmodel.ValidationSchema{Severity: "warning"}) || isBlockingValidation(definitionmodel.ValidationSchema{Severity: "INFO"}) || !isBlockingValidation(definitionmodel.ValidationSchema{}) {
		t.Fatal("blocking severity mismatch")
	}
}

func TestValidationConditionsAndValues(t *testing.T) {
	if !validationConditionApplies(definitionmodel.ValidationSchema{}, nil) {
		t.Fatal("unconditional validation did not apply")
	}
	base := definitionmodel.ValidationSchema{Config: map[string]any{"when_field": "status"}}
	if validationConditionApplies(base, nil) {
		t.Fatal("missing condition field applied")
	}
	base.Config["when_in"] = []string{"active"}
	if !validationConditionApplies(base, map[string]any{"status": "active"}) || validationConditionApplies(base, map[string]any{"status": "closed"}) {
		t.Fatal("when_in condition mismatch")
	}
	base.Config = map[string]any{"when_field": "status", "values": []any{"open"}}
	if !validationConditionApplies(base, map[string]any{"status": "open"}) {
		t.Fatal("values condition mismatch")
	}
	base.Config = map[string]any{"when_field": "status", "value": "open"}
	if !validationConditionApplies(base, map[string]any{"status": "open"}) || validationConditionApplies(base, map[string]any{"status": "closed"}) {
		t.Fatal("value condition mismatch")
	}
	base.Config = map[string]any{"when_field": "status"}
	if !validationConditionApplies(base, map[string]any{"status": "anything"}) {
		t.Fatal("presence condition mismatch")
	}
	if validationValueMatches(1, nil) || validationValueMatches(0, map[string]any{"nonzero": true}) || !validationValueMatches(2, map[string]any{"nonzero": true}) {
		t.Fatal("nonzero matching mismatch")
	}
	if !validationValueMatches("active", map[string]any{"value": "active"}) || validationValueMatches("active", map[string]any{"value": ""}) {
		t.Fatal("value matching mismatch")
	}
	if !validationValueMatches("active", map[string]any{"values": []string{"active"}}) || validationValueMatches("missing", map[string]any{"values": []string{"active"}}) {
		t.Fatal("values matching mismatch")
	}
}

func TestJSONAndScheduleValidationHelpers(t *testing.T) {
	for _, test := range []struct {
		value any
		want  int
		code  string
	}{
		{nil, 0, ""}, {[]any{"a", "b"}, 2, ""}, {[]string{"a"}, 1, ""}, {[]map[string]any{{"a": 1}}, 1, ""},
		{`["a","b"]`, 2, ""}, {`bad`, 0, "json"}, {1, 0, "backend.validation.json_array"},
	} {
		items, err := jsonArrayAny(test.value)
		if test.code == "json" {
			if err == nil {
				t.Fatal("invalid JSON accepted")
			}
			continue
		}
		assertValidationCode(t, err, test.code)
		if err == nil && len(items) != test.want {
			t.Errorf("jsonArrayAny(%#v) len=%d want=%d", test.value, len(items), test.want)
		}
	}
	if values, err := jsonStringArray([]any{" a ", "b"}); err != nil || len(values) != 2 || values[0] != "a" {
		t.Fatalf("string array=%#v err=%v", values, err)
	}
	assertValidationCode(t, errorFromJSONStrings([]any{1}), "backend.validation.array_item_string")
	assertValidationCode(t, errorFromJSONStrings([]any{" "}), "backend.validation.array_item_required")

	validSegments := []any{map[string]any{"opens_at": "09:00", "closes_at": "12:00"}, map[string]any{"opens_at": "13:00", "closes_at": "17:00"}}
	assertValidationCode(t, validateBusinessHourSegmentsValue(validSegments), "")
	for _, test := range []struct {
		value any
		code  string
	}{
		{[]any{"bad"}, "backend.validation.business_hours_object"},
		{[]any{map[string]any{}}, "backend.validation.business_hours_required"},
		{[]any{map[string]any{"opens_at": "9", "closes_at": "10:00"}}, "backend.validation.business_hours_open_format"},
		{[]any{map[string]any{"opens_at": "09:00", "closes_at": "bad"}}, "backend.validation.business_hours_close_format"},
		{[]any{map[string]any{"opens_at": "10:00", "closes_at": "09:00"}}, "backend.validation.business_hours_close_after_open"},
		{[]any{map[string]any{"opens_at": "09:00", "closes_at": "12:00"}, map[string]any{"opens_at": "11:00", "closes_at": "13:00"}}, "backend.validation.business_hours_overlap"},
	} {
		assertValidationCode(t, validateBusinessHourSegmentsValue(test.value), test.code)
	}
	validDates := []any{map[string]any{"date": "2026-07-18", "segments": validSegments}}
	assertValidationCode(t, validateSpecialDatesScheduleValue(validDates), "")
	for _, test := range []struct {
		value any
		code  string
	}{
		{[]any{"bad"}, "backend.validation.special_date_object"},
		{[]any{map[string]any{}}, "backend.validation.special_date_required"},
		{[]any{map[string]any{"date": "18/07/2026"}}, "backend.validation.special_date_format"},
		{[]any{map[string]any{"date": "2026-07-18", "segments": []any{map[string]any{}}}}, "backend.validation.business_hours_required"},
	} {
		assertValidationCode(t, validateSpecialDatesScheduleValue(test.value), test.code)
	}
}

func errorFromJSONStrings(value any) error {
	_, err := jsonStringArray(value)
	return err
}

func TestDateAndTimeValidationHelpers(t *testing.T) {
	for value, want := range map[string]int{"00:00": 0, "09:30": 570, "23:59": 1439} {
		actual, ok := parseHHMMMinutes(value)
		if !ok || actual != want {
			t.Errorf("parseHHMMMinutes(%q)=(%d,%v)", value, actual, ok)
		}
	}
	for _, value := range []string{"9:00", "24:00", "12:60", "bad"} {
		if _, ok := parseHHMMMinutes(value); ok {
			t.Errorf("invalid time %q accepted", value)
		}
	}
	for _, value := range []any{"2026-07-18", "2026-07-18T12:30:00Z"} {
		if parsed, ok := validationDateOnly(value); !ok || parsed.Format("2006-01-02") != "2026-07-18" {
			t.Errorf("validationDateOnly(%#v)=(%v,%v)", value, parsed, ok)
		}
	}
	for _, value := range []any{nil, "bad"} {
		if _, ok := validationDateOnly(value); ok {
			t.Errorf("invalid date %#v accepted", value)
		}
	}
	if parsed, ok := RecordDateOnly("2026-07-18T12:30:00Z"); !ok || parsed.Format("2006-01-02") != "2026-07-18" {
		t.Fatalf("RecordDateOnly=%v,%v", parsed, ok)
	}
	for _, value := range []any{nil, "bad"} {
		if _, ok := RecordDateOnly(value); ok {
			t.Errorf("RecordDateOnly accepted %#v", value)
		}
	}
	if value := stateMachineValue(map[string]any{"state": " active "}, "state"); value != "active" {
		t.Fatalf("state=%q", value)
	}
	if stateMachineValue(nil, "state") != "" || stateMachineValue(map[string]any{}, "state") != "" {
		t.Fatal("missing state not empty")
	}
	_ = time.UTC
}

func TestValidationErrorContract(t *testing.T) {
	err := &apperror.CodedError{Code: " backend.test.code ", Params: map[string]string{"a": "b"}}
	if err.Error() != " backend.test.code " || err.ErrorCode() != "backend.test.code" || err.ErrorParams()["a"] != "b" {
		t.Fatalf("validation error contract mismatch: %#v", err)
	}
	params := err.ErrorParams()
	params["a"] = "changed"
	if err.Params["a"] != "b" {
		t.Fatal("ErrorParams aliases internal map")
	}
	if (&apperror.CodedError{}).ErrorParams() != nil {
		t.Fatal("empty params should be nil")
	}
	if validationCode("backend.custom.code", "backend.fallback") != "backend.custom.code" || validationCode("plain text", "backend.fallback") != "backend.fallback" || validationCode("plain", "also plain") != "backend.validation.invalid" {
		t.Fatal("validationCode fallback mismatch")
	}
	invalid := validationError("invalid code", "", "ignored", "field", "name", "dangling")
	assertValidationCode(t, invalid, "backend.validation.invalid")
}
