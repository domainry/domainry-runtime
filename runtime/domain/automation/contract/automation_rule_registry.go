package contract

import automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"

type AutomationRuleRegistry interface {
	List() []automationmodel.AutomationRuleSchema
	Get(string) (automationmodel.AutomationRuleSchema, bool)
}
