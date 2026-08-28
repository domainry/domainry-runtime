package rulesetmodel

import (
	"errors"
	"testing"
)

func TestParseEffectiveTimeFallsBackFromInvalidDateToRFC3339(t *testing.T) {
	if value, err := ParseEffectiveTime("2026-07-22"); err != nil || value.IsZero() {
		t.Fatalf("valid date=%v err=%v", value, err)
	}
	if value, err := ParseEffectiveTime("2026-07-22T01:02:03Z"); err != nil || value.IsZero() {
		t.Fatalf("valid timestamp=%v err=%v", value, err)
	}
	if _, err := ParseEffectiveTime("not-a-date"); err == nil {
		t.Fatal("invalid date was accepted")
	}
	cause := errors.New("cause")
	err := &RuleSetError{Code: " backend.rule_set.invalid ", RuleSetKey: " pricing ", Cause: cause}
	if err.Error() == "" || err.ErrorCode() != "backend.rule_set.invalid" || err.ErrorParams()["rule_set_key"] != "pricing" || !errors.Is(err, cause) {
		t.Fatalf("rule set error=%#v", err)
	}
}
