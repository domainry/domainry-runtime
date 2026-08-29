package projection

import "testing"

func TestSchedulerSystemObjectsContainOnlyRecordTimerLifecycle(t *testing.T) {
	objects := SchedulerSystemObjects()
	if len(objects) != 2 || objects[0].Key != "record_timer" || objects[1].Key != "record_timer_event" {
		t.Fatalf("objects = %#v", objects)
	}
	for _, object := range objects {
		if object.Config["runtime_owned"] != true || object.Config["system_kind"] != "record_timer" || object.Config["record_timer_runtime"] != true {
			t.Fatalf("ownership for %s = %#v", object.Key, object.Config)
		}
	}
	if objects[1].Config["append_only"] != true {
		t.Fatalf("event contract = %#v", objects[1].Config)
	}
	objects[0].Fields[0].Key = "mutated"
	if SchedulerSystemObjects()[0].Fields[0].Key == "mutated" {
		t.Fatal("system schema leaked mutation")
	}
}

func TestRecordTimerPublishesDurableIdentityAndOrderedClaimContract(t *testing.T) {
	object := SchedulerSystemObjects()[0]
	if len(object.Validations) != 1 || object.Validations[0].Type != "composite_unique" {
		t.Fatalf("identity contract = %#v", object.Validations)
	}
	indexed, bounded := map[string]bool{}, map[string]bool{}
	for _, field := range object.Fields {
		indexed[field.Key] = field.Config["indexed"] == true
		bounded[field.Key] = field.Config["max_length"] == 128
	}
	for _, field := range []string{"timer_key", "object_key", "record_id", "purpose"} {
		if !bounded[field] {
			t.Fatalf("identity field %s is unbounded", field)
		}
	}
	for _, field := range []string{"status", "due_at", "priority", "sequence", "object_key", "record_id"} {
		if !indexed[field] {
			t.Fatalf("claim field %s is not indexed", field)
		}
	}
}

func TestRecordTimerEventIsRelatedAndIndexedEvidence(t *testing.T) {
	object := SchedulerSystemObjects()[1]
	for _, field := range object.Fields {
		if field.Key == "record_timer_id" {
			if field.Type != "relation" || field.Validation.Target != "record_timer" || field.Config["indexed"] != true {
				t.Fatalf("timer relation = %#v", field)
			}
			return
		}
	}
	t.Fatal("record_timer_id field missing")
}
