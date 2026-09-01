package automation

import (
	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"context"
	"fmt"
	"strings"

	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"

	"github.com/domainry/domainry-foundation/apperror"
	actionmodel "github.com/domainry/domainry-runtime/runtime/domain/action/model"
	automationruntime "github.com/domainry/domainry-runtime/runtime/domain/automation/runtime"
)

type AutomationInstructionRenderContext struct {
	Payload            map[string]any
	RenderString       func(any) string
	RenderOptionalData func(any) (map[string]any, error)
}

type AutomationInstructionDispatchDependencies struct {
	InvokeAction func(context.Context, actionmodel.ActionInvocation) (actionmodel.ActionInvocationResult, error)
	RunWorkflow  func(context.Context, string, map[string]any, principalmodel.Principal) (map[string]any, error)
	EmitEvent    func(context.Context, automationmodel.AutomationRuleSchema, automationmodel.AutomationInstructionSchema) (map[string]any, error)
}

type AutomationInstructionDispatchApplicationService struct {
	dependencies AutomationInstructionDispatchDependencies
}

func NewAutomationInstructionDispatchApplicationService(dependencies AutomationInstructionDispatchDependencies) *AutomationInstructionDispatchApplicationService {
	return &AutomationInstructionDispatchApplicationService{dependencies: dependencies}
}

func (d *AutomationInstructionDispatchApplicationService) Execute(ctx context.Context, rule automationmodel.AutomationRuleSchema, instruction automationmodel.AutomationInstructionSchema, render AutomationInstructionRenderContext, record *recordmodel.Record, principal principalmodel.Principal) (automationmodel.AutomationInstructionResult, error) {
	result := automationmodel.AutomationInstructionResult{Key: instruction.Key, Type: instruction.Type, Status: "success"}
	switch strings.TrimSpace(instruction.Type) {
	case "invoke_business_action":
		actionKey := render.RenderString(instruction.Config["action_key"])
		objectKey := valueOrDefault(render.RenderString(instruction.Config["object_key"]), rule.ObjectKey)
		if actionKey == "" {
			return failedInstruction(result, automationError(apperror.KindBadRequest, "backend.automation.business_action_key_required", nil, "instruction", instruction.Key))
		}
		input := map[string]any{}
		configuredInput := instruction.Config["input"]
		configured, err := render.RenderOptionalData(configuredInput)
		if err != nil {
			return failedInstruction(result, err)
		}
		for key, value := range configured {
			input[key] = value
		}
		recordID := render.RenderString(instruction.Config["record_id"])
		if recordID == "" && record != nil && rule.Trigger.Phase != "before" {
			recordID = record.ID
		}
		invocation, err := d.dependencies.InvokeAction(ctx, actionmodel.ActionInvocation{
			ActionKey: actionKey, ObjectKey: objectKey, RecordID: recordID, Input: input, Principal: principal,
			Source: actionmodel.ActionSourceAutomation, RequestID: principal.RequestID,
			IdempotencyKey: automationruntime.AutomationInstructionIdempotencyKey(rule, instruction, record),
		})
		if err != nil {
			return failedInstruction(result, err)
		}
		result.InvocationID = invocation.InvocationID
		result.OutboxIDs = invocation.OutboxIDs
		result.Data = invocation.Output
		if recordID == "" {
			for key, value := range automationInstructionMap(invocation.Output["data"]) {
				render.Payload[key] = value
			}
		}
		return result, nil
	case "start_workflow":
		workflowKey := render.RenderString(instruction.Config["workflow_key"])
		if workflowKey == "" {
			return failedInstruction(result, automationError(apperror.KindBadRequest, "backend.automation.workflow_key_required", nil, "instruction", instruction.Key))
		}
		payloadValue := instruction.Config["payload"]
		if payloadValue == nil {
			payloadValue = instruction.Config["input"]
		}
		payload, err := render.RenderOptionalData(payloadValue)
		if err != nil {
			return failedInstruction(result, err)
		}
		result.Data, err = d.dependencies.RunWorkflow(ctx, workflowKey, payload, principal)
		if err != nil {
			return failedInstruction(result, err)
		}
		return result, nil
	case "emit_event":
		data, err := d.dependencies.EmitEvent(ctx, rule, instruction)
		if err != nil {
			return failedInstruction(result, err)
		}
		result.Data = data
		return result, nil
	default:
		return failedInstruction(result, automationError(apperror.KindBadRequest, "backend.automation.instruction_type_invalid", nil,
			"instruction", instruction.Key, "type", instruction.Type))
	}
}

func automationInstructionMap(value any) map[string]any {
	mapped, _ := value.(map[string]any)
	if mapped == nil {
		return map[string]any{}
	}
	return mapped
}

func failedInstruction(result automationmodel.AutomationInstructionResult, err error) (automationmodel.AutomationInstructionResult, error) {
	result.Status = "failed"
	result.ErrorCode = errorCode(err)
	result.Message = fmt.Sprint(err)
	return result, err
}
