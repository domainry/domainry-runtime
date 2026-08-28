package contract

import (
	integrationmodel "github.com/domainry/domainry-runtime/runtime/domain/integration/model"
)

type CapabilityAutomationCatalog struct {
	Phases                  []string                                 `json:"phases"`
	Operations              []string                                 `json:"operations"`
	ExecutionModes          []string                                 `json:"execution_modes"`
	RunAsModes              []string                                 `json:"run_as_modes"`
	ResultNotificationModes []string                                 `json:"result_notification_modes"`
	InstructionTypes        []string                                 `json:"instruction_types"`
	BeforeInstructionTypes  []string                                 `json:"before_instruction_types"`
	AfterInstructionTypes   []string                                 `json:"after_instruction_types"`
	Capabilities            []CapabilityAutomationInstruction        `json:"capabilities"`
	Connectors              []integrationmodel.ConnectorSchema       `json:"connectors"`
	Connections             []integrationmodel.IntegrationConnection `json:"connections"`
	AuthoringProjection     *CapabilityAuthoringProjection           `json:"authoring_projection,omitempty"`
}

type CapabilityAutomationInstruction struct {
	Type              string   `json:"type"`
	Owner             string   `json:"owner"`
	Lifecycle         string   `json:"lifecycle"`
	SupportedContexts []string `json:"supported_contexts"`
}

func RuntimeAutomationCapabilities() CapabilityAutomationCatalog {
	return CapabilityAutomationCatalog{
		Phases: []string{"after", "before"}, Operations: []string{"create", "delete", "transition", "update"}, ExecutionModes: []string{"async", "sync"}, RunAsModes: []string{"initiator"}, ResultNotificationModes: []string{"none", "failures", "all"},
		InstructionTypes: []string{"assert", "derive_fields", "emit_event", "invoke_business_action", "start_workflow"}, BeforeInstructionTypes: []string{"assert", "derive_fields"}, AfterInstructionTypes: []string{"emit_event", "invoke_business_action", "start_workflow"},
		Capabilities: automationInstructionCapabilities(),
	}
}

func automationInstructionCapabilities() []CapabilityAutomationInstruction {
	active := []string{"assert", "derive_fields", "invoke_business_action", "start_workflow", "emit_event"}
	capabilities := make([]CapabilityAutomationInstruction, 0, len(active))
	for _, actionType := range active {
		contexts := []string{"after"}
		if actionType == "assert" || actionType == "derive_fields" {
			contexts = []string{"before"}
		}
		capabilities = append(capabilities, CapabilityAutomationInstruction{Type: actionType, Owner: "automation_rule", Lifecycle: "record_lifecycle", SupportedContexts: contexts})
	}
	return capabilities
}
