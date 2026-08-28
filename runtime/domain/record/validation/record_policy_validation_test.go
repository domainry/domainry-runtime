package validation

import (
	"encoding/json"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func policyValidation(config map[string]any) definitionmodel.ValidationSchema {
	return definitionmodel.ValidationSchema{Config: config}
}

func TestRecordPolicyConditionMatches(t *testing.T) {
	tests := []struct {
		name   string
		config map[string]any
		data   map[string]any
		want   bool
	}{
		{name: "no conditions", config: nil, data: nil, want: true},
		{name: "when field in values", config: map[string]any{"when_field": "status", "when_in": []any{"draft", "ACTIVE"}}, data: map[string]any{"status": " active "}, want: true},
		{name: "when field alternate values", config: map[string]any{"when_field": "status", "values": []string{"active"}}, data: map[string]any{"status": "inactive"}},
		{name: "when field exact value", config: map[string]any{"when_field": "status", "value": "active"}, data: map[string]any{"status": "active"}, want: true},
		{name: "when field presence only", config: map[string]any{"when_field": "status"}, data: map[string]any{"status": "active"}, want: true},
		{name: "when field missing", config: map[string]any{"when_field": "status"}, data: map[string]any{}},
		{name: "conditions field", config: map[string]any{"conditions": []any{map[string]any{"field": "kind", "value": "lead"}}}, data: map[string]any{"kind": "lead"}, want: true},
		{name: "when field key", config: map[string]any{"when": []map[string]any{{"field_key": "kind", "in": []string{"lead"}}}}, data: map[string]any{"kind": "lead"}, want: true},
		{name: "condition without field", config: map[string]any{"conditions": []any{map[string]any{"value": "lead"}}}, data: map[string]any{"kind": "lead"}},
		{name: "condition mismatch", config: map[string]any{"conditions": []any{map[string]any{"field": "kind", "value": "lead"}}}, data: map[string]any{"kind": "account"}},
		{name: "when mismatch", config: map[string]any{"when": []any{map[string]any{"field": "kind", "value": "lead"}}}, data: map[string]any{"kind": "account"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RecordPolicyConditionMatches(policyValidation(tt.config), tt.data); got != tt.want {
				t.Fatalf("RecordPolicyConditionMatches() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRecordPolicyFilterExpectedValue(t *testing.T) {
	tests := []struct {
		name   string
		filter map[string]any
		data   map[string]any
		want   any
		ok     bool
	}{
		{name: "literal", filter: map[string]any{"value": 0}, want: 0, ok: true},
		{name: "nil literal falls through", filter: map[string]any{"value": nil}},
		{name: "source field", filter: map[string]any{"source_field": "owner"}, data: map[string]any{"owner": "u-1"}, want: "u-1", ok: true},
		{name: "equals field alias", filter: map[string]any{"equals_field": "owner"}, data: map[string]any{"owner": "u-2"}, want: "u-2", ok: true},
		{name: "source missing", filter: map[string]any{"source_field": "owner"}, data: map[string]any{}},
		{name: "no source", filter: map[string]any{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := RecordPolicyFilterExpectedValue(tt.filter, tt.data)
			if ok != tt.ok || got != tt.want {
				t.Fatalf("RecordPolicyFilterExpectedValue() = (%v, %v), want (%v, %v)", got, ok, tt.want, tt.ok)
			}
		})
	}
}

func TestRecordMatchesPolicyFilters(t *testing.T) {
	record := recordmodel.Record{Data: map[string]any{
		"owner": "u-1", "status": "Active", "start": "2026-07-10", "end": 20,
	}}
	tests := []struct {
		name    string
		filters []map[string]any
		data    map[string]any
		want    bool
	}{
		{name: "no filters", want: true},
		{name: "empty field ignored", filters: []map[string]any{{"value": "x"}}, want: true},
		{name: "literal equality", filters: []map[string]any{{"field": "owner", "value": "u-1"}}, want: true},
		{name: "literal mismatch", filters: []map[string]any{{"field": "owner", "value": "u-2"}}},
		{name: "source equality", filters: []map[string]any{{"field": "owner", "source_field": "actor"}}, data: map[string]any{"actor": "u-1"}, want: true},
		{name: "allowed values", filters: []map[string]any{{"field": "status", "values": []any{"draft", "active"}}}, want: true},
		{name: "disallowed value", filters: []map[string]any{{"field": "status", "values": []string{"draft"}}}},
		{name: "date lower bound", filters: []map[string]any{{"field": "start", "gte_field": "minimum"}}, data: map[string]any{"minimum": "2026-07-10"}, want: true},
		{name: "date below bound", filters: []map[string]any{{"field": "start", "gte_field": "minimum"}}, data: map[string]any{"minimum": "2026-07-11"}},
		{name: "numeric upper bound", filters: []map[string]any{{"field": "end", "lte_field": "maximum"}}, data: map[string]any{"maximum": "20"}, want: true},
		{name: "numeric above bound", filters: []map[string]any{{"field": "end", "lte_field": "maximum"}}, data: map[string]any{"maximum": 19}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validation := policyValidation(map[string]any{"filters": tt.filters})
			if got := RecordMatchesPolicyFilters(record, validation, tt.data); got != tt.want {
				t.Fatalf("RecordMatchesPolicyFilters() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRecordPolicyRangeComparisons(t *testing.T) {
	tests := []struct {
		name   string
		actual any
		bound  any
		after  bool
		before bool
	}{
		{name: "same date", actual: "2026-07-19T23:00:00Z", bound: "2026-07-19", after: true, before: true},
		{name: "later date", actual: "2026-07-20", bound: "2026-07-19", after: true},
		{name: "earlier date", actual: "2026-07-18", bound: "2026-07-19", before: true},
		{name: "same number", actual: 10, bound: "10", after: true, before: true},
		{name: "larger number", actual: json.Number("10.5"), bound: float32(10), after: true},
		{name: "smaller number", actual: int64(9), bound: float64(10), before: true},
		{name: "invalid", actual: "not-a-value", bound: 10},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RecordPolicyValueOnOrAfter(tt.actual, tt.bound); got != tt.after {
				t.Errorf("RecordPolicyValueOnOrAfter() = %v, want %v", got, tt.after)
			}
			if got := RecordPolicyValueOnOrBefore(tt.actual, tt.bound); got != tt.before {
				t.Errorf("RecordPolicyValueOnOrBefore() = %v, want %v", got, tt.before)
			}
		})
	}
}

func TestRecordThresholdPermissionConfiguration(t *testing.T) {
	if got := RecordThresholdPermissionKey(policyValidation(map[string]any{"permission": "invoice.approve"})); got != "invoice.approve" {
		t.Fatalf("permission = %q", got)
	}
	if got := RecordThresholdPermissionKey(policyValidation(map[string]any{"required_permission": "quote.approve"})); got != "quote.approve" {
		t.Fatalf("required permission = %q", got)
	}
	if got := RecordThresholdPermissionKey(policyValidation(map[string]any{"requires_permission": "order.approve"})); got != "order.approve" {
		t.Fatalf("requires permission = %q", got)
	}
	if got := RecordThresholdPermissionKey(policyValidation(nil)); got != "" {
		t.Fatalf("empty permission = %q", got)
	}

	if !RecordThresholdPermissionChangedOnly(policyValidation(map[string]any{"changed_only": "yes"})) {
		t.Fatal("changed_only alias should be true")
	}
	if !RecordThresholdPermissionChangedOnly(policyValidation(map[string]any{"changed_only": "invalid", "only_when_changed": true})) {
		t.Fatal("invalid primary value should fall through to another key")
	}
	if !RecordThresholdPermissionChangedOnly(policyValidation(map[string]any{"only_when_changed": true})) {
		t.Fatal("only_when_changed alias should be true")
	}
	if RecordThresholdPermissionChangedOnly(policyValidation(nil)) {
		t.Fatal("empty changed-only configuration should be false")
	}
}

func TestRecordThresholdPermissionExceededAny(t *testing.T) {
	tests := []struct {
		name   string
		config map[string]any
		value  float64
		data   map[string]any
		want   bool
	}{
		{name: "no checks", config: map[string]any{"min": "bad"}, value: 10},
		{name: "inclusive threshold", config: map[string]any{"min": 10}, value: 10, want: true},
		{name: "threshold aliases", config: map[string]any{"threshold": 11, "gte": 12, "min_value": 10}, value: 10, want: true},
		{name: "exclusive threshold", config: map[string]any{"exclusive_min": 10}, value: 10},
		{name: "exclusive alias", config: map[string]any{"gt": 10}, value: 11, want: true},
		{name: "ratio inclusive", config: map[string]any{"ratio_field": "base", "ratio_min": 0.5}, value: 50, data: map[string]any{"base": 100}, want: true},
		{name: "base field alias and ratio aliases", config: map[string]any{"base_field": "base", "ratio_threshold": 0.6, "min_ratio": 0.5}, value: 50, data: map[string]any{"base": 100}, want: true},
		{name: "ratio exclusive", config: map[string]any{"ratio_field": "base", "exclusive_ratio_min": 0.5}, value: 50, data: map[string]any{"base": 100}},
		{name: "ratio exclusive alias", config: map[string]any{"ratio_field": "base", "ratio_gt": 0.4}, value: 50, data: map[string]any{"base": 100}, want: true},
		{name: "invalid ratio does not count", config: map[string]any{"ratio_field": "base", "ratio_min": 0.5}, value: 50, data: map[string]any{"base": 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := RecordThresholdPermissionExceeded(policyValidation(tt.config), tt.value, tt.data); got != tt.want {
				t.Fatalf("RecordThresholdPermissionExceeded() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRecordThresholdPermissionExceededAll(t *testing.T) {
	tests := []struct {
		name   string
		config map[string]any
		value  float64
		data   map[string]any
		want   bool
	}{
		{name: "no checks", config: map[string]any{"match": "all"}, value: 10},
		{name: "all inclusive aliases", config: map[string]any{"match": "ALL", "min": 10, "threshold": 9, "gte": 8, "min_value": 7}, value: 10, want: true},
		{name: "inclusive failure", config: map[string]any{"match": "all", "min": 11}, value: 10},
		{name: "all exclusive aliases", config: map[string]any{"match": "all", "exclusive_min": 9, "gt": 8}, value: 10, want: true},
		{name: "exclusive failure", config: map[string]any{"match": "all", "gt": 10}, value: 10},
		{name: "all ratio inclusive aliases", config: map[string]any{"match": "all", "ratio_field": "base", "ratio_min": .5, "ratio_threshold": .4, "min_ratio": .3}, value: 50, data: map[string]any{"base": 100}, want: true},
		{name: "ratio inclusive failure", config: map[string]any{"match": "all", "ratio_field": "base", "ratio_min": .6}, value: 50, data: map[string]any{"base": 100}},
		{name: "all ratio exclusive aliases", config: map[string]any{"match": "all", "base_field": "base", "exclusive_ratio_min": .4, "ratio_gt": .3}, value: 50, data: map[string]any{"base": 100}, want: true},
		{name: "ratio exclusive failure", config: map[string]any{"match": "all", "ratio_field": "base", "ratio_gt": .5}, value: 50, data: map[string]any{"base": 100}},
		{name: "ratio unavailable", config: map[string]any{"match": "all", "ratio_field": "base", "ratio_min": .5}, value: 50, data: map[string]any{"base": 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validation := policyValidation(tt.config)
			if got := RecordThresholdPermissionExceeded(validation, tt.value, tt.data); got != tt.want {
				t.Fatalf("RecordThresholdPermissionExceeded(all) = %v, want %v", got, tt.want)
			}
			if got := RecordThresholdPermissionAllExceeded(validation, tt.value, tt.data); got != tt.want {
				t.Fatalf("RecordThresholdPermissionAllExceeded() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestRecordPolicyConversionHelpers(t *testing.T) {
	if ratio, ok := RecordThresholdPermissionRatio(50, "100"); !ok || ratio != .5 {
		t.Fatalf("ratio = (%v, %v)", ratio, ok)
	}
	for _, base := range []any{0, "bad", nil} {
		if ratio, ok := RecordThresholdPermissionRatio(50, base); ok || ratio != 0 {
			t.Fatalf("invalid ratio for %v = (%v, %v)", base, ratio, ok)
		}
	}

	maps := []map[string]any{{"id": 1}}
	gotMaps := policyMapSlice(maps)
	gotMaps[0] = map[string]any{"id": 2}
	if maps[0]["id"] != 1 {
		t.Fatal("typed map slice must be copied")
	}
	if got := policyMapSlice([]any{map[string]any{"id": 1}, "skip"}); len(got) != 1 {
		t.Fatalf("mixed map slice length = %d", len(got))
	}
	if got := policyMapSlice("invalid"); got != nil {
		t.Fatalf("invalid map slice = %#v", got)
	}
	if got := RecordMapSlice([]any{map[string]any{"id": 1}}); len(got) != 1 {
		t.Fatalf("RecordMapSlice length = %d", len(got))
	}

	if policyConfigString(nil) != "" || policyConfigString(" <nil> ") != "" || policyConfigString(" value ") != "value" {
		t.Fatal("policyConfigString normalization mismatch")
	}

	numbers := []struct {
		value any
		want  float64
		ok    bool
	}{
		{value: float64(1.5), want: 1.5, ok: true},
		{value: float32(2.5), want: 2.5, ok: true},
		{value: int(3), want: 3, ok: true},
		{value: int64(4), want: 4, ok: true},
		{value: json.Number("5.5"), want: 5.5, ok: true},
		{value: json.Number("bad")},
		{value: " 6.5 ", want: 6.5, ok: true},
		{value: "bad"},
		{value: true},
	}
	for _, tt := range numbers {
		if got, ok := policyNumericAny(tt.value); got != tt.want || ok != tt.ok {
			t.Errorf("policyNumericAny(%v) = (%v, %v), want (%v, %v)", tt.value, got, ok, tt.want, tt.ok)
		}
	}
	if got, ok := RecordNumericValue("7.5"); !ok || got != 7.5 {
		t.Fatalf("RecordNumericValue() = (%v, %v)", got, ok)
	}

	bools := []struct {
		value any
		want  bool
		ok    bool
	}{
		{value: true, want: true, ok: true},
		{value: false, ok: true},
		{value: " YES ", want: true, ok: true},
		{value: "1", want: true, ok: true},
		{value: "true", want: true, ok: true},
		{value: "no", ok: true},
		{value: "0", ok: true},
		{value: "false", ok: true},
		{value: "invalid"},
		{value: 1},
	}
	for _, tt := range bools {
		if got, ok := policyBoolAny(tt.value); got != tt.want || ok != tt.ok {
			t.Errorf("policyBoolAny(%v) = (%v, %v), want (%v, %v)", tt.value, got, ok, tt.want, tt.ok)
		}
	}
	if got, ok := RecordBoolValue("true"); !ok || !got {
		t.Fatalf("RecordBoolValue() = (%v, %v)", got, ok)
	}
}
