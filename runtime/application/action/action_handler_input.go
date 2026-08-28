package action

import (
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

// businessHandlerInput projects the governed Runtime payload back onto the
// published business input contract. Runtime-only invocation metadata such as
// idempotency_key may participate in assurance, fingerprinting, or system
// operations, but it is not part of a generated Business Handler input unless
// the Action explicitly declares that field.
func businessHandlerInput(action definitionmodel.ActionSchema, payload map[string]any) map[string]any {
	input := make(map[string]any, len(action.PayloadFields))
	for _, field := range action.PayloadFields {
		key := strings.TrimSpace(field.Key)
		if key == "" {
			continue
		}
		if value, ok := payload[key]; ok {
			input[key] = value
		}
	}
	return input
}
