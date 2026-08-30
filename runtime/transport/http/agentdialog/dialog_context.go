package agentdialog

import (
	"strings"
	"unicode/utf8"

	agentsdk "github.com/domainry/domainry-agent-sdk"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

func agentDialogTrustedRuntimeContext(context agentsdk.GlobalContext) map[string]any {
	return map[string]any{
		"contract_version": context.ContractVersion, "context_revision": context.ContextRevision,
		"entrypoint_key": context.EntrypointKey, "agent_key": context.AgentKey, "surface": context.Surface, "route_key": context.RouteKey,
		"object_key": context.ObjectKey, "record_id": context.RecordID, "selected_record_ids": append([]string(nil), context.SelectedRecordIDs...),
		"locale": context.Locale, "timezone": context.Timezone, "principal": context.Principal,
		"allowed_task_keys": append([]string(nil), context.AllowedTaskKeys...), "allowed_workflow_keys": append([]string(nil), context.AllowedWorkflowKeys...),
		"available_operations": append([]string(nil), context.AvailableOperations...),
	}
}

func agentDialogRuntimeContext(input map[string]any, principal principalmodel.Principal) map[string]any {
	out := map[string]any{}
	for _, key := range []string{"workspace", "module_key", "module_label", "surface", "surface_label", "view_key", "view_label", "object_key", "object_label", "record_id", "record_label", "account", "region", "data_scope_note"} {
		if value := agentDialogSafeString(input[key]); value != "" {
			out[key] = value
		}
	}
	for _, key := range []string{"selected_records"} {
		if values := agentDialogSafeStringList(input[key], 20); len(values) > 0 {
			out[key] = values
		}
	}
	for _, key := range []string{"filters", "import_state", "audit"} {
		if value := agentDialogSafeValue(input[key], 2); value != nil {
			out[key] = value
		}
	}
	for _, key := range []string{"reports", "workflows", "workflow_executions"} {
		if value := agentDialogSafeValue(input[key], 3); value != nil {
			out[key] = value
		}
	}
	out["workspace_id"] = principal.WorkspaceID
	out["user_id"] = principal.UserID
	out["role"] = principal.RoleKey
	out["permission_scope"] = "server_principal_scoped"
	return out
}

func agentDialogSafeString(value any) string {
	text, ok := value.(string)
	if !ok {
		return ""
	}
	text = strings.TrimSpace(text)
	if len(text) > 512 {
		text = text[:512]
		for !utf8.ValidString(text) {
			text = text[:len(text)-1]
		}
	}
	return text
}

func agentDialogSafeStringList(value any, limit int) []string {
	items, ok := value.([]any)
	if !ok {
		if typed, ok := value.([]string); ok {
			out := make([]string, 0, len(typed))
			for _, item := range typed {
				if text := agentDialogSafeString(item); text != "" {
					out = append(out, text)
				}
				if len(out) >= limit {
					break
				}
			}
			return out
		}
		return nil
	}
	out := make([]string, 0, len(items))
	for _, item := range items {
		if text := agentDialogSafeString(item); text != "" {
			out = append(out, text)
		}
		if len(out) >= limit {
			break
		}
	}
	return out
}

func agentDialogSafeValue(value any, depth int) any {
	if depth <= 0 || value == nil {
		return nil
	}
	switch typed := value.(type) {
	case string:
		return agentDialogSafeString(typed)
	case bool, float64, int, int64:
		return typed
	case []any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			if safe := agentDialogSafeValue(item, depth-1); safe != nil {
				out = append(out, safe)
			}
			if len(out) >= 20 {
				break
			}
		}
		return out
	case []string:
		return agentDialogSafeStringList(typed, 20)
	case map[string]any:
		out := map[string]any{}
		for key, item := range typed {
			safeKey := agentDialogSafeString(key)
			if safeKey == "" {
				continue
			}
			if safe := agentDialogSafeValue(item, depth-1); safe != nil {
				out[safeKey] = safe
			}
			if len(out) >= 30 {
				break
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	default:
		return nil
	}
}
