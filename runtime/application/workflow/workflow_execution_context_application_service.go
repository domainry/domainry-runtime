package workflow

import (
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"

	"context"
	"fmt"
	"strings"
	"time"

	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
)

func workflowWorkerPrincipal() principalmodel.Principal {
	principal := principalmodel.NewSystemPrincipal("workflow:worker", principalmodel.NewSystemScope(principalmodel.SystemScopeRuntimeGlobal, "workflow worker dispatch"))
	principal.WorkspaceID = principalmodel.InstallationWorkspaceID
	return principal
}

func WorkflowWorkerPrincipal() principalmodel.Principal { return workflowWorkerPrincipal() }

func (s *WorkflowApplicationService) workflowPrincipal(ctx context.Context, workflow definitionmodel.WorkflowSchema, initiator principalmodel.Principal) (principalmodel.Principal, error) {
	return NewWorkflowPrincipalResolver(s.principals).ResolveWorkflowPrincipal(ctx, workflow, initiator)
}

func workflowRenderedString(ctx context.Context, value any, payload map[string]any, principal principalmodel.Principal) string {
	rendered := renderWorkflowValue(ctx, value, payload, principal)
	if rendered == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(rendered))
}

func renderWorkflowRecordData(ctx context.Context, spec map[string]any, payload map[string]any, principal principalmodel.Principal) map[string]any {
	out := map[string]any{}
	for key, value := range spec {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[key] = renderWorkflowValue(ctx, value, payload, principal)
	}
	return out
}

func renderWorkflowRecordDataWithSource(ctx context.Context, spec map[string]any, payload map[string]any, source recordmodel.Record, principal principalmodel.Principal) map[string]any {
	out := map[string]any{}
	for key, value := range spec {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		out[key] = renderWorkflowValueWithSource(ctx, value, payload, source, principal)
	}
	return out
}

func renderWorkflowValue(ctx context.Context, value any, payload map[string]any, principal principalmodel.Principal) any {
	return renderWorkflowValueWithSource(ctx, value, payload, recordmodel.Record{}, principal)
}

func renderWorkflowValueWithSource(ctx context.Context, value any, payload map[string]any, source recordmodel.Record, principal principalmodel.Principal) any {
	_ = ctx
	text, ok := value.(string)
	if !ok {
		return value
	}
	if nested, matched := workflowNestedReferenceValue(text, "$workflow.", payload); matched {
		return nested
	}
	switch {
	case text == "$payload.id":
		return payload["record_id"]
	case workflowSimpleTemplateRefKey(text, "$payload.") != "":
		return payload[strings.TrimPrefix(text, "$payload.")]
	case text == "$workflow.id":
		return payload["record_id"]
	case workflowSimpleTemplateRefKey(text, "$workflow.") != "":
		return payload[strings.TrimPrefix(text, "$workflow.")]
	case text == "$record.id":
		return payload["record_id"]
	case workflowSimpleTemplateRefKey(text, "$record.") != "":
		return payload[strings.TrimPrefix(text, "$record.")]
	case text == "$source.id":
		return source.ID
	case workflowSimpleTemplateRefKey(text, "$source.") != "":
		return source.Data[strings.TrimPrefix(text, "$source.")]
	case text == "$principal.user_id":
		return principal.UserID
	case text == "$principal.role_key":
		return principal.RoleKey
	case text == "$now":
		return time.Now().UTC().Format(time.RFC3339)
	case text == "$today":
		return time.Now().UTC().Format("2006-01-02")
	default:
		if strings.Contains(text, "$payload.") || strings.Contains(text, "$workflow.") || strings.Contains(text, "$record.") || strings.Contains(text, "$source.") || strings.Contains(text, "$principal.") || strings.Contains(text, "$now") || strings.Contains(text, "$today") {
			return renderWorkflowTemplateWithSource(ctx, text, payload, source, principal)
		}
		return value
	}
}

