package validation

import (
	"testing"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

type blankRecordStringer struct{}

func (blankRecordStringer) String() string { return "" }

func TestRecordRetentionAndStalePolicyEdges(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	retention := definitionmodel.ValidationSchema{Key: "retain", Type: "retention_guard", Config: map[string]any{}}
	object := definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{retention}}
	if got := recordValidationErrorCode(validateRetentionPoliciesAt(object, nil, map[string]any{"legal_hold": true}, "delete", now)); got != "backend.policy.retention_guard" {
		t.Fatalf("got %s", got)
	}
	if got := recordValidationErrorCode(validateRetentionPoliciesAt(object, nil, map[string]any{"retain_until": "bad"}, "delete", now)); got != "backend.validation.date_format" {
		t.Fatalf("got %s", got)
	}
	if got := recordValidationErrorCode(validateRetentionPoliciesAt(object, nil, map[string]any{"retain_until": "2026-07-19"}, "delete", now)); got != "backend.policy.retention_guard" {
		t.Fatalf("got %s", got)
	}
	if err := validateRetentionPoliciesAt(object, nil, map[string]any{"retain_until": "2026-07-18"}, "delete", now); err != nil {
		t.Fatal(err)
	}
	if err := RecordValidateRetentionPolicies(definitionmodel.ObjectSchema{}, nil, nil, "delete"); err != nil {
		t.Fatal(err)
	}

	stale := definitionmodel.ValidationSchema{Key: "stale", Type: "stale_record_guard", Config: map[string]any{"max_idle_days": 30}}
	object.Validations = []definitionmodel.ValidationSchema{stale}
	if got := recordValidationErrorCode(validateStaleRecordPoliciesAt(object, map[string]any{"last_activity_at": "bad"}, "update", now)); got != "backend.validation.date_format" {
		t.Fatalf("got %s", got)
	}
	if got := recordValidationErrorCode(validateStaleRecordPoliciesAt(object, map[string]any{"last_activity_at": "2026-01-01"}, "update", now)); got != "backend.policy.stale_record_required_field" {
		t.Fatalf("got %s", got)
	}
	if err := validateStaleRecordPoliciesAt(object, map[string]any{"last_activity_at": "2026-01-01", "stalled_reason": "known"}, "update", now); err != nil {
		t.Fatal(err)
	}
	if err := RecordValidateStaleRecordPolicies(definitionmodel.ObjectSchema{}, nil, "update"); err != nil {
		t.Fatal(err)
	}
	for input, want := range map[string]string{"custom.code": "custom.code", "plain": "fallback", "bad code.x": "fallback", "bad@code.x": "fallback"} {
		if got := RecordPolicyMessageCode(input, "fallback"); got != want {
			t.Fatalf("%s=%s", input, got)
		}
	}
	for key, want := range map[string]int{"max_idle_days": 1, "max_age_days": 2, "stale_after_days": 3} {
		if got := RecordStalePolicyMaxAgeDays(definitionmodel.ValidationSchema{Config: map[string]any{key: want}}); got != want {
			t.Fatalf("%s=%d", key, got)
		}
	}
	if RecordStalePolicyMaxAgeDays(definitionmodel.ValidationSchema{}) != 0 {
		t.Fatal("max age")
	}
	for _, value := range []any{nil, " ", "2026-01-02", "2026-01-02T03:04:05Z", "bad"} {
		_, _, _ = RecordStalePolicyDateValue(value)
		_, _, _ = RecordRetentionPolicyDateValue(value)
	}
}

func TestRecordRetentionDeleteIntent(t *testing.T) {
	v := definitionmodel.ValidationSchema{Config: map[string]any{}}
	if !RecordRetentionPolicyDeleteIntent(v, nil, nil, " DELETE ") {
		t.Fatal("delete")
	}
	if RecordRetentionPolicyDeleteIntent(v, nil, map[string]any{"status": "open"}, "update") {
		t.Fatal("open")
	}
	if !RecordRetentionPolicyDeleteIntent(v, nil, map[string]any{"status": "deleted"}, "update") {
		t.Fatal("deleted create")
	}
	if RecordRetentionPolicyDeleteIntent(v, map[string]any{"status": "deleted"}, map[string]any{"status": "deleted"}, "update") {
		t.Fatal("unchanged")
	}
	v.Config = map[string]any{"delete_status_field": "state", "delete_status_values": []string{"removed"}}
	if !RecordRetentionPolicyDeleteIntent(v, map[string]any{"state": "open"}, map[string]any{"state": "removed"}, "update") {
		t.Fatal("custom delete")
	}
	v.Config = map[string]any{"deleted_statuses": []string{"gone"}}
	if !RecordRetentionPolicyDeleteIntent(v, nil, map[string]any{"status": "gone"}, "update") {
		t.Fatal("alias delete")
	}
}

