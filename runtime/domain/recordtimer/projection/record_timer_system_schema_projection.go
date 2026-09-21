package projection

import definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

// RecordTimerSystemObjects contains the Runtime-owned record timer lifecycle.
func RecordTimerSystemObjects() []definitionmodel.ObjectSchema {
	return []definitionmodel.ObjectSchema{recordTimerObject(), recordTimerEventObject()}
}

func recordTimerObject() definitionmodel.ObjectSchema {
	object := recordTimerSystemObject("record_timer", "Record Timer", "Durable record-scoped action and workflow timers.", []definitionmodel.FieldSchema{
		recordTimerField("timer_key", "Timer Key", "text", true),
		recordTimerField("object_key", "Object Key", "text", true),
		recordTimerField("record_id", "Record ID", "text", true),
		recordTimerField("purpose", "Purpose", "text", true),
		recordTimerSelect("status", "Status", true, "scheduled", "leased", "fired", "cancelled", "superseded", "failed"),
		recordTimerSelect("schedule_mode", "Schedule Mode", true, "absolute", "relative_field", "business_calendar"),
		recordTimerField("due_at", "Due At", "datetime", true),
		recordTimerField("source_field", "Source Field", "text", false),
		recordTimerField("offset_seconds", "Offset Seconds", "number", false),
		recordTimerField("timezone", "Timezone", "text", true),
		recordTimerField("business_calendar_key", "Business Calendar Key", "text", false),
		recordTimerField("business_calendar_revision", "Business Calendar Revision", "text", false),
		recordTimerSelect("target_type", "Target Type", true, "action", "workflow"),
		recordTimerField("target_key", "Target Key", "text", true),
		recordTimerField("payload_json", "Payload JSON", "long_text", false),
		recordTimerField("priority", "Priority", "number", true),
		recordTimerField("sequence", "Sequence", "number", true),
		recordTimerField("lease_owner", "Lease Owner", "text", false),
		recordTimerField("lease_expires_at", "Lease Expires At", "datetime", false),
		recordTimerField("fencing_token", "Fencing Token", "number", true),
		recordTimerField("attempt", "Attempt", "number", true),
		recordTimerField("max_attempts", "Max Attempts", "number", true),
		recordTimerField("retry_delay_seconds", "Retry Delay Seconds", "number", true),
		recordTimerField("retry_max_delay_seconds", "Retry Max Delay Seconds", "number", true),
		recordTimerField("last_error", "Last Error", "long_text", false),
		recordTimerField("failed_at", "Failed At", "datetime", false),
		recordTimerField("supersedes_timer_id", "Supersedes Timer", "text", false),
		recordTimerField("fired_at", "Fired At", "datetime", false),
		recordTimerField("cancelled_at", "Cancelled At", "datetime", false),
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
	object := recordTimerSystemObject("record_timer_event", "Record Timer Event", "Immutable execution evidence for a record-scoped timer.", []definitionmodel.FieldSchema{
		recordTimerRelation("record_timer_id", "Record Timer", "record_timer", true),
		recordTimerSelect("event_type", "Event Type", true, "claimed", "succeeded", "retry_scheduled", "failed", "requeued", "resolved"),
		recordTimerField("attempt", "Attempt", "number", true),
		recordTimerField("fencing_token", "Fencing Token", "number", true),
		recordTimerField("worker_id", "Worker ID", "text", false),
		recordTimerField("error_code", "Error Code", "text", false),
		recordTimerField("message", "Message", "long_text", false),
		recordTimerField("next_due_at", "Next Due At", "datetime", false),
	})
	object.Config["append_only"] = true
	for index := range object.Fields {
		if object.Fields[index].Key == "record_timer_id" || object.Fields[index].Key == "event_type" {
			object.Fields[index].Config["indexed"] = true
		}
	}
	return object
}

func recordTimerSystemObject(key, name, description string, fields []definitionmodel.FieldSchema) definitionmodel.ObjectSchema {
	return definitionmodel.ObjectSchema{Key: key, Name: name, Description: description, Fields: fields, UX: map[string]any{}, Config: map[string]any{"runtime_owned": true, "system_kind": "record_timer", "record_timer_runtime": true}}
}

func recordTimerField(key, name, fieldType string, required bool) definitionmodel.FieldSchema {
	return definitionmodel.FieldSchema{Key: key, Name: name, Type: fieldType, Required: required, Config: map[string]any{}}
}

func recordTimerSelect(key, name string, required bool, options ...string) definitionmodel.FieldSchema {
	field := recordTimerField(key, name, "select", required)
	field.Validation.Options = append([]string(nil), options...)
	return field
}

func recordTimerRelation(key, name, target string, required bool) definitionmodel.FieldSchema {
	field := recordTimerField(key, name, "relation", required)
	field.Validation.Target = target
	return field
}
