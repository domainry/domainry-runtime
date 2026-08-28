package record

import (
	"context"
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestRecordStateMachineApplySelfEffectsLowersPatchIntoCandidate(t *testing.T) {
	called := 0
	service := NewRecordStateMachineEffectApplicationService(RecordStateMachineEffectDependencies{
		ApplySelfPatch: func(_ context.Context, transition, candidate map[string]any, recordID string, principal principalmodel.Principal) bool {
			called++
			if recordID != "order-1" || principal.UserID != "user-1" {
				t.Fatalf("unexpected context record=%q principal=%#v", recordID, principal)
			}
			patch, _ := transition["self_patch"].(map[string]any)
			for key, value := range patch {
				candidate[key] = value
			}
			return len(patch) > 0
		},
	})
	object := stateMachineSelfEffectObject()
	candidate := map[string]any{"status": "approved"}
	changed, err := service.ApplySelfEffects(t.Context(), object, map[string]any{"status": "draft"}, candidate, "order-1", principalmodel.Principal{Principal: identitysdk.Principal{UserID: "user-1"}})
	if err != nil || !changed || called != 1 || candidate["reviewed"] != true {
		t.Fatalf("changed=%t called=%d candidate=%#v err=%v", changed, called, candidate, err)
	}
}

func TestRecordStateMachineApplySelfEffectsSkipsNonTransitionInputs(t *testing.T) {
	service := NewRecordStateMachineEffectApplicationService(RecordStateMachineEffectDependencies{
		ApplySelfPatch: func(context.Context, map[string]any, map[string]any, string, principalmodel.Principal) bool {
			t.Fatal("self patch must not run")
			return false
		},
	})
	tests := []struct {
		name   string
		object definitionmodel.ObjectSchema
		before map[string]any
		next   map[string]any
	}{
		{name: "nil before", object: stateMachineSelfEffectObject(), next: map[string]any{"status": "approved"}},
		{name: "same state", object: stateMachineSelfEffectObject(), before: map[string]any{"status": "draft"}, next: map[string]any{"status": "draft"}},
		{name: "unknown transition", object: stateMachineSelfEffectObject(), before: map[string]any{"status": "approved"}, next: map[string]any{"status": "archived"}},
		{name: "non state validation", object: definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{{Type: "required"}}}, before: map[string]any{"status": "draft"}, next: map[string]any{"status": "approved"}},
		{name: "non blocking state validation", object: definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{{Type: "state_machine", Severity: "warning", FieldKey: "status"}}}, before: map[string]any{"status": "draft"}, next: map[string]any{"status": "approved"}},
		{name: "empty field", object: definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{{Type: "state_machine", FieldKey: " "}}}, before: map[string]any{"status": "draft"}, next: map[string]any{"status": "approved"}},
		{name: "empty from", object: stateMachineSelfEffectObject(), before: map[string]any{"status": ""}, next: map[string]any{"status": "approved"}},
		{name: "empty to", object: stateMachineSelfEffectObject(), before: map[string]any{"status": "draft"}, next: map[string]any{"status": ""}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			changed, err := service.ApplySelfEffects(t.Context(), test.object, test.before, test.next, "order-1", principalmodel.Principal{})
			if err != nil || changed {
				t.Fatalf("changed=%t err=%v", changed, err)
			}
		})
	}
}

func TestStateMachineEffectStringNil(t *testing.T) {
	if got := stateMachineEffectString(nil); got != "" {
		t.Fatalf("value=%q", got)
	}
}

func TestRecordStateMachineApplySelfEffectsWithoutReducerIsNoop(t *testing.T) {
	service := NewRecordStateMachineEffectApplicationService(RecordStateMachineEffectDependencies{})
	changed, err := service.ApplySelfEffects(t.Context(), stateMachineSelfEffectObject(), map[string]any{"status": "draft"}, map[string]any{"status": "approved"}, "order-1", principalmodel.Principal{})
	if err != nil || changed {
		t.Fatalf("changed=%t err=%v", changed, err)
	}
}

func TestRecordStateMachineApplySelfEffectsReducerDeclinesPatch(t *testing.T) {
	service := NewRecordStateMachineEffectApplicationService(RecordStateMachineEffectDependencies{
		ApplySelfPatch: func(context.Context, map[string]any, map[string]any, string, principalmodel.Principal) bool {
			return false
		},
	})
	changed, err := service.ApplySelfEffects(t.Context(), stateMachineSelfEffectObject(), map[string]any{"status": "draft"}, map[string]any{"status": "approved"}, "order-1", principalmodel.Principal{})
	if err != nil || changed {
		t.Fatalf("changed=%t err=%v", changed, err)
	}
}

func stateMachineSelfEffectObject() definitionmodel.ObjectSchema {
	return definitionmodel.ObjectSchema{Validations: []definitionmodel.ValidationSchema{{
		Type: "state_machine", FieldKey: "status", Config: map[string]any{
			"transitions": []any{map[string]any{"from": "draft", "to": "approved", "self_patch": map[string]any{"reviewed": true}}},
		},
	}}}
}
