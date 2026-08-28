package policy

import (
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func ActionName(action definitionmodel.ActionSchema) string {
	_, name := definitionmodel.ActionPermissionSubject(action)
	return name
}

func ActionSplitPermission(value string) (string, string) {
	parts := strings.Split(strings.TrimSpace(value), ".")
	if len(parts) < 2 {
		return "", strings.TrimSpace(value)
	}
	return strings.TrimSpace(parts[len(parts)-2]), strings.TrimSpace(parts[len(parts)-1])
}

func ActionFirstPresent(data map[string]any, keys ...string) (any, bool) {
	if data == nil {
		return nil, false
	}
	for _, key := range keys {
		if value, ok := data[key]; ok {
			return value, true
		}
	}
	return nil, false
}

func ActionFirstString(data map[string]any, keys ...string) string {
	value, ok := ActionFirstPresent(data, keys...)
	if !ok || actionIsEmptyValue(value) {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func ActionPipelineInt(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case json.Number:
		parsed, err := typed.Int64()
		return int(parsed), err == nil
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		return parsed, err == nil
	default:
		return 0, false
	}
}

func ActionApplyPipelineCompletionFields(itemData map[string]any, toStage recordmodel.Record, actionData map[string]any, transitionedAt string, reopening bool) {
	if reopening {
		itemData["completed_at"] = ""
		itemData["failure_reason"] = ""
		status, exists := itemData["status"]
		if !exists || ActionRecordValueEmpty(status) || ActionPipelineTerminalStatus(fmt.Sprint(status)) {
			itemData["status"] = "open"
		}
		return
	}
	stageType := strings.TrimSpace(fmt.Sprint(toStage.Data["stage_type"]))
	if stageType == "" || stageType == "<nil>" {
		stageType = "open"
	}
	if status := strings.TrimSpace(fmt.Sprint(itemData["status"])); status == "" || status == "<nil>" || status == "open" {
		itemData["status"] = stageType
	}
	if !ActionPipelineStageTerminal(toStage) {
		if !ActionPipelineTerminalStatus(fmt.Sprint(itemData["status"])) {
			itemData["completed_at"] = ""
			itemData["failure_reason"] = ""
		}
		return
	}
	if strings.TrimSpace(fmt.Sprint(itemData["completed_at"])) == "" || strings.TrimSpace(fmt.Sprint(itemData["completed_at"])) == "<nil>" {
		itemData["completed_at"] = transitionedAt
	}
	if ActionPipelineStageFailed(toStage) {
		reason := strings.TrimSpace(ActionFirstString(actionData, "failure_reason", "reason", "lost_reason", "cancel_reason"))
		if reason == "" {
			reason = stageType
		}
		itemData["failure_reason"] = reason
	}
}

func ActionPipelineStageTerminal(stage recordmodel.Record) bool {
	if value, ok := pipelineBool(stage.Data["is_terminal"]); ok && value {
		return true
	}
	return ActionPipelineTerminalStatus(fmt.Sprint(stage.Data["stage_type"]))
}

func ActionPipelineStageFailed(stage recordmodel.Record) bool {
	if value, ok := pipelineBool(stage.Data["is_failed"]); ok && value {
		return true
	}
	return ActionPipelineFailedStatus(fmt.Sprint(stage.Data["stage_type"]))
}

func ActionPipelineTerminalStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case "won", "lost", "done", "cancelled", "failed":
		return true
	default:
		return false
	}
}

func ActionPipelineFailedStatus(status string) bool {
	switch strings.TrimSpace(status) {
	case "lost", "cancelled", "failed":
		return true
	default:
		return false
	}
}

func ActionPipelineFloat(value any) (float64, bool) {
	switch typed := value.(type) {
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case float64:
		return typed, true
	case json.Number:
		parsed, err := typed.Float64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func ActionSplitCSV(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" || value == "<nil>" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

func pipelineBool(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		parsed, err := strconv.ParseBool(strings.TrimSpace(typed))
		return parsed, err == nil
	default:
		return false, false
	}
}
