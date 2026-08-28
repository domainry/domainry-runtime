package validation

import (
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

	"context"
	"errors"
	"fmt"
	"strings"

	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	"github.com/domainry/domainry-runtime/runtime/platform/apperror"
)

const AutomationRuntimeAuthoringContractVersion = "runtime-authoring-v1"

// AutomationValidateRuleForAuthoring aggregates instruction-shape issues with the
// injected catalog validator while preserving non-validation failures.
func AutomationValidateRuleForAuthoring(ctx context.Context, rule automationmodel.AutomationRuleSchema, validate func(context.Context, automationmodel.AutomationRuleSchema) error) (AutomationValidationResult, error) {
	issues := AutomationValidateActiveInstructionIssues(rule)
	if validate != nil {
		if err := validate(ctx, rule); err != nil {
			var appErr *apperror.AppError
			if errors.As(err, &appErr) && appErr.Kind == apperror.KindBadRequest {
				issues = AutomationAppendDistinctValidationIssue(issues, AutomationValidationIssueFromError(rule, err))
			} else {
				return AutomationValidationResult{Valid: false, Rule: rule, Errors: issues}, err
			}
		}
	}
	return AutomationValidationResult{Valid: len(issues) == 0, Rule: rule, Errors: issues}, nil
}

func AutomationAppendDistinctValidationIssue(issues []AutomationValidationIssue, candidate AutomationValidationIssue) []AutomationValidationIssue {
	for _, issue := range issues {
		if issue.ErrorCode == candidate.ErrorCode && issue.FieldPath == candidate.FieldPath && issue.InstructionKey == candidate.InstructionKey {
			return issues
		}
	}
	return append(issues, candidate)
}

func AutomationValidateActiveInstructionIssues(rule automationmodel.AutomationRuleSchema) []AutomationValidationIssue {
	issues := []AutomationValidationIssue{}
	for _, instruction := range rule.Instructions {
		switch strings.TrimSpace(instruction.Type) {
		case "derive_fields", "assert", "invoke_business_action", "start_workflow", "emit_event":
		default:
			err := definitionError(apperror.KindBadRequest, "backend.automation.instruction_type_invalid", nil,
				"instruction", instruction.Key,
				"type", instruction.Type,
				"allowed", "assert,derive_fields,emit_event,invoke_business_action,start_workflow",
				"actual", instruction.Type,
			)
			issues = append(issues, AutomationValidationIssueFromError(rule, err))
		}
	}
	return issues
}

func AutomationValidateActiveInstructions(rule automationmodel.AutomationRuleSchema) error {
	issues := AutomationValidateActiveInstructionIssues(rule)
	if len(issues) == 0 {
		return nil
	}
	params := []string{}
	for key, value := range issues[0].Params {
		params = append(params, key, value)
	}
	return definitionError(apperror.KindBadRequest, issues[0].ErrorCode, nil, params...)
}

