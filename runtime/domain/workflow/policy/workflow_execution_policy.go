package policy

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/domainry/domainry-foundation/apperror"
	"github.com/domainry/domainry-foundation/idempotency"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	workflowmodel "github.com/domainry/domainry-runtime/runtime/domain/workflow/model"
)

func WorkflowPermissionAllows(principal principalmodel.Principal, action string) bool {
	return principal.Known && (principal.HasPermission("ops.workflow."+action) || principal.Allows("workflow", action))
}

// WorkflowRunPermissionAllows supports least-privilege callers such as a
// consumer portal. A role may run every manual workflow with workflow.run, or
// one explicitly named workflow with workflow.run.<workflow-key>.
func WorkflowRunPermissionAllows(principal principalmodel.Principal, workflowKey string) bool {
	return WorkflowPermissionAllows(principal, "run") ||
		(principal.Known && principal.HasPermission("workflow.run."+strings.TrimSpace(workflowKey)))
}

func WorkflowDefinitionPermissionAllows(principal principalmodel.Principal, permission string) bool {
	return principal.Known && principal.HasPermission(permission)
}

func WorkflowChangedFieldsFromTrigger(workflow definitionmodel.WorkflowSchema, trigger string) []string {
	fields := []string{}
	if strings.HasPrefix(strings.TrimSpace(trigger), "record_updated:") {
		parts := strings.Split(trigger, ".")
		if len(parts) >= 2 {
			field := strings.TrimSpace(parts[len(parts)-1])
			if field != "" {
				fields = append(fields, field)
			}
		}
	}
	if workflow.TriggerContract != nil {
		field := strings.TrimSpace(workflow.TriggerContract.FieldKey)
		if field != "" && !workflowContainsText(fields, field) {
			fields = append(fields, field)
		}
	}
	return fields
}

func WorkflowPayloadString(payload map[string]any, key string) string {
	value, ok := payload[key]
	if !ok || value == nil {
		return ""
	}
	return strings.TrimSpace(fmt.Sprint(value))
}

func WorkflowRetryCount(value any) int {
	switch number := value.(type) {
	case int:
		return number
	case int32:
		return int(number)
	case int64:
		return int(number)
	case float32:
		return int(number)
	case float64:
		return int(number)
	case json.Number:
		parsed, _ := number.Int64()
		return int(parsed)
	default:
		return 0
	}
}

func WorkflowRunAs(workflow definitionmodel.WorkflowSchema) string {
	return strings.TrimSpace(workflow.RunAs)
}

func WorkflowExecutionRetryScheduled(execution workflowmodel.WorkflowExecution) bool {
	status := strings.TrimSpace(execution.Status)
	if status != "failed" && status != "pending" {
		return false
	}
	if execution.MaxAttempts > 0 && execution.Attempt >= execution.MaxAttempts {
		return false
	}
	return strings.TrimSpace(execution.NextRunAt) != ""
}

func WorkflowExecutionDue(execution workflowmodel.WorkflowExecution, now time.Time) bool {
	status := strings.TrimSpace(execution.Status)
	if status == "running" {
		leaseExpiresAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(execution.LeaseExpiresAt))
		if err != nil {
			leaseExpiresAt, err = time.Parse(time.RFC3339, strings.TrimSpace(execution.LeaseExpiresAt))
		}
		return err == nil && !leaseExpiresAt.After(now)
	}
	if status != "pending" && status != "failed" {
		return false
	}
	if execution.MaxAttempts > 0 && execution.Attempt >= execution.MaxAttempts {
		return false
	}
	if strings.TrimSpace(execution.NextRunAt) == "" {
		return status == "pending"
	}
	nextRunAt, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(execution.NextRunAt))
	if err != nil {
		nextRunAt, err = time.Parse(time.RFC3339, strings.TrimSpace(execution.NextRunAt))
	}
	return err == nil && !nextRunAt.After(now)
}

