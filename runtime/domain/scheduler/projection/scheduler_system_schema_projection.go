package projection

import definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

// SchedulerSystemObjects is the canonical schema for runtime-owned Scheduler
// state. Published definitions live exclusively in the versioned Metadata
// scheduler resource and are therefore intentionally absent here.
func SchedulerSystemObjects() []definitionmodel.ObjectSchema {
	return []definitionmodel.ObjectSchema{
		schedulerObject("scheduler_cursor", "Scheduler Cursor", "Runtime-owned cursor for one published scheduler definition.", []definitionmodel.FieldSchema{
			schedulerField("scheduler_definition_key", "Scheduler Definition Key", "text", true),
			schedulerField("next_run_at", "Next Run At", "datetime", false),
			schedulerField("last_run_at", "Last Run At", "datetime", false),
			schedulerField("last_run_status", "Last Run Status", "text", false),
		}),
		schedulerRunObject(schedulerObject("job_run", "Job Run", "Runtime-owned scheduler executions.", []definitionmodel.FieldSchema{
			schedulerField("scheduler_definition_key", "Scheduler Definition Key", "text", true),
			schedulerSelect("status", "Status", true, "queued", "leased", "running", "succeeded", "failed", "retrying", "cancelled", "dead_letter"),
			schedulerSelect("triggered_by", "Triggered By", true, "scheduler", "manual", "manual_run", "api", "workflow", "retry"),
			schedulerField("scheduled_for", "Scheduled For", "datetime", true), schedulerField("started_at", "Started At", "datetime", false),
			schedulerField("finished_at", "Finished At", "datetime", false), schedulerField("lease_owner", "Lease Owner", "text", false),
			schedulerField("lease_expires_at", "Lease Expires At", "datetime", false), schedulerField("fencing_token", "Fencing Token", "number", false), schedulerField("attempt", "Attempt", "number", true),
			schedulerField("max_attempts", "Max Attempts", "number", true), schedulerField("timeout_seconds", "Timeout Seconds", "number", false),
			schedulerField("next_retry_at", "Next Retry At", "datetime", false), schedulerField("retry_backoff", "Retry Backoff", "text", false),
			schedulerField("retry_delay_seconds", "Retry Delay Seconds", "number", false), schedulerField("retry_max_delay_seconds", "Max Retry Delay Seconds", "number", false),
			schedulerField("retry_backoff_seconds", "Retry Backoff Seconds", "number", false), schedulerField("idempotency_scope", "Idempotency Scope", "text", false), schedulerField("idempotency_key", "Idempotency Key", "text", false),
			schedulerField("last_command_scope", "Last Command Scope", "text", false), schedulerField("last_command_key", "Last Command Key", "text", false),
			schedulerField("workflow_key", "Workflow Key", "text", false), schedulerField("workflow_execution_id", "Workflow Execution ID", "text", false),
			schedulerField("target_object", "Target Object", "text", false), schedulerField("target_record_id", "Target Record ID", "text", false),
			schedulerField("payload_json", "Payload JSON", "long_text", false), schedulerField("result_json", "Result JSON", "long_text", false),
			schedulerField("checkpoint_cursor", "Checkpoint Cursor", "long_text", false), schedulerField("checkpoint_processed", "Checkpoint Processed", "number", false),
			schedulerField("error_message", "Error Message", "long_text", false), schedulerField("error_category", "Error Category", "text", false),
			schedulerField("recoverability", "Recoverability", "text", false),
		})),
		schedulerObject("job_run_event", "Job Run Event", "Runtime-owned scheduler audit events.", []definitionmodel.FieldSchema{
			schedulerRelation("job_run_id", "Job Run", "job_run", true),
			schedulerSelect("event_type", "Event Type", true, "created", "lease_acquired", "checkpoint_saved", "definition_cursor_advanced", "state_changed", "simulated", "workflow_triggered", "action_triggered", "report_query_run_created", "report_export_audit_created", "download_task_created", "retry_scheduled", "dead_lettered", "dead_letter_resolved", "cancelled"),
			schedulerField("message", "Message", "long_text", false), schedulerField("metadata_json", "Metadata JSON", "long_text", false),
		}),
		schedulerObject("job_dead_letter", "Job Dead Letter", "Runtime-owned exhausted scheduler runs.", []definitionmodel.FieldSchema{
			schedulerRelation("job_run_id", "Job Run", "job_run", true), schedulerField("scheduler_definition_key", "Scheduler Definition Key", "text", true),
			schedulerSelect("status", "Status", true, "open", "retrying", "resolved", "ignored"), schedulerField("reason", "Reason", "long_text", true),
			schedulerField("last_error", "Last Error", "long_text", false), schedulerField("failed_at", "Failed At", "datetime", true),
			schedulerField("resolved_at", "Resolved At", "datetime", false), schedulerField("resolved_by", "Resolved By", "text", false),
			schedulerField("resolution_note", "Resolution Note", "long_text", false), schedulerField("resolution_idempotency_key", "Resolution Idempotency Key", "text", false),
		}),
		schedulerTimerObject(),
	}
}

func schedulerRunObject(object definitionmodel.ObjectSchema) definitionmodel.ObjectSchema {
	for index := range object.Fields {
		switch object.Fields[index].Key {
		case "scheduler_definition_key", "scheduled_for", "status", "lease_expires_at", "next_retry_at":
			object.Fields[index].Config["indexed"] = true
		}
	}
	object.Validations = []definitionmodel.ValidationSchema{{
		Key: "scheduler_run_window", Type: "composite_unique", Fields: []string{"scheduler_definition_key", "scheduled_for"},
	}}
	return object
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

func schedulerObject(key, name, description string, fields []definitionmodel.FieldSchema) definitionmodel.ObjectSchema {
	return definitionmodel.ObjectSchema{Key: key, Name: name, Description: description, Fields: fields, UX: map[string]any{}, Config: map[string]any{"runtime_owned": true, "system_kind": "scheduler"}}
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
