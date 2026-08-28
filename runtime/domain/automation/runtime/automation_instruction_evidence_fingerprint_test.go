package runtime

import (
	"testing"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestAutomationFingerprintsAreStableForEquivalentUnicode(t *testing.T) {
	composed := automationmodel.AutomationRuleSchema{Key: "caf\u00e9", ObjectKey: "r\u00e9sum\u00e9", Trigger: automationmodel.AutomationTriggerSchema{Operation: "cr\u00e9er"}}
	decomposed := automationmodel.AutomationRuleSchema{Key: "cafe\u0301", ObjectKey: "re\u0301sume\u0301", Trigger: automationmodel.AutomationTriggerSchema{Operation: "cre\u0301er"}}
	record := &recordmodel.Record{ID: "re\u0301cord", UpdatedAt: "2026-07-19T00:00:00Z"}
	first := AutomationRuleIdempotencyKey(composed, record)
	second := AutomationRuleIdempotencyKey(decomposed, record)
	if first == "" || first != second {
		t.Fatalf("equivalent fingerprints = %q / %q", first, second)
	}
	if first == AutomationInstructionIdempotencyKey(composed, automationmodel.AutomationInstructionSchema{Key: "step"}, record) {
		t.Fatal("rule and instruction use cases collided")
	}
	if AutomationRuleIdempotencyKey(composed, nil) == "" || AutomationRecordVersion(nil) != "unknown" || AutomationRecordVersion(&recordmodel.Record{}) != "unknown" {
		t.Fatal("nil/zero record fingerprint fallback failed")
	}
}
