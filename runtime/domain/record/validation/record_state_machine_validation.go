package validation

import (
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"strings"

	"github.com/domainry/domainry-foundation/apperror"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func RecordValidateStateMachinePolicies(object definitionmodel.ObjectSchema, before, next map[string]any, principal principalmodel.Principal) error {
	if before == nil {
		return nil
	}
	for _, validation := range object.Validations {
		if strings.TrimSpace(validation.Type) != "state_machine" || !validationBlocks(validation) {
			continue
		}
		if err := RecordValidateStateMachineEffectContract(validation); err != nil {
			return err
		}
		fieldKey := strings.TrimSpace(validation.FieldKey)
		if fieldKey == "" {
			continue
		}
		from := policyConfigString(before[fieldKey])
		to := policyConfigString(next[fieldKey])
		if from == "" || to == "" || from == to {
			continue
		}
		transition, allowed := RecordStateMachineTransition(validation, from, to)
		if !allowed {
			return stateMachineError(apperror.KindBadRequest, messageCode(validation.Message, "backend.transition.invalid_transition"), "field", fieldKey, "from", from, "to", to)
		}
		if err := validateStructuredTransition(transition, next, principal); err != nil {
			return err
		}
	}
	return nil
}

// RecordValidateStateMachineEffectContract permits only self patches that are
// lowered into the parent MutationPlan. Cross-object writes and orchestration
// must be expressed by a published Business Action.
func RecordValidateStateMachineEffectContract(validation definitionmodel.ValidationSchema) error {
	for _, transition := range policyMapSlice(validation.Config["transitions"]) {
		for _, key := range []string{"patch", "self_patch"} {
			if value, exists := transition[key]; exists {
				if _, ok := value.(map[string]any); !ok {
					return stateMachineError(apperror.KindBadRequest, "backend.transition.self_patch_invalid", "field", key)
				}
			}
		}
		for _, effect := range RecordTransitionEffects(transition) {
			mapped, ok := effect.(map[string]any)
			if !ok {
				return stateMachineError(apperror.KindBadRequest, "backend.transition.effect_requires_action")
			}
			effectType := strings.TrimSpace(policyConfigString(mapped["type"]))
			if effectType == "" {
				effectType = strings.TrimSpace(policyConfigString(mapped["kind"]))
			}
			switch effectType {
			case "patch_self", "update_self", "set_fields":
			default:
				return stateMachineError(apperror.KindBadRequest, "backend.transition.effect_requires_action", "effect", effectType)
			}
		}
	}
	return nil
}

func RecordTransitionEffects(transition map[string]any) []any {
	if len(transition) == 0 {
		return nil
	}
	if raw, ok := transition["effects"].([]any); ok {
		return append([]any(nil), raw...)
	}
	if raw, ok := transition["effects"].([]string); ok {
		out := make([]any, 0, len(raw))
		for _, item := range raw {
			out = append(out, item)
		}
		return out
	}
	if raw, ok := transition["effect"].(map[string]any); ok {
		return []any{raw}
	}
	return nil
}

func RecordStateMachineTransition(validation definitionmodel.ValidationSchema, from, to string) (map[string]any, bool) {
	for _, transition := range policyMapSlice(validation.Config["transitions"]) {
		if transitionEndpointMatches(transition["from"], from) && transitionEndpointMatches(transition["to"], to) {
			return transition, true
		}
	}
	return nil, false
}

func validateStructuredTransition(transition map[string]any, next map[string]any, principal principalmodel.Principal) error {
	for _, requiredField := range transitionRequiredFields(transition) {
		requiredField = strings.TrimSpace(requiredField)
		if requiredField != "" && RecordIsEmptyValue(next[requiredField]) {
			return stateMachineError(apperror.KindBadRequest, "backend.transition.required_field", "field", requiredField)
		}
	}
	permission := transitionRequiredPermission(transition)
	if permission == "" {
		return nil
	}
	objectKey, action := recordSplitPermission(permission)
	if objectKey == "" || action == "" {
		return stateMachineError(apperror.KindBadRequest, "backend.transition.invalid_permission", "permission", permission)
	}
	if !principal.Allows(objectKey, action) {
		return stateMachineError(apperror.KindForbidden, "backend.transition.permission_required", "permission", permission)
	}
	return nil
}

func transitionRequiredFields(transition map[string]any) []string {
	fields := stringListAny(transition["required_fields"])
	if len(fields) == 0 {
		fields = stringListAny(transition["require_fields"])
	}
	return fields
}

func transitionRequiredPermission(transition map[string]any) string {
	permission := policyConfigString(transition["required_permission"])
	if permission == "" {
		permission = policyConfigString(transition["require_permission"])
	}
	return permission
}

func transitionEndpointMatches(value any, current string) bool {
	for _, item := range stringListAny(value) {
		item = strings.TrimSpace(item)
		if item == "*" || item == current {
			return true
		}
	}
	text := policyConfigString(value)
	return text == "*" || text == current
}

func validationBlocks(validation definitionmodel.ValidationSchema) bool {
	severity := strings.ToLower(strings.TrimSpace(validation.Severity))
	return severity != "warning" && severity != "info"
}

func messageCode(message, fallback string) string {
	message = strings.TrimSpace(message)
	if strings.Contains(message, ".") && !strings.ContainsAny(message, " \t\n\r") {
		return message
	}
	return fallback
}

func RecordMessageCode(message, fallback string) string {
	return messageCode(message, fallback)
}

func stateMachineError(kind apperror.ErrorKind, code string, params ...string) error {
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
