package repository

import (
	"context"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
)

type AutomationExecutionRepository interface {
	InsertExecution(context.Context, string, automationmodel.AutomationRuleExecution) (automationmodel.AutomationRuleExecution, error)
	ListExecutions(context.Context, string, automationmodel.AutomationExecutionFilter) ([]automationmodel.AutomationRuleExecution, error)
}
