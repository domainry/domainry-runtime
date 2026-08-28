package service

import (
	"testing"

	identitysdk "github.com/domainry/domainry-identity-sdk"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func TestActionApplyTransitionSelfPatchComposesLegacyAndEffectForms(t *testing.T) {
	data := map[string]any{"status": "draft", "unchanged": "same"}
	transition := map[string]any{
		"patch":      map[string]any{"status": "draft"},
		"self_patch": map[string]any{"owner_id": "$principal.user_id", " ": "ignored"},
		"effects": []any{
			"invalid",
			map[string]any{"type": "notify", "field": "ignored", "value": true},
			map[string]any{"type": "patch_self", "patch": map[string]any{"record_id": "$record.id", "unchanged": "same"}},
			map[string]any{"kind": "update_self", "field": "priority", "value": "high"},
			map[string]any{"type": "set_fields", "field": " ", "value": "ignored"},
		},
	}
	principal := principalmodel.Principal{Principal: identitysdk.Principal{UserID: "user-1"}}
	if !ActionApplyTransitionSelfPatch(transition, data, "record-1", principal) {
		t.Fatal("transition patch did not report changes")
	}
	for key, want := range map[string]any{"status": "draft", "owner_id": "user-1", "record_id": "record-1", "priority": "high", "unchanged": "same"} {
		if data[key] != want {
			t.Fatalf("data[%s]=%#v want=%#v data=%#v", key, data[key], want, data)
		}
	}
	if _, exists := data[""]; exists {
		t.Fatalf("blank field was applied: %#v", data)
	}
}

func TestActionApplyTransitionSelfPatchReportsNoChange(t *testing.T) {
	data := map[string]any{"status": "ready"}
	if ActionApplyTransitionSelfPatch(map[string]any{"patch": map[string]any{"status": " ready "}}, data, "record-1", principalmodel.Principal{}) {
		t.Fatalf("whitespace-equivalent patch reported a change: %#v", data)
	}
}
