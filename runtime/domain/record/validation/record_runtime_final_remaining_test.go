package validation

import (
	"encoding/json"
	"math"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestRecordIntegerRemainingRepresentations(t *testing.T) {
	field := definitionmodel.FieldSchema{Key: "count", Type: "integer"}
	for _, value := range []any{math.NaN(), math.Inf(1), 1.5, math.Nextafter(float64(math.MinInt64), math.Inf(-1)), math.Nextafter(float64(math.MaxInt64), math.Inf(1)), "bad"} {
		if err := validateFieldType(field, value); err == nil {
			t.Fatalf("invalid integer accepted: %v", value)
		}
	}
	for _, value := range []any{int(1), int64(1), float64(1)} {
		if err := validateFieldType(field, value); err != nil {
			t.Fatalf("valid integer %T rejected: %v", value, err)
		}
	}
	for _, value := range []any{math.NaN(), math.Inf(1), 1.5, -math.MaxFloat64, float64(1 << 63)} {
		if _, ok := integerValue(value); ok {
			t.Fatalf("invalid integer value accepted: %v", value)
		}
	}
	for _, value := range []any{float32(1), json.Number("1"), "1"} {
		if _, ok := integerValue(value); !ok {
			t.Fatalf("valid integer value rejected: %T", value)
		}
	}
	if _, ok := integerValue(struct{}{}); ok {
		t.Fatal("unsupported integer accepted")
	}
}

func TestRecordStateMachineRemainingContracts(t *testing.T) {
	invalid := definitionmodel.ValidationSchema{Type: "state_machine", FieldKey: "status", Config: map[string]any{"transitions": []any{map[string]any{"from": "draft", "to": "done", "effects": []any{"external"}}}}}
	if err := RecordValidateStateMachinePolicies(definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{invalid}}, map[string]any{"status": "draft"}, map[string]any{"status": "done"}, principalmodel.Principal{}); err == nil {
		t.Fatal("invalid state effect accepted")
	}
	for index, value := range []any{"invalid", map[string]any{"field": "value"}} {
		validation := definitionmodel.ValidationSchema{Config: map[string]any{"transitions": []any{map[string]any{"patch": value}}}}
		err := RecordValidateStateMachineEffectContract(validation)
		if (index == 0) == (err == nil) {
			t.Fatalf("patch=%T err=%v", value, err)
		}
	}
	for _, effect := range []map[string]any{{"kind": "patch_self"}, {"type": "update_self"}, {"type": "set_fields"}} {
		validation := definitionmodel.ValidationSchema{Config: map[string]any{"transitions": []any{map[string]any{"effects": []any{effect}}}}}
		if err := RecordValidateStateMachineEffectContract(validation); err != nil {
			t.Fatalf("self effect rejected: %v", err)
		}
	}
	if effects := RecordTransitionEffects(map[string]any{"effects": []string{"one", "two"}}); len(effects) != 2 {
		t.Fatalf("effects=%v", effects)
	}
	validation := definitionmodel.ValidationSchema{Config: map[string]any{"transitions": []any{map[string]any{"from": "draft", "to": "done"}}}}
	if _, ok := RecordStateMachineTransition(validation, "other", "done"); ok {
		t.Fatal("unpublished transition accepted")
	}
}

