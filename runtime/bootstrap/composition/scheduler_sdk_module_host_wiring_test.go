package composition

import (
	"testing"

	schedulerapplication "github.com/domainry/domainry-runtime/runtime/application/scheduler"
)

func TestSchedulerSDKDefinitionPreservesCalendarAndRoutesModuleHTTPThroughRuntime(t *testing.T) {
	definition := schedulerSDKDefinition(schedulerapplication.PublishedDefinition{Key: "partner_sync", UpdatedAt: "2026-08-29T12:00:00Z", Data: map[string]any{
		"name": "Partner sync", "status": "enabled", "schedule_type": "weekly_at", "time_of_day": "09:30", "day_of_week": "monday", "timezone": "Asia/Shanghai",
		"target_type": "http", "target_key": "sync", "connection_key": "partner_primary", "operation": "sync", "dispatch_mode": "direct", "payload_json": `{"full":true}`,
	}})
	if definition.Schedule.TimeOfDay != "09:30" || definition.Schedule.DayOfWeek != "monday" {
		t.Fatalf("schedule=%#v", definition.Schedule)
	}
	if definition.Target.Type != "http" || definition.Target.ConnectionKey != "partner_primary" || definition.Target.Operation != "sync" || definition.Target.DispatchMode != "runtime_callback" {
		t.Fatalf("target=%#v", definition.Target)
	}
}

func TestSchedulerSDKDefinitionMapsRuntimeOwnerWithoutEmbeddingBusinessLogic(t *testing.T) {
	definition := schedulerSDKDefinition(schedulerapplication.PublishedDefinition{Key: "daily", Data: map[string]any{"status": "enabled", "schedule_type": "interval", "interval_seconds": 60, "target_type": "workflow", "target_key": "scheduled:daily", "payload_json": `{}`}})
	if definition.Target.Type != "runtime_operation" || definition.Target.Owner != "workflow" || definition.Target.Operation != "scheduled:daily" {
		t.Fatalf("target=%#v", definition.Target)
	}
}
