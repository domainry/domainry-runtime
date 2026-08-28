package validation

import automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

type AutomationValidationResult struct {
	Valid  bool                                 `json:"valid"`
	Rule   automationmodel.AutomationRuleSchema `json:"rule"`
	Errors []AutomationValidationIssue          `json:"errors,omitempty"`
}

type AutomationFragmentValidationResult struct {
	Valid         bool                        `json:"valid"`
	CapabilityKey string                      `json:"capability_key"`
	Fragment      map[string]any              `json:"fragment"`
	Errors        []AutomationValidationIssue `json:"errors,omitempty"`
}

type AutomationValidationIssue struct {
	Section         string            `json:"section"`
	InstructionKey  string            `json:"instruction_key,omitempty"`
	FieldPath       string            `json:"field_path"`
	ErrorCode       string            `json:"error_code"`
	MessageKey      string            `json:"message_key"`
	CapabilityKey   string            `json:"capability_key"`
	ContractVersion string            `json:"contract_version"`
	Params          map[string]string `json:"params,omitempty"`
}
