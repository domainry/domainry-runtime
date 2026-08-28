package projection

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	changeplanpolicy "github.com/domainry/domainry-runtime/runtime/domain/changeplan/policy"
)

func BuildChangePlanBusinessDiff(item changeplanmodel.BusinessSystemChangeItem, snapshot changeplanmodel.Snapshot, graph changeplanmodel.ReferenceGraph) changeplanmodel.BusinessChangeDiff {
	resourceType := businessReferenceResourceType(item.ResourceType)
	diff := changeplanmodel.BusinessChangeDiff{
		ItemID: item.ItemID, ResourceType: item.ResourceType, ResourceKey: item.ResourceKey, Operation: item.Operation,
		BusinessSummary: ChangePlanBusinessSummary(item), Changes: []changeplanmodel.BusinessPropertyChange{}, RiskSignals: []string{},
		ReferenceImpact: ChangePlanReferenceImpact(graph, resourceType, item.ResourceKey),
		AffectedRecords: snapshot.ObjectRecordCounts[businessChangeObjectKey(item)],
	}
	diff.NormalizedBefore, diff.NormalizedAfter = normalizeBusinessChangeJSON(item.Before), normalizeBusinessChangeJSON(item.After)
	before, after := businessChangeJSONValue(diff.NormalizedBefore), businessChangeJSONValue(diff.NormalizedAfter)
	diff.Changes = collectBusinessPropertyChanges("", before, after)
	diff.RiskSignals = businessChangeRiskSignals(item, snapshot, diff.ReferenceImpact)
	return diff
}

func ChangePlanBusinessSummary(item changeplanmodel.BusinessSystemChangeItem) string {
	verbs := map[string]string{"create": "Create", "update": "Update", "archive": "Archive", "delete": "Delete", "noop": "Keep"}
	resourceNames := map[string]string{"object": "domain object", "field": "domain field", "role": "role", "permission": "permission", "data_scope": "data scope", "field_permission": "field permission", "menu": "menu", "action": "domain action", "workflow": "workflow", "automation_rule": "automation rule", "scheduler_job": "scheduled task", "connector": "connector", "report": "report", "frontend_surface": "frontend feature"}
	verb := verbs[item.Operation]
	if verb == "" {
		verb = "Change"
	}
	resourceType := businessReferenceResourceType(item.ResourceType)
	resourceName := resourceNames[resourceType]
	if resourceName == "" {
		resourceName = strings.ReplaceAll(resourceType, "_", " ")
	}
	displayName := businessChangeDisplayName(item)
	if displayName != "" && displayName != item.ResourceKey {
		return fmt.Sprintf("%s %s %s (%s)", verb, resourceName, displayName, item.ResourceKey)
	}
	return fmt.Sprintf("%s %s %s", verb, resourceName, item.ResourceKey)
}

func businessChangeDisplayName(item changeplanmodel.BusinessSystemChangeItem) string {
	for _, payload := range []json.RawMessage{item.After, item.Before} {
		values := businessChangeJSONMap(payload)
		for _, key := range []string{"name", "label", "title"} {
			if value := strings.TrimSpace(stringValue(values[key])); value != "" {
				return value
			}
		}
	}
	return ""
}

func normalizeBusinessChangeJSON(value json.RawMessage) json.RawMessage {
	if !rawJSONPresent(value) {
		return nil
	}
	var decoded any
	if json.Unmarshal(value, &decoded) != nil {
		return append(json.RawMessage(nil), value...)
	}
	normalized, _ := json.Marshal(decoded)
	return normalized
}

func businessChangeJSONValue(value json.RawMessage) any {
	if !rawJSONPresent(value) {
		return nil
	}
	var decoded any
	_ = json.Unmarshal(value, &decoded)
	return decoded
}

func rawJSONPresent(value json.RawMessage) bool {
	trimmed := strings.TrimSpace(string(value))
	return trimmed != "" && trimmed != "null"
}

func collectBusinessPropertyChanges(path string, before, after any) []changeplanmodel.BusinessPropertyChange {
	if reflect.DeepEqual(before, after) {
		return []changeplanmodel.BusinessPropertyChange{}
	}
	beforeMap, beforeIsMap := before.(map[string]any)
	afterMap, afterIsMap := after.(map[string]any)
	if beforeIsMap && afterIsMap {
		keys := map[string]bool{}
		for key := range beforeMap {
			keys[key] = true
		}
		for key := range afterMap {
			keys[key] = true
		}
		ordered := make([]string, 0, len(keys))
		for key := range keys {
			ordered = append(ordered, key)
		}
		sort.Strings(ordered)
		changes := []changeplanmodel.BusinessPropertyChange{}
		for _, key := range ordered {
			childPath := key
			if path != "" {
				childPath = path + "." + key
			}
			changes = append(changes, collectBusinessPropertyChanges(childPath, beforeMap[key], afterMap[key])...)
		}
		return changes
	}
	kind := "replace"
	if before == nil {
		kind = "add"
	}
	if after == nil {
		kind = "remove"
	}
	if path == "" {
		path = "$"
	}
	return []changeplanmodel.BusinessPropertyChange{{Path: path, Kind: kind, Before: before, After: after}}
}