func TestRecordTimeOverlapEdges(t *testing.T) {
	for _, tc := range []struct {
		validation definitionmodel.ValidationSchema
		start, end string
	}{
		{definitionmodel.ValidationSchema{Config: map[string]any{"start_field": "from", "end_field": "to"}}, "from", "to"},
		{definitionmodel.ValidationSchema{Fields: []string{"a", "b"}}, "a", "b"},
		{definitionmodel.ValidationSchema{}, "starts_at", "ends_at"},
	} {
		start, end := RecordTimeOverlapRangeFields(tc.validation)
		if start != tc.start || end != tc.end {
			t.Fatalf("%s %s", start, end)
		}
	}
	if _, _, ok, err := RecordTimeOverlapRange(map[string]any{}, "start", "end"); err != nil || ok {
		t.Fatalf("%v %v", ok, err)
	}
	if _, _, _, err := RecordTimeOverlapRange(map[string]any{"start": "bad", "end": "2026-01-02"}, "start", "end"); recordValidationErrorCode(err) != "backend.validation.datetime_format" {
		t.Fatal(err)
	}
	if _, _, _, err := RecordTimeOverlapRange(map[string]any{"start": "2026-01-01", "end": "bad"}, "start", "end"); recordValidationErrorCode(err) != "backend.validation.datetime_format" {
		t.Fatal(err)
	}
	start, end, ok, err := RecordTimeOverlapRange(map[string]any{"start": "2026-01-01", "end": "2026-01-02"}, "start", "end")
	if err != nil || !ok || !end.After(start) {
		t.Fatalf("%v %v", ok, err)
	}
	if _, _, _, err := RecordTimeOverlapRange(map[string]any{"start": "2026-01-02T00:00:00Z", "end": "2026-01-01T00:00:00Z"}, "start", "end"); recordValidationErrorCode(err) != "backend.policy.invalid_time_range" {
		t.Fatal(err)
	}
	if _, dateOnly, err := parseOverlapBoundary("2026-01-01"); err != nil || !dateOnly {
		t.Fatal(err)
	}
	if _, dateOnly, err := parseOverlapBoundary("2026-01-01T00:00:00Z"); err != nil || dateOnly {
		t.Fatal(err)
	}

	left, right := map[string]any{"owner": "u1", "store": "s1"}, map[string]any{"owner": "u1", "store": "s2"}
	if !RecordTimeOverlapScopeMatches(definitionmodel.ValidationSchema{}, left, right) {
		t.Fatal("default owner")
	}
	validation := definitionmodel.ValidationSchema{Config: map[string]any{"resource_groups": []any{[]string{"owner", "store"}, "store"}}}
	if RecordTimeOverlapScopeMatches(validation, left, right) {
		t.Fatal("group mismatch should not match")
	}
	right["store"] = "s1"
	if !RecordTimeOverlapScopeMatches(validation, left, right) {
		t.Fatal("group match")
	}
	for _, config := range []map[string]any{{"scope_groups": []any{[]any{"owner"}}}, {"scope_fields": []string{"owner"}}, {"match_fields": []string{"owner"}}, {"resource_fields": []string{"owner"}}} {
		if len(timeOverlapResourceGroups(definitionmodel.ValidationSchema{Config: config})) == 0 {
			t.Fatalf("%#v", config)
		}
	}
	if timeOverlapResourceGroupMatches([]string{"", "owner"}, left, right) != true || timeOverlapResourceGroupMatches([]string{"missing"}, left, right) {
		t.Fatal("group helper")
	}
	if stringGroupsFromAny("bad") != nil || len(stringGroupsFromAny([]any{"owner", []string{"store"}, nil})) != 2 {
		t.Fatal("string groups")
	}
	if err := validationTimeError(apperror.KindBadRequest, "x", " ", "ignored"); recordValidationErrorCode(err) != "x" {
		t.Fatal(err)
	}
}

func TestRecordTimeRemainingStatements(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	retentionObject := definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{{Type: "other"}, {Type: "retention_guard", Severity: "warning"}}}
	if err := validateRetentionPoliciesAt(retentionObject, nil, nil, "delete", now); err != nil {
		t.Fatal(err)
	}
	staleObject := definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{{Type: "other"}, {Type: "stale_record_guard", Severity: "warning"}}}
	if err := validateStaleRecordPoliciesAt(staleObject, nil, "update", now); err != nil {
		t.Fatal(err)
	}
	for _, value := range []any{blankRecordStringer{}} {
		_, _, _ = RecordStalePolicyDateValue(value)
		_, _, _ = RecordRetentionPolicyDateValue(value)
	}
	if len(stringGroupsFromAny([]any{42})) != 1 {
		t.Fatal("scalar group")
	}
}