func workflowNestedReferenceValue(text, prefix string, payload map[string]any) (any, bool) {
	if !strings.HasPrefix(text, prefix) {
		return nil, false
	}
	path := strings.Split(strings.TrimPrefix(text, prefix), ".")
	if len(path) < 2 {
		return nil, false
	}
	for _, segment := range path {
		if segment == "" || workflowSimpleTemplateRefKey(prefix+segment, prefix) == "" {
			return nil, false
		}
	}
	var current any = payload
	for _, segment := range path {
		object, ok := current.(map[string]any)
		if !ok {
			return nil, true
		}
		current = object[segment]
	}
	return current, true
}

func workflowSimpleTemplateRefKey(text string, prefix string) string {
	if !strings.HasPrefix(text, prefix) {
		return ""
	}
	key := strings.TrimPrefix(text, prefix)
	if key == "" {
		return ""
	}
	for _, char := range key {
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') || (char >= '0' && char <= '9') || char == '_' {
			continue
		}
		return ""
	}
	return key
}

func renderWorkflowTemplate(ctx context.Context, text string, payload map[string]any, principal principalmodel.Principal) string {
	return renderWorkflowTemplateWithSource(ctx, text, payload, recordmodel.Record{}, principal)
}

func renderWorkflowTemplateWithSource(ctx context.Context, text string, payload map[string]any, source recordmodel.Record, principal principalmodel.Principal) string {
	_ = ctx
	out := text
	for key, value := range payload {
		out = strings.ReplaceAll(out, "$payload."+key, fmt.Sprint(value))
		out = strings.ReplaceAll(out, "$workflow."+key, fmt.Sprint(value))
		out = strings.ReplaceAll(out, "$record."+key, fmt.Sprint(value))
	}
	out = strings.ReplaceAll(out, "$workflow.id", fmt.Sprint(payload["record_id"]))
	out = strings.ReplaceAll(out, "$record.id", fmt.Sprint(payload["record_id"]))
	if source.ID != "" {
		out = strings.ReplaceAll(out, "$source.id", source.ID)
		for key, value := range source.Data {
			out = strings.ReplaceAll(out, "$source."+key, fmt.Sprint(value))
		}
	}
	out = strings.ReplaceAll(out, "$principal.user_id", principal.UserID)
	out = strings.ReplaceAll(out, "$principal.role_key", principal.RoleKey)
	out = strings.ReplaceAll(out, "$today", time.Now().UTC().Format("2006-01-02"))
	out = strings.ReplaceAll(out, "$now", time.Now().UTC().Format(time.RFC3339))
	return out
}

func workflowPayloadForRecord(objectKey string, record recordmodel.Record) map[string]any {
	return workflowPayloadForRecordChange(objectKey, record, nil, "")
}

func WorkflowPayloadForRecord(objectKey string, record recordmodel.Record) map[string]any {
	return workflowPayloadForRecord(objectKey, record)
}

func workflowPayloadForRecordChange(objectKey string, record recordmodel.Record, before map[string]any, trigger string) map[string]any {
	payload := workflowpolicy.WorkflowCloneMap(record.Data)
	payload["object_key"] = objectKey
	payload["id"] = record.ID
	payload["record_id"] = record.ID
	payload["created_at"] = record.CreatedAt
	payload["updated_at"] = record.UpdatedAt
	payload["after"] = workflowpolicy.WorkflowCloneMap(record.Data)
	if before != nil {
		payload["before"] = workflowpolicy.WorkflowCloneMap(before)
		payload["changed_fields"] = workflowpolicy.WorkflowChangedRecordFieldKeys(before, record.Data)
	}
	if trigger != "" {
		payload["trigger_event"] = trigger
	}
	return payload
}

func WorkflowPayloadForRecordChange(objectKey string, record recordmodel.Record, before map[string]any, trigger string) map[string]any {
	return workflowPayloadForRecordChange(objectKey, record, before, trigger)
}

