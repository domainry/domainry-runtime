package policy

import (
	"encoding/json"
	"testing"
	"time"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func TestExecutionPolicyHelpers(t *testing.T) {
	if SchedulerLimit(0) != 25 || SchedulerLimit(999) != 500 || SchedulerLimit(7) != 7 {
		t.Fatal("unexpected scheduler limits")
	}
	if SchedulerNextAttempt(recordmodel.Record{Data: map[string]any{"attempt": 2}}, "failed") != 3 {
		t.Fatal("failed run must advance attempt")
	}
	now := time.Now().UTC()
	if !SchedulerLeaseExpired(recordmodel.Record{Data: map[string]any{"lease_expires_at": now.Add(-time.Minute).Format(time.RFC3339)}}, now) {
		t.Fatal("past lease must be expired")
	}
	if SchedulerSlug(" Daily / Billing ") != "daily_billing" {
		t.Fatal("slug normalization changed")
	}
}

func TestSchedulerIntAcceptsJSONNumberFromStrictBlueprintDecode(t *testing.T) {
	if got := SchedulerInt(json.Number("1"), 0); got != 1 {
		t.Fatalf("SchedulerInt(json.Number(1))=%d", got)
	}
}
