package projection

import definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

// SchedulerSystemObjects contains only Runtime-owned record timers. Recurrence
// state, runs, run events and dead letters are owned by domainry-scheduler.
func SchedulerSystemObjects() []definitionmodel.ObjectSchema {
	return []definitionmodel.ObjectSchema{schedulerTimerObject(), recordTimerEventObject()}
}

func schedulerTimerObject() definitionmodel.ObjectSchema {
	object := schedulerObject("record_timer", "Record Timer", "Durable record-scoped action and workflow timers.", []definitionmodel.FieldSchema{
		schedulerField("timer_key", "Timer Key", "text", true),
		schedulerField("object_key", "Object Key", "text", true),
		schedulerField("record_id", "Record ID", "text", true),
		schedulerField("purpose", "Purpose", "text", true),
		schedulerSelect("status", "Status", true, "scheduled", "leased", "fired", "cancelled", "superseded", "failed"),
		schedulerSelect("schedule_mode", "Schedule Mode", true, "absolute", "relative_field", "business_calendar"),
		schedulerField("due_at", "Due At", "datetime", true),
		schedulerField("source_field", "Source Field", "text", false),
		schedulerField("offset_seconds", "Offset Seconds", "number", false),
		schedulerField("timezone", "Timezone", "text", true),
		schedulerField("business_calendar_key", "Business Calendar Key", "text", false),
		schedulerSelect("target_type", "Target Type", true, "action", "workflow"),
		schedulerField("target_key", "Target Key", "text", true),
		schedulerField("payload_json", "Payload JSON", "long_text", false),
		schedulerField("priority", "Priority", "number", true),
		schedulerField("sequence", "Sequence", "number", true),
		schedulerField("lease_owner", "Lease Owner", "text", false),
		schedulerField("lease_expires_at", "Lease Expires At", "datetime", false),
		schedulerField("fencing_token", "Fencing Token", "number", true),
		schedulerField("attempt", "Attempt", "number", true),
		schedulerField("max_attempts", "Max Attempts", "number", true),
		schedulerField("retry_delay_seconds", "Retry Delay Seconds", "number", true),
		schedulerField("retry_max_delay_seconds", "Retry Max Delay Seconds", "number", true),
		schedulerField("last_error", "Last Error", "long_text", false),
		schedulerField("failed_at", "Failed At", "datetime", false),
		schedulerField("supersedes_timer_id", "Supersedes Timer", "text", false),
		schedulerField("fired_at", "Fired At", "datetime", false),
		schedulerField("cancelled_at", "Cancelled At", "datetime", false),
	})
	for index := range object.Fields {
		switch object.Fields[index].Key {
		case "timer_key", "object_key", "record_id", "purpose":
			// These stable identifiers form one workspace-scoped composite index.
			// Their explicit bound keeps the exact unique key portable to InnoDB
			// without prefix-index semantics or hashed collision risk.
			object.Fields[index].Config["max_length"] = 128
			if object.Fields[index].Key == "object_key" || object.Fields[index].Key == "record_id" {
				object.Fields[index].Config["indexed"] = true
			}
		case "status", "due_at", "priority", "sequence":
			object.Fields[index].Config["indexed"] = true
		}
	}
	object.Validations = []definitionmodel.ValidationSchema{{
		Key: "record_timer_identity", Type: "composite_unique", Fields: []string{"timer_key", "object_key", "record_id", "purpose"},
	}}
	return object
}

func recordTimerEventObject() definitionmodel.ObjectSchema {
	object := schedulerObject("record_timer_event", "Record Timer Event", "Immutable execution evidence for a record-scoped timer.", []definitionmodel.FieldSchema{
		schedulerRelation("record_timer_id", "Record Timer", "record_timer", true),
		schedulerSelect("event_type", "Event Type", true, "claimed", "succeeded", "retry_scheduled", "failed", "requeued", "resolved"),
		schedulerField("attempt", "Attempt", "number", true),
		schedulerField("fencing_token", "Fencing Token", "number", true),
		schedulerField("worker_id", "Worker ID", "text", false),
		schedulerField("error_code", "Error Code", "text", false),
		schedulerField("message", "Message", "long_text", false),
		schedulerField("next_due_at", "Next Due At", "datetime", false),
	})
	object.Config["append_only"] = true
	for index := range object.Fields {
		if object.Fields[index].Key == "record_timer_id" || object.Fields[index].Key == "event_type" {
			object.Fields[index].Config["indexed"] = true
		}
	}
	return object
}

func schedulerObject(key, name, description string, fields []definitionmodel.FieldSchema) definitionmodel.ObjectSchema {
	return definitionmodel.ObjectSchema{Key: key, Name: name, Description: description, Fields: fields, UX: map[string]any{}, Config: map[string]any{"runtime_owned": true, "system_kind": "record_timer", "record_timer_runtime": true}}
}

func schedulerField(key, name, fieldType string, required bool) definitionmodel.FieldSchema {
	return definitionmodel.FieldSchema{Key: key, Name: name, Type: fieldType, Required: required, Config: map[string]any{}}
}

func schedulerSelect(key, name string, required bool, options ...string) definitionmodel.FieldSchema {
	field := schedulerField(key, name, "select", required)
	field.Validation.Options = append([]string(nil), options...)
	return field
}

func schedulerRelation(key, name, target string, required bool) definitionmodel.FieldSchema {
	field := schedulerField(key, name, "relation", required)
	field.Validation.Target = target
	return field
}
