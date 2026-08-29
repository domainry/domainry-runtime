package projection

import "testing"

func TestSchedulerSystemObjectsCanonicalShape(t *testing.T) {
	objects := SchedulerSystemObjects()
	if len(objects) != 5 {
		t.Fatalf("objects = %d", len(objects))
	}
	wantKeys := []string{"scheduler_cursor", "job_run", "job_run_event", "job_dead_letter", "record_timer"}
	for index, object := range objects {
		if object.Key != wantKeys[index] || object.Name == "" || object.Description == "" || len(object.Fields) == 0 || object.Config["runtime_owned"] != true || object.Config["system_kind"] != "scheduler" || object.UX == nil {
			t.Errorf("object[%d] = %#v", index, object)
		}
		for _, field := range object.Fields {
			if field.Key == "" || field.Name == "" || field.Type == "" || field.Config == nil {
				t.Errorf("incomplete field in %s: %#v", object.Key, field)
			}
		}
	}
	objects[0].Fields[0].Key = "mutated"
	if SchedulerSystemObjects()[0].Fields[0].Key == "mutated" {
		t.Fatal("system schema leaked mutation")
	}
}

func TestSchedulerRecordTimerPublishesDurableUniqueAndOrderedClaimContract(t *testing.T) {
	object := SchedulerSystemObjects()[4]
	if object.Key != "record_timer" || len(object.Validations) != 1 || object.Validations[0].Type != "composite_unique" {
		t.Fatalf("record timer identity contract = %#v", object)
	}
	indexed := map[string]bool{}
	bounded := map[string]bool{}
	for _, field := range object.Fields {
		if field.Config["indexed"] == true {
			indexed[field.Key] = true
		}
		if field.Config["max_length"] == 128 {
			bounded[field.Key] = true
		}
	}
	for _, field := range []string{"timer_key", "object_key", "record_id", "purpose"} {
		if !bounded[field] {
			t.Fatalf("record timer identity field %s has no portable index bound", field)
		}
	}
	for _, field := range []string{"status", "due_at", "priority", "sequence", "object_key", "record_id"} {
		if !indexed[field] {
			t.Fatalf("record timer ordered claim field %s is not indexed", field)
		}
	}
	statusOptions := map[string]bool{}
	for _, field := range object.Fields {
		if field.Key == "status" {
			for _, option := range field.Validation.Options {
				statusOptions[option] = true
			}
		}
	}
	for _, status := range []string{"scheduled", "leased", "fired", "cancelled", "superseded", "failed"} {
		if !statusOptions[status] {
			t.Fatalf("record timer status %s is not published", status)
		}
	}
}

func TestSchedulerRunSchemaMatchesRuntimePersistenceContract(t *testing.T) {
	objects := SchedulerSystemObjects()
	if len(objects[1].Validations) != 1 || objects[1].Validations[0].Type != "composite_unique" {
		t.Fatalf("job_run window identity contract = %#v", objects[1].Validations)
	}
	windows := objects[1].Validations[0].Fields
	if len(windows) != 2 || windows[0] != "scheduler_definition_key" || windows[1] != "scheduled_for" {
		t.Fatalf("job_run window fields = %#v", windows)
	}
	triggeredBy := map[string]bool{}
	for _, field := range objects[1].Fields {
		if field.Key == "triggered_by" {
			for _, option := range field.Validation.Options {
				triggeredBy[option] = true
			}
		}
	}
	if !triggeredBy["manual_run"] {
		t.Fatalf("job_run.triggered_by options = %#v", triggeredBy)
	}
	for _, field := range objects[2].Fields {
		if field.Key == "created_at" {
			t.Fatal("job_run_event redeclares the Record system created_at column")
		}
		if field.Key == "job_run_id" && (field.Type != "relation" || field.Validation.Target != "job_run") {
			t.Fatalf("job_run_event relation = %#v", field)
		}
	}
}

func TestSchedulerSchemaConstructors(t *testing.T) {
	object := schedulerObject("key", "Name", "Description", nil)
	field := schedulerField("field", "Field", "text", true)
	selectField := schedulerSelect("status", "Status", false, "one", "two")
	relation := schedulerRelation("parent", "Parent", "target", true)
	if object.Key != "key" || field.Key != "field" || !field.Required || len(selectField.Validation.Options) != 2 || relation.Validation.Target != "target" || relation.Type != "relation" {
		t.Fatalf("constructors = %#v %#v %#v %#v", object, field, selectField, relation)
	}
}
