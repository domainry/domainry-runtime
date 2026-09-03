package validation

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
)

// AutomationValidateAuthoringFragment applies Automation-owned, context-free
// semantic validation to one published leaf capability payload. References to
// workspace objects, fields, actions and workflows are intentionally resolved
// by their published reference contracts or by full-rule validation.
func AutomationValidateAuthoringFragment(capabilityKey string, value map[string]any) error {
	capabilityKey = strings.TrimSpace(capabilityKey)
	value = AutomationAuthoringFragmentWithDefaults(capabilityKey, value)
	switch capabilityKey {
	case "automation.trigger":
		var trigger automationmodel.AutomationTriggerSchema
		if err := automationDecodeAuthoringFragment(value, &trigger); err != nil {
			return err
		}
		return AutomationValidateTriggerShape(trigger)
	case "automation.condition_group":
		var conditions automationmodel.AutomationConditionGroup
		if err := automationDecodeAuthoringFragment(value, &conditions); err != nil {
			return err
		}
		return AutomationValidateConditionGroup(conditions, "conditions", 0)
	case "automation.execution_policy":
		var execution automationmodel.AutomationExecutionPolicy
		if err := automationDecodeAuthoringFragment(value, &execution); err != nil {
			return err
		}
		return AutomationValidateExecutionPolicyShape(execution)
	case "automation.instruction.derive_fields", "automation.instruction.assert",
		"automation.instruction.invoke_business_action", "automation.instruction.start_workflow",
		"automation.instruction.emit_event":
		var instruction automationmodel.AutomationInstructionSchema
		if err := automationDecodeAuthoringFragment(value, &instruction); err != nil {
			return err
		}
		phase := "after"
		if instruction.Type == "derive_fields" || instruction.Type == "assert" {
			phase = "before"
		}
		return AutomationValidateInstructionShape(phase, instruction)
	default:
		return &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.automation.authoring_capability_unsupported", Params: map[string]string{"capability_key": capabilityKey}}
	}
}

// AutomationAuthoringFragmentWithDefaults materializes values determined by
// the selected capability route. Callers author instruction intent only; the
// route already identifies the instruction type.
func AutomationAuthoringFragmentWithDefaults(capabilityKey string, value map[string]any) map[string]any {
	normalized := make(map[string]any, len(value)+1)
	for key, item := range value {
		normalized[key] = item
	}
	if strings.HasPrefix(strings.TrimSpace(capabilityKey), "automation.instruction.") {
		normalized["type"] = strings.TrimPrefix(strings.TrimSpace(capabilityKey), "automation.instruction.")
	}
	return normalized
}

func automationDecodeAuthoringFragment(value map[string]any, target any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.automation.authoring_fragment_invalid", Err: err}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.automation.authoring_fragment_invalid", Err: err}
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return &apperror.AppError{Kind: apperror.KindBadRequest, Code: "backend.automation.authoring_fragment_invalid", Err: err}
	}
	return nil
}
