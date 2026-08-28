package changeplanmodel

import "testing"

func TestBusinessChangePlanDraftConflictErrorIncludesPlanID(t *testing.T) {
	err := (&BusinessChangePlanDraftConflictError{PlanID: "plan-42"}).Error()
	if err != "domain change plan draft conflict: plan-42" {
		t.Fatalf("error=%q", err)
	}
}
