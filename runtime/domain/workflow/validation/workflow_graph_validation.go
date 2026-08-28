package validation

import (
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	workflowpolicy "github.com/domainry/domainry-runtime/runtime/domain/workflow/policy"
)

func WorkflowValidateGraph(graph *definitionmodel.WorkflowGraphSchema) error {
	return workflowpolicy.WorkflowValidateGraph(graph)
}

func WorkflowConditionContractIsValid(contract definitionmodel.WorkflowConditionContract) bool {
	return workflowpolicy.WorkflowConditionContractIsValid(contract)
}
