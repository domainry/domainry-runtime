package record

import (
	"context"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
)

// RecordStateMachineEffectDependencies contains only the pure self-patch
// reducer. Cross-object writes, Workflow dispatch and Audit are Business Action
// responsibilities and are intentionally absent from this contract.
type RecordStateMachineEffectDependencies struct {
	ApplySelfPatch func(context.Context, map[string]any, map[string]any, string, principalmodel.Principal) bool
}

// RecordStateMachineEffectApplicationService lowers declarative transition
// self-effects into the candidate Record before the canonical MutationPlan is
// produced. It performs no I/O and owns no commit boundary.
type RecordStateMachineEffectApplicationService struct {
	dependencies RecordStateMachineEffectDependencies
}

func NewRecordStateMachineEffectApplicationService(dependencies RecordStateMachineEffectDependencies) *RecordStateMachineEffectApplicationService {
	return &RecordStateMachineEffectApplicationService{dependencies: dependencies}
}

func (s *RecordStateMachineEffectApplicationService) ApplySelfEffects(ctx context.Context, object definitionmodel.ObjectSchema, before, next map[string]any, recordID string, principal principalmodel.Principal) (bool, error) {
	if before == nil {
		return false, nil
	}
	changed := false
	for _, validation := range object.Validations {
		if strings.TrimSpace(validation.Type) != "state_machine" || !recordvalidation.RecordValidationBlocks(validation) {
			continue
		}
		fieldKey := strings.TrimSpace(validation.FieldKey)
		if fieldKey == "" {
			continue
		}
		from := stateMachineEffectString(before[fieldKey])
		to := stateMachineEffectString(next[fieldKey])
		if from == "" || to == "" || from == to {
			continue
		}
		transition, allowed := recordvalidation.RecordStateMachineTransition(validation, from, to)
		if allowed && s.dependencies.ApplySelfPatch != nil && s.dependencies.ApplySelfPatch(ctx, transition, next, recordID, principal) {
			changed = true
		}
	}
	return changed, nil
}

func stateMachineEffectString(value any) string {
	if value == nil {
		return ""
	}
	text, _ := value.(string)
	return strings.TrimSpace(text)
}
