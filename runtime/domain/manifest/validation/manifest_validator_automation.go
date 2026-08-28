package validation

import (
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"

	"fmt"
	"strings"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
)

func (state *validationState) validateAutomationRules() {
	seen := map[string]bool{}
	for index, rule := range state.manifest.AutomationRules {
		path := fmt.Sprintf("automation_rules[%d]", index)
		key := strings.TrimSpace(rule.Key)
		if key == "" {
			state.add(path+".key", "is required")
		} else if seen[key] {
			state.add(path+".key", "duplicate automation rule key %q", key)
		}
		seen[key] = true
		if _, ok := state.objects[strings.TrimSpace(rule.ObjectKey)]; !ok {
			state.add(path+".object_key", "unknown object %q", rule.ObjectKey)
		}
		phase := strings.TrimSpace(rule.Trigger.Phase)
		if phase != "before" && phase != "after" {
			state.add(path+".trigger.phase", "must be before or after")
		}
		switch strings.TrimSpace(rule.Trigger.Operation) {
		case "create", "update", "delete", "transition":
		default:
			state.add(path+".trigger.operation", "must be create, update, delete, or transition")
		}
		if phase == "before" && rule.Execution.Mode != "" && rule.Execution.Mode != "sync" {
			state.add(path+".execution.mode", "before rules must execute synchronously")
		}
		if rule.Execution.RunAs != "" && rule.Execution.RunAs != "initiator" {
			state.add(path+".execution.run_as", "must be initiator; lifecycle automation cannot elevate the initiating principal")
		}
		resultNotification := strings.TrimSpace(rule.Execution.ResultNotification)
		if resultNotification != "" && resultNotification != "none" && resultNotification != "failures" && resultNotification != "all" {
			state.add(path+".execution.result_notification", "must be none, failures, or all")
		} else if phase == "before" && resultNotification != "" && resultNotification != "none" {
			state.add(path+".execution.result_notification", "before rules cannot publish terminal result notifications")
		}
		state.validateAutomationInstructions(path, rule)
	}
}

func (state *validationState) validateAutomationInstructions(path string, rule automationmodel.AutomationRuleSchema) {
	keys := map[string]bool{}
	aliases := map[string]bool{}
	for index, action := range rule.Instructions {
		instructionPath := fmt.Sprintf("%s.instructions[%d]", path, index)
		key := strings.TrimSpace(action.Key)
		if key == "" {
			state.add(instructionPath+".key", "is required")
		} else if keys[key] {
			state.add(instructionPath+".key", "duplicate instruction key %q", key)
		}
		keys[key] = true
		actionType := strings.TrimSpace(action.Type)
		if !automationInstructionTypeAllowed(actionType) {
			state.add(instructionPath+".type", "unsupported Automation instruction type %q", actionType)
		}
		if rule.Trigger.Phase == "before" && !automationBeforeInstructionTypeAllowed(actionType) {
			state.add(instructionPath+".type", "instruction type %q is not allowed before persistence", actionType)
		}
		if actionType == "integration_call" || actionType == "reserve" {
			connector, ok := state.connectors[strings.TrimSpace(action.ConnectorKey)]
			if !ok {
				state.add(instructionPath+".connector_key", "unknown Connector %q", action.ConnectorKey)
			} else if len(connector.Operations) > 0 && !connectorHasOperation(connector, action.Operation) {
				state.add(instructionPath+".operation", "unknown operation %q for Connector %q", action.Operation, action.ConnectorKey)
			}
			if strings.TrimSpace(action.ConnectionKey) == "" {
				state.add(instructionPath+".connection_key", "is required")
			}
			if strings.TrimSpace(action.Operation) == "" {
				state.add(instructionPath+".operation", "is required")
			}
			if rule.Trigger.Phase == "before" && strings.TrimSpace(action.OnError) != "block" {
				state.add(instructionPath+".on_error", "before integration guards must block on error")
			}
		}
		alias := strings.TrimSpace(action.ResultAlias)
		if alias != "" {
			if aliases[alias] {
				state.add(instructionPath+".result_alias", "duplicate result alias %q", alias)
			}
			aliases[alias] = true
		}
	}
}

func connectorHasOperation(connector integrationmodel.ConnectorSchema, operationKey string) bool {
	for _, operation := range connector.Operations {
		if strings.TrimSpace(operation.Key) == strings.TrimSpace(operationKey) {
			return true
		}
	}
	return false
}

func automationInstructionTypeAllowed(value string) bool {
	switch value {
	case "derive_fields", "assert", "invoke_business_action", "emit_event", "start_workflow":
		return true
	default:
		return false
	}
}

func automationBeforeInstructionTypeAllowed(value string) bool {
	switch value {
	case "derive_fields", "assert":
		return true
	default:
		return false
	}
}
