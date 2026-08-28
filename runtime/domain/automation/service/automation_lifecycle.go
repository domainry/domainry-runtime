package service

import (
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	recordcontract "github.com/domainry/domainry-runtime/runtime/domain/record/contract"

	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
)

func MutationVersion(before map[string]any, record recordmodel.Record) string {
	payload, _ := json.Marshal(map[string]any{"before": before, "after": record.Data})
	sum := sha256.Sum256(payload)
	return fmt.Sprintf("%s-%x", valueOrDefault(strings.TrimSpace(record.UpdatedAt), "unknown"), sum[:8])
}

func LifecycleEventPayload(event automationmodel.AutomationLifecycleEvent) map[string]any {
	return map[string]any{
		"id": event.ID, "rule_key": event.RuleKey, "object_key": event.ObjectKey, "operation": event.Operation,
		"record_id": event.RecordID, "record_version": event.RecordVersion, "before": recordcontract.RecordCloneData(event.Before),
		"record":        map[string]any{"id": event.Record.ID, "data": recordcontract.RecordCloneData(event.Record.Data), "created_at": event.Record.CreatedAt, "updated_at": event.Record.UpdatedAt},
		"actor_user_id": event.ActorUserID, "actor_role_key": event.ActorRoleKey, "request_id": event.RequestID,
		"correlation_id": event.CorrelationID, "causation_id": event.CausationID, "identity_policy": event.IdentityPolicy,
		"automation_depth": event.AutomationDepth, "visited_rule_keys": append([]string(nil), event.VisitedRuleKeys...), "occurred_at": event.OccurredAt,
	}
}

func LifecycleEventFromPayload(payload map[string]any) automationmodel.AutomationLifecycleEvent {
	recordMap := mapFromAny(payload["record"])
	return automationmodel.AutomationLifecycleEvent{
		ID: strings.TrimSpace(fmt.Sprint(payload["id"])), RuleKey: strings.TrimSpace(fmt.Sprint(payload["rule_key"])),
		ObjectKey: strings.TrimSpace(fmt.Sprint(payload["object_key"])), Operation: strings.TrimSpace(fmt.Sprint(payload["operation"])),
		RecordID: strings.TrimSpace(fmt.Sprint(payload["record_id"])), RecordVersion: strings.TrimSpace(fmt.Sprint(payload["record_version"])),
		Before: mapFromAny(payload["before"]), Record: recordmodel.Record{ID: strings.TrimSpace(fmt.Sprint(recordMap["id"])), Data: mapFromAny(recordMap["data"]), CreatedAt: strings.TrimSpace(fmt.Sprint(recordMap["created_at"])), UpdatedAt: strings.TrimSpace(fmt.Sprint(recordMap["updated_at"]))},
		ActorUserID: strings.TrimSpace(fmt.Sprint(payload["actor_user_id"])), ActorRoleKey: strings.TrimSpace(fmt.Sprint(payload["actor_role_key"])),
		RequestID: strings.TrimSpace(fmt.Sprint(payload["request_id"])), CorrelationID: strings.TrimSpace(fmt.Sprint(payload["correlation_id"])),
		CausationID: strings.TrimSpace(fmt.Sprint(payload["causation_id"])), IdentityPolicy: strings.TrimSpace(fmt.Sprint(payload["identity_policy"])),
		AutomationDepth: intFromAny(payload["automation_depth"]), VisitedRuleKeys: stringSliceFromAny(payload["visited_rule_keys"]),
		OccurredAt: strings.TrimSpace(fmt.Sprint(payload["occurred_at"])),
	}
}

func intFromAny(value any) int {
	switch typed := value.(type) {
	case int:
		return typed
	case float64:
		return int(typed)
	default:
		return 0
	}
}

func stringSliceFromAny(value any) []string {
	values, _ := value.([]any)
	if direct, ok := value.([]string); ok {
		return append([]string(nil), direct...)
	}
	result := make([]string, 0, len(values))
	for _, item := range values {
		if text := strings.TrimSpace(fmt.Sprint(item)); text != "" {
			result = append(result, text)
		}
	}
	return result
}

func ProtocolValueMatchesType(value any, fieldType string) bool {
	if value == nil {
		return true
	}
	switch strings.TrimSpace(fieldType) {
	case "text", "long_text", "date", "datetime", "file":
		_, ok := value.(string)
		return ok
	case "integer":
		switch typed := value.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64:
			return true
		case float64:
			return typed == float64(int64(typed))
		default:
			return false
		}
	case "decimal":
		switch value.(type) {
		case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64:
			return true
		default:
			return false
		}
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "json":
		return true
	default:
		return false
	}
}

func valueOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}

func mapFromAny(value any) map[string]any {
	if value == nil {
		return map[string]any{}
	}
	if typed, ok := value.(map[string]any); ok {
		return recordcontract.RecordCloneData(typed)
	}
	payload, err := json.Marshal(value)
	if err != nil {
		return map[string]any{}
	}
	result := map[string]any{}
	if json.Unmarshal(payload, &result) != nil {
		return map[string]any{}
	}
	return result
}
