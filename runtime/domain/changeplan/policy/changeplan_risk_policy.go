package policy

import (
	"encoding/json"
	"reflect"
	"strings"
)

func ChangePlanExpectedChangeKind(operation, resourceType string, before, after json.RawMessage) string {
	switch operation {
	case "create":
		return "additive"
	case "archive", "delete":
		return "destructive"
	case "update":
		if changePlanFieldTypeChanged(resourceType, before, after) {
			return "destructive"
		}
		if changePlanFieldBecameRequired(resourceType, before, after) || changePlanFieldOptionsShrank(resourceType, before, after) {
			return "breaking"
		}
	}
	return ""
}

func ChangePlanSensitiveUpdate(operation, resourceType string, before, after json.RawMessage) bool {
	if operation != "update" {
		return false
	}
	switch strings.TrimSpace(resourceType) {
	case "role", "permission", "data_scope", "field_permission", "menu", "workflow",
		"identity.role", "identity.role_permission", "identity.role_data_scope", "identity.role_field_permission", "identity.menu":
		return true
	default:
		return changePlanFieldTypeChanged(resourceType, before, after) || changePlanFieldBecameRequired(resourceType, before, after) || changePlanFieldOptionsShrank(resourceType, before, after)
	}
}

// ChangePlanResourceOwnerForSourceKind separates storage provenance from the
// finite governance owner vocabulary used by system drafts.
func ChangePlanResourceOwnerForSourceKind(sourceKind string) string {
	switch strings.ToLower(strings.TrimSpace(sourceKind)) {
	case "builder", "builder_v4", "model", "agent":
		return "builder"
	case "manual", "user", "human", "admin":
		return "manual"
	case "platform", "system", "runtime":
		return "platform"
	case "plugin":
		return "plugin"
	case "template", "generated", "manifest", "package", "seed":
		return "template"
	default:
		return "unknown"
	}
}

func changePlanFieldTypeChanged(resourceType string, beforeRaw, afterRaw json.RawMessage) bool {
	if strings.TrimSpace(resourceType) != "field" {
		return false
	}
	before, after := changePlanJSONMap(beforeRaw), changePlanJSONMap(afterRaw)
	beforeType, afterType := strings.TrimSpace(changePlanString(before["type"])), strings.TrimSpace(changePlanString(after["type"]))
	return beforeType != "" && afterType != "" && beforeType != afterType
}

func changePlanFieldBecameRequired(resourceType string, beforeRaw, afterRaw json.RawMessage) bool {
	if strings.TrimSpace(resourceType) != "field" {
		return false
	}
	before, after := changePlanJSONMap(beforeRaw), changePlanJSONMap(afterRaw)
	beforeRequired, beforeOK := before["required"].(bool)
	afterRequired, afterOK := after["required"].(bool)
	return beforeOK && afterOK && !beforeRequired && afterRequired
}

func changePlanFieldOptionsShrank(resourceType string, beforeRaw, afterRaw json.RawMessage) bool {
	if strings.TrimSpace(resourceType) != "field" {
		return false
	}
	before, after := changePlanJSONMap(beforeRaw), changePlanJSONMap(afterRaw)
	beforeOptions, beforeOK := before["options"].([]any)
	afterOptions, afterOK := after["options"].([]any)
	return beforeOK && afterOK && len(afterOptions) < len(beforeOptions) && !reflect.DeepEqual(beforeOptions, afterOptions)
}

func changePlanJSONMap(payload json.RawMessage) map[string]any {
	values := map[string]any{}
	_ = json.Unmarshal(payload, &values)
	return values
}

func changePlanString(value any) string {
	text, _ := value.(string)
	return text
}
