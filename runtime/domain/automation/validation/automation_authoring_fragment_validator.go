package validation

import (
	"fmt"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
)

// AutomationValidateTriggerShape validates the context-free portion of an
// Automation trigger. Object and field references remain the responsibility of
// the full definition validator and the published reference resolvers.
func AutomationValidateTriggerShape(trigger automationmodel.AutomationTriggerSchema) error {
	if trigger.Phase != "before" && trigger.Phase != "after" {
		return definitionError(apperror.KindBadRequest, "backend.automation.phase_invalid", nil)
	}
	switch trigger.Operation {
	case "create", "update", "delete", "transition":
	default:
		return definitionError(apperror.KindBadRequest, "backend.automation.operation_invalid", nil)
	}
	return AutomationValidateTriggerFilterShape(trigger)
}

// AutomationValidateTriggerFilterShape keeps operation/filter compatibility
// reusable for callers that validate filters independently from phase.
func AutomationValidateTriggerFilterShape(trigger automationmodel.AutomationTriggerSchema) error {
	if trigger.Operation != "update" && len(trigger.ChangedFields) > 0 {
		return definitionError(apperror.KindBadRequest, "backend.automation.changed_fields_operation_invalid", nil, "operation", trigger.Operation)
	}
	if trigger.Operation != "transition" && (strings.TrimSpace(trigger.FromState) != "" || strings.TrimSpace(trigger.ToState) != "") {
		return definitionError(apperror.KindBadRequest, "backend.automation.transition_filter_operation_invalid", nil, "operation", trigger.Operation)
	}
	return nil
}

// AutomationValidateExecutionPolicyShape validates execution controls that do
// not depend on a Runtime object catalog.
func AutomationValidateExecutionPolicyShape(execution automationmodel.AutomationExecutionPolicy) error {
	if execution.Mode != "" && execution.Mode != "sync" && execution.Mode != "async" {
		return definitionError(apperror.KindBadRequest, "backend.automation.execution_mode_invalid", nil, "mode", execution.Mode)
	}
	if execution.RunAs != "" && execution.RunAs != "initiator" {
		return definitionError(apperror.KindBadRequest, "backend.automation.run_as_invalid", nil, "run_as", execution.RunAs)
	}
	switch strings.TrimSpace(execution.ResultNotification) {
	case "", "none", "failures", "all":
	default:
		return definitionError(apperror.KindBadRequest, "backend.automation.result_notification_invalid", nil, "result_notification", execution.ResultNotification)
	}
	return nil
}

// AutomationValidateInstructionShape is shared by fragment authoring and the
// full rule validator, preventing the two entrypoints from drifting.
func AutomationValidateInstructionShape(phase string, instruction automationmodel.AutomationInstructionSchema) error {
	if strings.TrimSpace(instruction.Key) == "" {
		return definitionError(apperror.KindBadRequest, "backend.automation.instruction_key_invalid", nil, "instruction", instruction.Key)
	}
	if phase == "before" && instruction.Type != "derive_fields" && instruction.Type != "assert" {
		return definitionError(apperror.KindBadRequest, "backend.automation.before_instruction_unsupported", nil, "instruction", instruction.Key, "type", instruction.Type)
	}
	if phase == "after" && instruction.Type != "invoke_business_action" && instruction.Type != "emit_event" && instruction.Type != "start_workflow" {
		return definitionError(apperror.KindBadRequest, "backend.automation.after_instruction_unsupported", nil, "instruction", instruction.Key, "type", instruction.Type)
	}
	switch instruction.Type {
	case "invoke_business_action":
		if value := strings.TrimSpace(fmt.Sprint(instruction.Config["action_key"])); value == "" || value == "<nil>" {
			return definitionError(apperror.KindBadRequest, "backend.automation.business_action_key_required", nil, "instruction", instruction.Key)
		}
	case "start_workflow":
		if value := strings.TrimSpace(fmt.Sprint(instruction.Config["workflow_key"])); value == "" || value == "<nil>" {
			return definitionError(apperror.KindBadRequest, "backend.automation.workflow_key_required", nil, "instruction", instruction.Key)
		}
	case "derive_fields":
		if _, ok := instruction.Config["fields"].(map[string]any); !ok {
			return definitionError(apperror.KindBadRequest, "backend.automation.derive_fields_required", nil, "instruction", instruction.Key)
		}
	case "assert":
		if value := strings.TrimSpace(fmt.Sprint(instruction.Config["source"])); value == "" || value == "<nil>" {
			return definitionError(apperror.KindBadRequest, "backend.automation.assert_source_required", nil, "instruction", instruction.Key)
		}
	}
	return nil
}