func TestRecordTemporalExclusionRemainingMatrix(t *testing.T) {
	base := definitionmodel.ValidationSchema{Key: "booking_overlap", Type: "temporal_exclusion", Config: map[string]any{"start_field": "starts_at", "end_field": "ends_at", "scope_fields": []string{"room_id"}}}
	object := definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{{Type: "other"}, base}}
	policies, err := RecordTemporalExclusionPolicies(object)
	if err != nil || len(policies) != 1 {
		t.Fatalf("policies=%v err=%v", policies, err)
	}
	for _, config := range []map[string]any{
		{"end_field": "ends_at", "scope_fields": []string{"room_id"}},
		{"start_field": "starts_at", "scope_fields": []string{"room_id"}},
		{"start_field": "starts_at", "end_field": "ends_at"},
	} {
		if _, err := RecordTemporalExclusionPolicies(definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{{Type: "temporal_exclusion", Config: config}}}); err == nil {
			t.Fatalf("invalid config accepted: %v", config)
		}
	}
	if got := recordStringList([]any{nil, " ", " room ", 2}); len(got) != 2 || got[0] != "room" {
		t.Fatalf("list=%v", got)
	}
	if got := recordStringList([]string{" ", " room "}); len(got) != 1 || got[0] != "room" {
		t.Fatalf("string list=%v", got)
	}
	if got := recordStringList("unsupported"); len(got) != 0 {
		t.Fatalf("unsupported list=%v", got)
	}

	policy := policies[0]
	valid := map[string]any{"starts_at": "2026-08-01", "ends_at": "2026-08-02", "room_id": "room"}
	if ok, err := RecordTemporalExclusionCandidate(policy, valid, "create"); err != nil || !ok {
		t.Fatalf("valid candidate ok=%v err=%v", ok, err)
	}
	variants := []RecordTemporalExclusionPolicy{policy, policy, policy}
	for index := range variants {
		config := map[string]any{}
		for key, value := range policy.Definition.Config {
			config[key] = value
		}
		variants[index].Definition.Config = config
	}
	variants[0].Definition.Severity = "warning"
	variants[1].Definition.Config["operations"] = []string{"update"}
	variants[2].Definition.Config["when_field"], variants[2].Definition.Config["values"] = "status", []string{"active"}
	for index, candidatePolicy := range variants {
		if ok, err := RecordTemporalExclusionCandidate(candidatePolicy, valid, "create"); err != nil || ok {
			t.Fatalf("skipped candidate %d ok=%v err=%v", index, ok, err)
		}
	}
	policy = policies[0]
	policy.StatusField, policy.ExcludedStatuses = "status", []string{"cancelled"}
	if ok, err := RecordTemporalExclusionCandidate(policy, map[string]any{"status": "cancelled"}, "create"); err != nil || ok {
		t.Fatalf("excluded candidate ok=%v err=%v", ok, err)
	}
	active := map[string]any{"status": "active", "starts_at": "2026-08-01", "ends_at": "2026-08-02", "room_id": "room"}
	if ok, err := RecordTemporalExclusionCandidate(policy, active, "create"); err != nil || !ok {
		t.Fatalf("nonexcluded candidate ok=%v err=%v", ok, err)
	}
	for name, data := range map[string]map[string]any{
		"invalid range": {"starts_at": "bad", "ends_at": "2026-08-02", "room_id": "room"},
		"missing range": {"starts_at": "", "ends_at": "", "room_id": "room"},
		"missing scope": {"starts_at": "2026-08-01", "ends_at": "2026-08-02"},
	} {
		ok, err := RecordTemporalExclusionCandidate(policies[0], data, "create")
		if name == "missing range" {
			if err != nil || ok {
				t.Fatalf("%s ok=%v err=%v", name, ok, err)
			}
		} else if err == nil || ok {
			t.Fatalf("%s ok=%v err=%v", name, ok, err)
		}
	}
}

func TestRecordConditionalUniqueObjectValidationRemaining(t *testing.T) {
	fields := []definitionmodel.FieldSchema{{Key: "member_id", Type: "text"}, {Key: "status", Type: "text"}}
	rule := definitionmodel.ValidationSchema{Key: "active_member", Type: ConditionalUniqueValidationType, Fields: []string{"member_id"}, Config: map[string]any{"condition_field": "status", "condition_values": []string{"active"}}}
	object := definitionmodel.ObjectSchema{Key: "membership", Fields: fields, Validations: []definitionmodel.ValidationSchema{rule}}
	if err := RecordValidateData(object, map[string]any{"member_id": "", "status": "active"}, false); err == nil {
		t.Fatal("empty constrained field accepted")
	}
	if err := RecordValidateData(object, map[string]any{"member_id": "member", "status": "active"}, false); err != nil {
		t.Fatal(err)
	}
	if err := RecordValidateData(object, map[string]any{"member_id": "", "status": "inactive"}, false); err != nil {
		t.Fatalf("inactive candidate rejected: %v", err)
	}
	warning := rule
	warning.Severity = "warning"
	if err := RecordValidateData(definitionmodel.ObjectSchema{Key: object.Key, Fields: fields, Validations: []definitionmodel.ValidationSchema{warning}}, map[string]any{"member_id": "", "status": "active"}, false); err != nil {
		t.Fatalf("nonblocking validation rejected: %v", err)
	}
	invalid := rule
	invalid.Config = map[string]any{"condition_field": "missing", "condition_values": []string{"active"}}
	if err := RecordValidateData(definitionmodel.ObjectSchema{Key: object.Key, Fields: fields, Validations: []definitionmodel.ValidationSchema{invalid}}, map[string]any{"member_id": "member", "status": "active"}, false); err == nil {
		t.Fatal("invalid conditional unique accepted")
	}
}
