package validation

import (
	"encoding/json"
	"math"
	"net/url"
	"testing"
)

func TestRuleValueConversions(t *testing.T) {
	for _, test := range []struct {
		value any
		want  float64
		ok    bool
	}{
		{float64(1.5), 1.5, true}, {float32(2.5), 2.5, true}, {3, 3, true}, {int64(4), 4, true},
		{" 5.5 ", 5.5, true}, {json.Number("6.5"), 6.5, true}, {json.Number("bad"), 0, false}, {true, 0, false},
	} {
		actual, ok := numericValue(test.value)
		if ok != test.ok || ok && math.Abs(actual-test.want) > .0001 {
			t.Errorf("numericValue(%#v)=(%v,%v), want (%v,%v)", test.value, actual, ok, test.want, test.ok)
		}
	}
	for _, test := range []struct {
		value any
		want  int64
		ok    bool
	}{
		{int64(math.MaxInt64), math.MaxInt64, true}, {int64(math.MinInt64), math.MinInt64, true}, {json.Number("9223372036854775807"), math.MaxInt64, true},
		{"-9223372036854775808", math.MinInt64, true}, {float64(3), 3, true}, {float64(3.5), 0, false}, {float64(9223372036854775808), 0, false}, {"1.0", 0, false},
	} {
		actual, ok := integerValue(test.value)
		if ok != test.ok || ok && actual != test.want {
			t.Errorf("integerValue(%#v)=(%v,%v), want (%v,%v)", test.value, actual, ok, test.want, test.ok)
		}
	}
	for _, value := range []any{float64(1), float32(1), 1, int64(1), "1"} {
		if actual, ok := numericAny(value); !ok || actual != 1 {
			t.Errorf("numericAny(%#v)=(%v,%v)", value, actual, ok)
		}
	}
	for _, value := range []string{"bad", "123abc", "2026-07-18"} {
		if _, ok := numericAny(value); ok {
			t.Fatalf("invalid numericAny accepted %q", value)
		}
	}
}

func TestRuleBooleanAndStringConversions(t *testing.T) {
	for _, test := range []struct {
		value any
		want  bool
		ok    bool
	}{
		{true, true, true}, {false, false, true}, {1, true, true}, {int64(0), false, true}, {float64(2), true, true},
		{json.Number("1"), true, true}, {json.Number("bad"), false, false}, {" yes ", true, true}, {"off", false, true}, {"bad", false, false}, {struct{}{}, false, false},
	} {
		actual, ok := boolValue(test.value)
		if ok != test.ok || ok && actual != test.want {
			t.Errorf("boolValue(%#v)=(%v,%v), want (%v,%v)", test.value, actual, ok, test.want, test.ok)
		}
	}
	for _, test := range []struct {
		value any
		want  bool
		ok    bool
	}{
		{true, true, true}, {"true", true, true}, {"1", true, true}, {"yes", true, true}, {"false", false, true}, {"0", false, true}, {"no", false, true}, {"bad", false, false}, {1, false, false},
	} {
		actual, ok := boolAny(test.value)
		if ok != test.ok || ok && actual != test.want {
			t.Errorf("boolAny(%#v)=(%v,%v)", test.value, actual, ok)
		}
	}
	parsed, _ := url.Parse("https://example.com/path")
	if value, ok := stringValue(parsed); !ok || value != "https://example.com/path" {
		t.Fatalf("stringer value=(%q,%v)", value, ok)
	}
	if value, ok := stringValue(" text "); !ok || value != "text" {
		t.Fatalf("string value=(%q,%v)", value, ok)
	}
	if _, ok := stringValue(1); ok {
		t.Fatal("integer accepted as string")
	}
}

func TestRuleCollectionValueHelpers(t *testing.T) {
	if !containsOption([]string{"a", "b"}, "b") || containsOption([]string{"a"}, "b") {
		t.Fatal("containsOption mismatch")
	}
	if !containsString([]string{"a", "b"}, "a") || containsString(nil, "a") {
		t.Fatal("containsString mismatch")
	}
	if !RecordIsEmptyValue(nil) || !RecordIsEmptyValue("  ") || RecordIsEmptyValue(0) || RecordIsEmptyValue("x") {
		t.Fatal("RecordIsEmptyValue mismatch")
	}
	original := map[string]any{"a": 1}
	clone := RecordCloneData(original)
	clone["a"] = 2
	if original["a"] != 1 {
		t.Fatal("RecordCloneData aliases source map")
	}
}
