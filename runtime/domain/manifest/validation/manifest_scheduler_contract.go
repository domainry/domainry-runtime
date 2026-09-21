package validation

import (
	"context"
	"fmt"
	"strings"

	actionpolicy "github.com/domainry/domainry-runtime/runtime/domain/action/policy"
	schedulersdk "github.com/domainry/domainry-scheduler-sdk"
	"github.com/domainry/domainry-scheduler-sdk/schedule"
)

func (state *validationState) validateSchedulerOwnershipAndDefinitions() {
	for index, object := range state.manifest.Objects {
		key := strings.TrimSpace(object.Key)
		if schedulersdk.OwnsManifestObjectKey(key) {
			state.add(fmt.Sprintf("objects[%d].key", index), "owner-managed Scheduler object %q must not be declared in a Runtime manifest", key)
		}
	}
	for index, seed := range state.manifest.SeedRecords {
		key := strings.TrimSpace(seed.ObjectKey)
		if schedulersdk.OwnsManifestObjectKey(key) {
			state.add(fmt.Sprintf("seed_records[%d].object_key", index), "Scheduler-owned operational state %q cannot be seeded by Runtime", key)
		}
	}

	seen := map[string]int{}
	for index, definition := range state.manifest.SchedulerDefinitions {
		path := fmt.Sprintf("scheduler_definitions[%d]", index)
		key := manifestSchedulerString(definition, "key")
		if key == "" {
			state.add(path+".key", "is required")
		} else if first, duplicate := seen[key]; duplicate {
			state.add(path+".key", "duplicate Scheduler definition key %q; first declared at scheduler_definitions[%d]", key, first)
		} else {
			seen[key] = index
		}
		if err := schedule.ValidateDefinitionData(context.Background(), definition); err != nil {
			code := schedule.ValidationCode(err)
			if code == "" {
				code = err.Error()
			}
			state.add(path, "violates Scheduler authoring contract: %s", code)
			continue
		}
		state.validateSchedulerBusinessCalendarReference(path, definition)
		state.validateSchedulerTargetReference(path, definition)
	}
}

func (state *validationState) validateSchedulerBusinessCalendarReference(path string, definition map[string]any) {
	calendarKey := manifestSchedulerString(definition, "business_calendar_key")
	if calendarKey == "" {
		return
	}
	for _, calendar := range state.manifest.BusinessCalendars {
		if strings.TrimSpace(calendar.Key) != calendarKey {
			continue
		}
		if timezone := manifestSchedulerString(definition, "timezone"); timezone != strings.TrimSpace(calendar.Timezone) {
			state.add(path+".timezone", "backend.scheduler.business_calendar_timezone_mismatch: calendar %q uses %q", calendarKey, calendar.Timezone)
		}
		return
	}
	state.add(path+".business_calendar_key", "backend.scheduler.business_calendar_not_found: %q", calendarKey)
}

func (state *validationState) validateSchedulerTargetReference(path string, definition map[string]any) {
	targetType := strings.ToLower(manifestSchedulerString(definition, "target_type"))
	targetKey := manifestSchedulerString(definition, "target_key")
	switch targetType {
	case "business_action":
		objectKey := manifestSchedulerString(definition, "target_object")
		action, found := state.actions[targetKey]
		if !found {
			state.add(path+".target_key", "references unknown Business Action %q", targetKey)
		} else {
			if strings.TrimSpace(action.ObjectKey) != objectKey {
				state.add(path+".target_object", "must match Business Action %q object %q", targetKey, action.ObjectKey)
			}
			if !actionpolicy.ActionIsObjectKind(action.Kind) {
				state.add(path+".target_key", "Business Action %q must be object-scoped because Scheduler targets do not carry a record identity", targetKey)
			}
		}
		roleKey := manifestSchedulerString(definition, "run_as_role")
		var roleFound bool
		for _, role := range state.manifest.Roles {
			if strings.TrimSpace(role.Key) != roleKey {
				continue
			}
			roleFound = true
			if strings.TrimSpace(role.Audience) != "service" || strings.TrimSpace(role.AssignmentMode) != "system_managed" {
				state.add(path+".run_as_role", "role %q must be a service role with system_managed assignment", roleKey)
			}
			permitted := false
			for _, permission := range role.Permissions {
				if strings.TrimSpace(permission.PermissionKey) == targetKey {
					permitted = true
					break
				}
			}
			if !permitted {
				state.add(path+".run_as_role", "service role %q does not grant Business Action %q", roleKey, targetKey)
			}
			break
		}
		if !roleFound {
			state.add(path+".run_as_role", "references unknown service role %q", roleKey)
		}
	case "workflow":
		workflowKey := strings.TrimPrefix(targetKey, "scheduled:")
		if workflowKey == "*" {
			return
		}
		for _, workflow := range state.manifest.Workflows {
			if strings.TrimSpace(workflow.Key) != workflowKey {
				continue
			}
			if !workflow.Enabled || workflow.TriggerContract == nil || strings.TrimSpace(workflow.TriggerContract.Type) != "scheduled" {
				state.add(path+".target_key", "workflow %q must be enabled with trigger_contract.type scheduled", workflowKey)
			}
			return
		}
		state.add(path+".target_key", "references unknown workflow %q", workflowKey)
	case "report_snapshot_refresh":
		for _, report := range state.manifest.Reports {
			if strings.TrimSpace(report.Key) == targetKey {
				return
			}
		}
		state.add(path+".target_key", "references unknown report %q", targetKey)
	case "http":
		connectionKey := manifestSchedulerString(definition, "connection_key")
		for _, connection := range state.manifest.Integrations.Connections {
			if strings.TrimSpace(connection.Key) == connectionKey {
				return
			}
		}
		state.add(path+".connection_key", "references unknown Integration connection %q", connectionKey)
	}
}

func manifestSchedulerString(data map[string]any, key string) string {
	value := strings.TrimSpace(fmt.Sprint(data[key]))
	if value == "<nil>" {
		return ""
	}
	return value
}
