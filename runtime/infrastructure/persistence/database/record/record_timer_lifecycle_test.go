package record

import (
	"slices"
	"testing"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordtimerprojection "github.com/domainry/domainry-runtime/runtime/domain/recordtimer/projection"
)

func TestRecordTimerLifecycleUsesRecordPolicyWithoutSchedulerCoupling(t *testing.T) {
	specs := recordLifecycleSpecs(recordtimerprojection.RecordTimerSystemObjects()...)
	var found bool
	for _, spec := range specs {
		if spec.Table != "record_timer" {
			continue
		}
		found = true
		if spec.PolicyKey != "record.object.default.v1" || spec.TimeColumn != "updated_at" || spec.StatusColumn != "status" {
			t.Fatalf("record timer cleanup spec=%#v", spec)
		}
		for _, status := range []string{"fired", "cancelled", "superseded", "failed"} {
			if !slices.Contains(spec.EligibleStatuses, status) {
				t.Fatalf("terminal status %q missing from %#v", status, spec.EligibleStatuses)
			}
		}
		if len(spec.ChildCollections) != 1 || spec.ChildCollections[0].Table != "record_timer_event" || spec.ChildCollections[0].ParentColumn != "record_timer_id" {
			t.Fatalf("record timer event cleanup=%#v", spec.ChildCollections)
		}
	}
	if !found {
		t.Fatal("record timer lifecycle cleanup spec missing")
	}
}

func TestRecordTimerLifecycleSpecRequiresBothSystemObjects(t *testing.T) {
	objects := recordtimerprojection.RecordTimerSystemObjects()
	for _, partial := range [][]int{{0}, {1}} {
		selected := []definitionmodel.ObjectSchema{objects[partial[0]]}
		for _, spec := range recordLifecycleSpecs(selected...) {
			if spec.Table == "record_timer" {
				t.Fatalf("partial system schema registered record timer cleanup: %#v", spec)
			}
		}
	}
}