func (s *WorkflowApplicationService) workflowRetryPayload(ctx context.Context, workspaceID string, previous workflowmodel.WorkflowExecution, principal principalmodel.Principal) map[string]any {
	payload := workflowpolicy.WorkflowCloneMap(previous.Payload)
	objectKey := valueOrDefault(strings.TrimSpace(previous.ObjectKey), workflowpolicy.WorkflowPayloadString(payload, "object_key"))
	recordID := valueOrDefault(strings.TrimSpace(previous.RecordID), workflowpolicy.WorkflowPayloadString(payload, "record_id"))
	if objectKey == "" || recordID == "" {
		return payload
	}
	object, ok := s.schemaMap(ctx)[objectKey]
	if !ok {
		return payload
	}
	record, ok, err := s.recordReader.GetWorkflowRecord(ctx, workspaceID, object, recordID, principal)
	if err != nil || !ok {
		return payload
	}
	refreshed := workflowPayloadForRecord(objectKey, record)
	for _, key := range []string{"request_id", "initiating_user_id", "initiating_role_key", "scheduled_at"} {
		if value, ok := payload[key]; ok && !workflowpolicy.WorkflowValueIsEmpty(value) {
			refreshed[key] = value
		}
	}
	refreshed["retry_source_execution_id"] = previous.ID
	refreshed["retry_source_status"] = previous.Status
	if previous.LastError != "" {
		refreshed["retry_source_last_error"] = previous.LastError
	}
	return refreshed
}

func workflowBaseResult(workflow definitionmodel.WorkflowSchema, payload map[string]any, trigger string, actionType string) map[string]any {
	return map[string]any{
		"workflow_key":       workflow.Key,
		"trigger":            trigger,
		"trigger_object_key": workflowpolicy.WorkflowPayloadString(payload, "object_key"),
		"trigger_record_id":  workflowpolicy.WorkflowPayloadString(payload, "record_id"),
		"changed_fields":     workflowpolicy.WorkflowChangedFieldsFromTrigger(workflow, trigger),
		"action_type":        actionType,
		"run_as":             workflowpolicy.WorkflowRunAs(workflow),
	}
}

func workflowExecutionAuditMetadata(execution workflowmodel.WorkflowExecution, principal principalmodel.Principal) map[string]any {
	metadata := map[string]any{
		"workflow_key":        execution.WorkflowKey,
		"workflow_name":       execution.Name,
		"status":              execution.Status,
		"trigger":             execution.Trigger,
		"trigger_object_key":  valueOrDefault(workflowpolicy.WorkflowPayloadString(execution.Payload, "object_key"), execution.ObjectKey),
		"trigger_record_id":   valueOrDefault(workflowpolicy.WorkflowPayloadString(execution.Payload, "record_id"), execution.RecordID),
		"execution_id":        execution.ID,
		"attempt":             execution.Attempt,
		"max_attempts":        execution.MaxAttempts,
		"run_as":              execution.RunAs,
		"action_type":         execution.ActionType,
		"idempotency_key":     execution.IdempotencyKey,
		"initiated_by":        valueOrDefault(workflowpolicy.WorkflowPayloadString(execution.Payload, "initiating_user_id"), principal.UserID),
		"initiating_role_key": valueOrDefault(workflowpolicy.WorkflowPayloadString(execution.Payload, "initiating_role_key"), principal.RoleKey),
	}
	if execution.NextRunAt != "" {
		metadata["next_run_at"] = execution.NextRunAt
	}
	if execution.LastError != "" {
		metadata["last_error"] = execution.LastError
	}
	for _, key := range []string{
		"created_object_key",
		"created_record_id",
		"created_record_ids",
		"updated_object_key",
		"updated_record_id",
		"updated_items",
		"notification_kind",
		"notification_channel",
		"notification_recipient",
		"notification_subject",
		"notification_severity",
	} {
		if value, ok := execution.Result[key]; ok && !workflowpolicy.WorkflowValueIsEmpty(value) {
			metadata[key] = value
		}
	}
	return metadata
}
