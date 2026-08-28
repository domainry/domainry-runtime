package service

import (
	actionpolicy "github.com/domainry/domainry-runtime/runtime/domain/action/policy"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
)

func ActionPatch(_ definitionmodel.ActionSchema, object definitionmodel.ObjectSchema, _ recordmodel.Record, data map[string]any, _ principalmodel.Principal) map[string]any {
	return ActionDataPatch(object, data)
}

func ActionDataPatch(object definitionmodel.ObjectSchema, data map[string]any) map[string]any {
	fields := make(map[string]bool, len(object.Fields))
	for _, field := range object.Fields {
		fields[field.Key] = true
	}
	patch := map[string]any{}
	for key, value := range data {
		key = strings.TrimSpace(key)
		if key != "" && key != "expected_version" && key != "expectedVersion" && key != "version" && fields[key] {
			patch[key] = value
		}
	}
	return patch
}

func ActionPreserveExplicitEmptyPatch(object definitionmodel.ObjectSchema, patch, normalized map[string]any) map[string]any {
	if len(patch) == 0 {
		return normalized
	}
	fields := make(map[string]bool, len(object.Fields))
	for _, field := range object.Fields {
		fields[field.Key] = true
	}
	out := cloneMap(normalized)
	for key, value := range patch {
		key = strings.TrimSpace(key)
		if fields[key] && actionpolicy.ActionRecordValueEmpty(value) {
			out[key] = ""
		}
	}
	return out
}

func ActionRenderRecordData(spec map[string]any, record recordmodel.Record, principal principalmodel.Principal) map[string]any {
	return ActionRenderRecordDataWithInput(spec, record, nil, principal)
}

func ActionRenderRecordDataWithInput(spec map[string]any, record recordmodel.Record, input map[string]any, principal principalmodel.Principal) map[string]any {
	out := map[string]any{}
	for key, value := range spec {
		if key = strings.TrimSpace(key); key != "" {
			out[key] = ActionRenderValueWithInput(value, record, input, principal)
		}
	}
	return out
}

func ActionRenderRelatedRecordData(spec map[string]any, record, source recordmodel.Record, principal principalmodel.Principal) map[string]any {
	out := map[string]any{}
	for key, value := range spec {
		if key = strings.TrimSpace(key); key != "" {
			out[key] = ActionRenderRelatedValue(value, record, source, principal)
		}
	}
	return out
}

func ActionRenderValue(value any, record recordmodel.Record, principal principalmodel.Principal) any {
	return ActionRenderValueWithInput(value, record, nil, principal)
}

func ActionRenderValueWithInput(value any, record recordmodel.Record, input map[string]any, principal principalmodel.Principal) any {
	text, ok := value.(string)
	if !ok {
		return value
	}
	switch {
	case strings.HasPrefix(text, "$input."):
		if input == nil {
			return nil
		}
		return input[strings.TrimPrefix(text, "$input.")]
	case text == "$document.ai_summary":
		return ActionDocumentAISummary(record)
	case text == "$document.ai_answer":
		return ActionDocumentAIAnswer(record, input)
	case text == "$record.id":
		return record.ID
	case text == "$record.created_at":
		return record.CreatedAt
	case text == "$record.updated_at":
		return record.UpdatedAt
	case text == "$record.version_group_id_or_id":
		if groupID := actionpolicy.ActionNormalizedValue(record.Data["version_group_id"]); groupID != "" {
			return groupID
		}
		return record.ID
	case text == "$record.version_plus_one":
		current, ok := integerValue(record.Data["version"])
		if !ok {
			current = 1
		}
		return current + 1
	case strings.HasPrefix(text, "$record."):
		return record.Data[strings.TrimPrefix(text, "$record.")]
	case text == "$principal.user_id":
		return principal.UserID
	case text == "$principal.role_key":
		return principal.RoleKey
	case text == "$now":
		return time.Now().UTC().Format(time.RFC3339)
	case text == "$today":
		return time.Now().UTC().Format("2006-01-02")
	default:
		return value
	}
}

func cleanProjectionValue(value any) string {
	result := strings.TrimSpace(fmt.Sprint(value))
	if result == "<nil>" {
		return ""
	}
	return result
}

func ActionRenderRelatedValue(value any, record, source recordmodel.Record, principal principalmodel.Principal) any {
	text, ok := value.(string)
	if !ok {
		return value
	}
	switch {
	case text == "$source.id":
		return source.ID
	case text == "$source.created_at":
		return source.CreatedAt
	case text == "$source.updated_at":
		return source.UpdatedAt
	case strings.HasPrefix(text, "$source."):
		return source.Data[strings.TrimPrefix(text, "$source.")]
	default:
		return ActionRenderValue(value, record, principal)
	}
}

func ActionDocumentAISummary(record recordmodel.Record) string {
	parts := []string{}
	for _, item := range []struct{ label, key string }{{"title", "title"}, {"file", "file_name"}, {"type", "document_type"}, {"status", "status"}, {"approval", "approval_status"}, {"version", "version"}} {
		if value := actionpolicy.ActionNormalizedValue(record.Data[item.key]); value != "" {
			parts = append(parts, item.label+" "+value)
		}
	}
	if len(parts) == 0 {
		return "This document has no readable metadata yet. Upload or sync the source file, then request a new summary."
	}
	return "Document summary: " + strings.Join(parts, "; ") + "."
}

func ActionDocumentAIAnswer(record recordmodel.Record, input map[string]any) string {
	question := actionpolicy.ActionNormalizedValue(input["question"])
	if question == "" {
		question = "Summarize the key points and risks in this document."
	}
	return ActionDocumentAISummary(record) + " Answered question: " + question
}

func integerValue(value any) (int, bool) {
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