func AutomationValidationIssueFromError(rule automationmodel.AutomationRuleSchema, err error) AutomationValidationIssue {
	code, params := automationErrorDetails(err)
	issue := AutomationValidationIssue{
		Section:         "rule",
		FieldPath:       "rule",
		ErrorCode:       code,
		MessageKey:      code,
		CapabilityKey:   validationCapabilityKey(code),
		ContractVersion: AutomationRuntimeAuthoringContractVersion,
		Params:          params,
	}

	switch {
	case strings.Contains(code, "identity"):
		issue.Section, issue.FieldPath = "identity", "key"
	case strings.Contains(code, "object_not_found"):
		issue.Section, issue.FieldPath = "object", "object_key"
	case strings.Contains(code, "phase"):
		issue.Section, issue.FieldPath = "trigger", "trigger.phase"
	case code == "backend.automation.operation_invalid":
		issue.Section, issue.FieldPath = "trigger", "trigger.operation"
	case strings.Contains(code, "changed_field"):
		issue.Section, issue.FieldPath = "trigger", "trigger.changed_fields"
	case strings.Contains(code, "transition_state"):
		issue.Section, issue.FieldPath = "trigger", "trigger."+valueOrDefault(strings.TrimSpace(params["field"]), "from_state")
	case strings.Contains(code, "transition_filter"):
		issue.Section, issue.FieldPath = "trigger", "trigger.operation"
	case strings.Contains(code, "run_as"):
		issue.Section, issue.FieldPath = "execution", "execution.run_as"
	case strings.Contains(code, "action") || strings.Contains(code, "connector") || strings.Contains(code, "connection") || strings.Contains(code, "operation_") || strings.Contains(code, "output_reference") || strings.Contains(code, "input_reference"):
		issue.Section = "instructions"
	}

	actionIndex := validationActionIndex(rule, params)
	if actionIndex < 0 {
		return issue
	}
	instruction := rule.Instructions[actionIndex]
	issue.Section = "instructions"
	issue.InstructionKey = instruction.Key
	prefix := fmt.Sprintf("instructions[%d]", actionIndex)
	switch {
	case strings.Contains(code, "instruction_type_invalid"):
		issue.FieldPath = prefix + ".type"
	case strings.Contains(code, "action_key_invalid"):
		issue.FieldPath = prefix + ".key"
	case strings.Contains(code, "before_action_unsupported") || strings.Contains(code, "after_action_unsupported"):
		issue.FieldPath = prefix + ".type"
	case strings.Contains(code, "target_action") || strings.Contains(code, "action_key_required"):
		issue.FieldPath = prefix + ".config.action_key"
	case strings.Contains(code, "workflow"):
		issue.FieldPath = prefix + ".config.workflow_key"
	case strings.Contains(code, "connection"):
		issue.FieldPath = prefix + ".connection_key"
	case strings.Contains(code, "connector_not_found"):
		issue.FieldPath = prefix + ".connector_key"
	case strings.Contains(code, "operation_input"):
		issue.FieldPath = prefix + ".input"
		if field := strings.TrimSpace(params["field"]); field != "" {
			issue.FieldPath += "." + field
		}
	case strings.Contains(code, "operation"):
		issue.FieldPath = prefix + ".operation"
	case strings.Contains(code, "output_reference"):
		issue.FieldPath = prefix + ".input"
	case strings.Contains(code, "before_must_block"):
		issue.FieldPath = prefix + ".on_error"
	default:
		issue.FieldPath = prefix
	}
	return issue
}

func AutomationHasPermission(principal principalmodel.Principal, action string) bool {
	if !principal.Known {
		return false
	}
	permissionKey := map[string]string{
		"read":     "automation.rule.read",
		"manage":   "automation.rule.write",
		"simulate": "automation.rule.simulate",
		"execute":  "automation.rule.execute",
		"history":  "automation.rule.history.read",
	}[action]
	return principal.HasPermission("workspace.admin") ||
		principal.HasPermission(permissionKey) ||
		principal.HasPermission("automation.*") ||
		principal.HasPermission("automation."+action)
}

func validationCapabilityKey(code string) string {
	switch {
	case strings.Contains(code, "trigger"), strings.Contains(code, "phase"), strings.Contains(code, "operation_invalid"):
		return "automation.trigger"
	case strings.Contains(code, "condition"):
		return "automation.condition_group"
	case strings.Contains(code, "execution"), strings.Contains(code, "run_as"):
		return "automation.execution_policy"
	default:
		return "automation.rule"
	}
}

func validationActionIndex(rule automationmodel.AutomationRuleSchema, params map[string]string) int {
	for index, instruction := range rule.Instructions {
		if value := strings.TrimSpace(params["instruction"]); value != "" && value == strings.TrimSpace(instruction.Key) {
			return index
		}
		if value := strings.TrimSpace(params["action"]); value != "" && value == strings.TrimSpace(instruction.Key) {
			return index
		}
		if value := strings.TrimSpace(params["operation"]); value != "" && value == strings.TrimSpace(instruction.Operation) {
			return index
		}
		if value := strings.TrimSpace(params["connection"]); value != "" && value == strings.TrimSpace(instruction.ConnectionKey) {
			return index
		}
		if value := strings.TrimSpace(params["connector"]); value != "" && value == strings.TrimSpace(instruction.ConnectorKey) {
			return index
		}
	}
	return -1
}

func automationErrorDetails(err error) (string, map[string]string) {
	var appErr *apperror.AppError
	if errors.As(err, &appErr) {
		return appErr.ErrorCode(), appErr.ErrorParams()
	}
	return "backend.internal", nil
}
