package service

import (
	actionpolicy "github.com/domainry/domainry-runtime/runtime/domain/action/policy"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"fmt"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"strings"
)

func ActionApplyTransitionSelfPatch(transition map[string]any, data map[string]any, sourceRecordID string, principal principalmodel.Principal) bool {
	changed := applySelfPatchMap(mapValue(transition["patch"]), data, sourceRecordID, principal)
	if applySelfPatchMap(mapValue(transition["self_patch"]), data, sourceRecordID, principal) {
		changed = true
	}
	for _, effect := range actionpolicy.ActionTransitionEffects(transition) {
		effectMap := mapValue(effect)
		effectType := actionpolicy.ActionNormalizedValue(effectMap["type"])
		if effectType == "" {
			effectType = actionpolicy.ActionNormalizedValue(effectMap["kind"])
		}
		if effectType != "patch_self" && effectType != "update_self" && effectType != "set_fields" {
			continue
		}
		patch := mapValue(effectMap["patch"])
		if len(patch) == 0 {
			if field := actionpolicy.ActionNormalizedValue(effectMap["field"]); field != "" {
				patch = map[string]any{field: effectMap["value"]}
			}
		}
		if applySelfPatchMap(patch, data, sourceRecordID, principal) {
			changed = true
		}
	}
	return changed
}

func applySelfPatchMap(patch, data map[string]any, sourceRecordID string, principal principalmodel.Principal) bool {
	changed := false
	source := recordmodel.Record{ID: sourceRecordID, Data: data}
	for key, value := range patch {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		rendered := ActionRenderValue(value, source, principal)
		if strings.TrimSpace(fmt.Sprint(data[key])) != strings.TrimSpace(fmt.Sprint(rendered)) {
			data[key] = rendered
			changed = true
		}
	}
	return changed
}
