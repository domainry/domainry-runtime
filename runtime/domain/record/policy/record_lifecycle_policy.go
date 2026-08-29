package policy

import (
	"fmt"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func RecordAutomationTransitionCandidate(before, candidate map[string]any) bool {
	for _, key := range []string{"status", "state", "current_stage"} {
		if _, presentBefore := before[key]; presentBefore && fmt.Sprint(before[key]) != fmt.Sprint(candidate[key]) {
			return true
		}
	}
	return false
}

func RecordValidateSchedulerOperationalCRUD(object definitionmodel.ObjectSchema, operation string) error {
	if !boolFromAny(object.Config["scheduler_runtime"]) && !boolFromAny(object.Config["record_timer_runtime"]) {
		return nil
	}
	return lifecycleError(apperror.KindForbidden, "backend.scheduler.runtime_api_required", "object", object.Key, "operation", operation)
}

func RecordUsesSoftDelete(object definitionmodel.ObjectSchema) bool {
	if RecordLifecycleMode(object) == definitionmodel.ObjectLifecycleSoftDeleteOnly {
		return recordLifecycleFieldExists(object, "status") && recordLifecycleFieldExists(object, "deleted_at") && recordLifecycleFieldExists(object, "deleted_by")
	}
	return recordLifecycleFieldExists(object, "status") && recordLifecycleFieldExists(object, "deleted_at") && recordLifecycleFieldExists(object, "deleted_by")
}

func RecordLifecycleMode(object definitionmodel.ObjectSchema) string {
	if object.LifecyclePolicy == nil || strings.TrimSpace(object.LifecyclePolicy.Mode) == "" {
		return definitionmodel.ObjectLifecycleMutable
	}
	return strings.TrimSpace(object.LifecyclePolicy.Mode)
}

func RecordRequiresSoftDelete(object definitionmodel.ObjectSchema) bool {
	return RecordLifecycleMode(object) == definitionmodel.ObjectLifecycleSoftDeleteOnly
}

// RecordValidateLifecycleMutation applies the same object lifecycle contract
// to every caller of the canonical mutation planner.
func RecordValidateLifecycleMutation(object definitionmodel.ObjectSchema, operation string, before map[string]any) error {
	operation = strings.TrimSpace(operation)
	switch RecordLifecycleMode(object) {
	case definitionmodel.ObjectLifecycleMutable, definitionmodel.ObjectLifecycleSoftDeleteOnly:
		return nil
	case definitionmodel.ObjectLifecycleAppendOnly:
		if operation != "create" {
			return lifecycleError(apperror.KindConflict, "backend.record.lifecycle_append_only", "object", object.Key, "operation", operation)
		}
	case definitionmodel.ObjectLifecycleImmutableAfterState:
		if operation == "create" {
			return nil
		}
		stateField := strings.TrimSpace(object.LifecyclePolicy.StateField)
		current := strings.TrimSpace(fmt.Sprint(before[stateField]))
		for _, immutable := range object.LifecyclePolicy.ImmutableStates {
			if current == strings.TrimSpace(immutable) {
				return lifecycleError(apperror.KindConflict, "backend.record.lifecycle_state_immutable", "object", object.Key, "operation", operation, "state_field", stateField, "state", current)
			}
		}
	}
	return nil
}

func RecordRestoreStatus(object definitionmodel.ObjectSchema) string {
	for _, value := range []any{object.Config["restore_status"], object.Config["active_status"]} {
		text := strings.TrimSpace(fmt.Sprint(value))
		if text != "" && text != "<nil>" {
			return text
		}
	}
	return "active"
}

func RecordRelationDeletePolicy(field definitionmodel.FieldSchema) string {
	policy := strings.TrimSpace(fmt.Sprint(field.Config["on_delete"]))
	if policy == "" || policy == "<nil>" {
		return "restrict"
	}
	return policy
}

func lifecycleError(kind apperror.ErrorKind, code string, params ...string) error {
	values := map[string]string{}
	for index := 0; index+1 < len(params); index += 2 {
		if key := strings.TrimSpace(params[index]); key != "" {
			values[key] = params[index+1]
		}
	}
	if len(values) == 0 {
		values = nil
	}
	return &apperror.AppError{Kind: kind, Code: code, Params: values}
}

func boolFromAny(value any) bool {
	switch typed := value.(type) {
	case bool:
		return typed
	case string:
		return strings.EqualFold(strings.TrimSpace(typed), "true")
	default:
		return false
	}
}

func recordLifecycleFieldExists(object definitionmodel.ObjectSchema, key string) bool {
	for _, field := range object.Fields {
		if field.Key == key {
			return true
		}
	}
	return false
}