func businessChangeRiskSignals(item changeplanmodel.BusinessSystemChangeItem, snapshot changeplanmodel.Snapshot, impact changeplanmodel.ReferenceImpact) []string {
	signals := map[string]bool{}
	resourceType := businessReferenceResourceType(item.ResourceType)
	if resourceType == "field" && item.Operation == "update" && (businessFieldTypeChanged(item) || businessFieldBecameRequired(item)) {
		signals["data_backfill_required"] = true
		if snapshot.ObjectRecordCounts[businessChangeObjectKey(item)] > 0 {
			signals["existing_records_impact"] = true
		}
	}
	if businessChangePermissionExpanded(item) {
		signals["permission_expansion"] = true
	}
	if businessChangePermissionReduced(item) {
		signals["permission_reduction"] = true
	}
	if item.Operation == "archive" || item.Operation == "delete" || item.ChangeKind == "breaking" || item.ChangeKind == "destructive" {
		signals["business_interruption"] = true
	}
	if strings.TrimSpace(item.FrontendSupportKey) != "" {
		signals["frontend_compatibility"] = true
	}
	for _, process := range snapshot.RuntimeState.RunningWorkflowProcesses {
		if resourceType == "workflow" && process.WorkflowKey == item.ResourceKey {
			signals["workflow_running_instances"] = true
		}
	}
	for _, edge := range append(append([]changeplanmodel.ReferenceEdge{}, impact.DirectConsumers...), impact.IndirectConsumers...) {
		switch edge.FromType {
		case "scheduler_job", "scheduler_run", "scheduler_dead_letter":
			signals["scheduler_execution_impact"] = true
		case "outbox_message":
			signals["outbox_delivery_impact"] = true
		case "frontend_surface", "view":
			signals["frontend_compatibility"] = true
		case "seed_record":
			signals["seed_data_impact"] = true
		}
	}
	result := make([]string, 0, len(signals))
	for signal := range signals {
		result = append(result, signal)
	}
	sort.Strings(result)
	return result
}

func businessChangeObjectKey(item changeplanmodel.BusinessSystemChangeItem) string {
	resourceType := businessReferenceResourceType(item.ResourceType)
	if resourceType == "object" {
		return item.ResourceKey
	}
	if resourceType != "field" {
		return ""
	}
	if objectKey, _, ok := strings.Cut(item.ResourceKey, "."); ok {
		return objectKey
	}
	for _, payload := range []json.RawMessage{item.After, item.Before} {
		values := businessChangeJSONMap(payload)
		for _, key := range []string{"object_key", "_definition_object_key"} {
			if value := strings.TrimSpace(stringValue(values[key])); value != "" {
				return value
			}
		}
	}
	return ""
}

func businessChangePermissionExpanded(item changeplanmodel.BusinessSystemChangeItem) bool {
	return businessChangeStringSetDelta(item, true)
}

func businessChangePermissionReduced(item changeplanmodel.BusinessSystemChangeItem) bool {
	return businessChangeStringSetDelta(item, false)
}

func businessChangeStringSetDelta(item changeplanmodel.BusinessSystemChangeItem, expansion bool) bool {
	resourceType := businessReferenceResourceType(item.ResourceType)
	if !strings.Contains(resourceType, "permission") && resourceType != "role" && resourceType != "data_scope" {
		return false
	}
	before, after := flattenBusinessChangeStrings(businessChangeJSONValue(item.Before)), flattenBusinessChangeStrings(businessChangeJSONValue(item.After))
	left, right := before, after
	if !expansion {
		left, right = after, before
	}
	for value := range right {
		if !left[value] {
			return true
		}
	}
	return false
}

func businessReferenceResourceType(resourceType string) string {
	return changeplanpolicy.ChangePlanCanonicalResourceType(resourceType)
}

func flattenBusinessChangeStrings(value any) map[string]bool {
	result := map[string]bool{}
	var visit func(any)
	visit = func(current any) {
		switch typed := current.(type) {
		case string:
			result[typed] = true
		case []any:
			for _, item := range typed {
				visit(item)
			}
		case map[string]any:
			for _, item := range typed {
				visit(item)
			}
		}
	}
	visit(value)
	return result
}