func WorkflowManualRunAllowed(workflow definitionmodel.WorkflowSchema) bool {
	return workflow.TriggerContract != nil && strings.TrimSpace(workflow.TriggerContract.Type) == "manual"
}

func WorkflowMaxAttempts(workflow definitionmodel.WorkflowSchema) int {
	if workflow.Retry != nil && workflow.Retry.MaxAttempts > 0 {
		return workflow.Retry.MaxAttempts
	}
	return 3
}

func WorkflowRetryDelaySeconds(workflow definitionmodel.WorkflowSchema) int {
	if workflow.Retry != nil && workflow.Retry.DelaySeconds > 0 {
		return workflow.Retry.DelaySeconds
	}
	return 60
}

func WorkflowMarkFailed(execution *workflowmodel.WorkflowExecution, workflow definitionmodel.WorkflowSchema, err error, now time.Time) {
	execution.Status = "failed"
	code, params := workflowErrorCode(err)
	execution.LastError, execution.Message = code, code
	if execution.Result == nil {
		execution.Result = map[string]any{}
	}
	execution.Result["last_error"], execution.Result["last_error_code"] = code, code
	if len(params) > 0 {
		execution.Result["last_error_params"] = params
	}
	maxAttempts := execution.MaxAttempts
	if maxAttempts <= 0 {
		maxAttempts = WorkflowMaxAttempts(workflow)
		execution.MaxAttempts = maxAttempts
	}
	if execution.Attempt >= maxAttempts {
		execution.Status, execution.NextRunAt = "dead_letter", ""
		execution.Result["dead_lettered"] = true
		return
	}
	execution.NextRunAt = now.Add(time.Duration(WorkflowRetryDelaySeconds(workflow)) * time.Second).Format(time.RFC3339)
	execution.Result["retry_scheduled"] = true
	execution.Result["retry_after"] = execution.NextRunAt
	execution.Result["retry_delay_seconds"] = WorkflowRetryDelaySeconds(workflow)
}

func WorkflowIdempotencyKey(workflow definitionmodel.WorkflowSchema, payload map[string]any) string {
	if len(workflow.IdempotencyKeys) == 0 {
		return ""
	}
	projection := make(map[string]any, len(workflow.IdempotencyKeys))
	for _, key := range workflow.IdempotencyKeys {
		projection[strings.TrimSpace(key)] = payload[key]
	}
	fingerprint, err := idempotency.Fingerprint(idempotency.FingerprintInput{
		UseCase: "workflow.execute", ResourceType: "workflow", TargetID: strings.TrimSpace(workflow.Key), Payload: projection,
	})
	if err != nil {
		return ""
	}
	return fingerprint
}

func WorkflowCloneMap(value map[string]any) map[string]any {
	out := make(map[string]any, len(value))
	for key, item := range value {
		out[key] = item
	}
	return out
}

func WorkflowValueIsEmpty(value any) bool {
	if value == nil {
		return true
	}
	text, ok := value.(string)
	return ok && strings.TrimSpace(text) == ""
}

func workflowErrorCode(err error) (string, map[string]string) {
	if err == nil {
		return "backend.internal", nil
	}
	var appErr *apperror.AppError
	if errors.As(err, &appErr) {
		return workflowNormalizedErrorCode(appErr.Kind, appErr.ErrorCode()), appErr.ErrorParams()
	}
	return "backend.internal", map[string]string{"operation": "workflow"}
}

func workflowNormalizedErrorCode(kind apperror.ErrorKind, code string) string {
	if strings.Contains(strings.TrimSpace(code), ".") {
		return strings.TrimSpace(code)
	}
	switch kind {
	case apperror.KindBadRequest:
		return "backend.bad_request"
	case apperror.KindForbidden:
		return "backend.forbidden"
	case apperror.KindNotFound:
		return "backend.not_found"
	case apperror.KindConflict:
		return "backend.conflict"
	default:
		return "backend.internal"
	}
}

func workflowContainsText(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
