package validation

import (
	"fmt"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func validationMapSlice(value any) []map[string]any {
	switch typed := value.(type) {
	case []map[string]any:
		return append([]map[string]any(nil), typed...)
	case []any:
		out := []map[string]any{}
		for _, item := range typed {
			if mapped := validationMap(item); len(mapped) > 0 {
				out = append(out, mapped)
			}
		}
		return out
	default:
		return nil
	}
}

func validationMap(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return typed
	default:
		return map[string]any{}
	}
}

func validationStringList(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := []string{}
		for _, item := range typed {
			text := strings.TrimSpace(fmt.Sprint(item))
			if text != "" && text != "<nil>" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

// Manifest validation owns its publication diagnostic and does not import the
// Record validation implementation across Domain owners.
func manifestStateMachineEffectContractCode(validation definitionmodel.ValidationSchema) string {
	for _, transition := range validationMapSlice(validation.Config["transitions"]) {
		for _, key := range []string{"patch", "self_patch"} {
			if value, exists := transition[key]; exists {
				if _, ok := value.(map[string]any); !ok {
					return "backend.transition.self_patch_invalid"
				}
			}
		}
		for _, effect := range manifestTransitionEffects(transition) {
			mapped, ok := effect.(map[string]any)
			if !ok {
				return "backend.transition.effect_requires_action"
			}
			effectType := cleanManifestReference(mapped["type"])
			if effectType == "" {
				effectType = cleanManifestReference(mapped["kind"])
			}
			switch strings.TrimSpace(effectType) {
			case "patch_self", "update_self", "set_fields":
			default:
				return "backend.transition.effect_requires_action"
			}
		}
	}
	return ""
}

func manifestTransitionEffects(transition map[string]any) []any {
	if effects, ok := transition["effects"].([]any); ok {
		return effects
	}
	if effects, ok := transition["effects"].([]string); ok {
		result := make([]any, len(effects))
		for index := range effects {
			result[index] = effects[index]
		}
		return result
	}
	if effect, ok := transition["effect"].(map[string]any); ok {
		return []any{effect}
	}
	return nil
}
