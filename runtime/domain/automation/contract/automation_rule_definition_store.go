package contract

import (
	"context"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
)

// AutomationRuleDefinitionStore owns workspace rule drafts, publications and
// enabled overrides. Implementations must compare ExpectedRevision atomically.
type AutomationRuleDefinitionStore interface {
	Get(context.Context, string, string) (automationmodel.AutomationRuleDefinition, bool, error)
	List(context.Context, string) ([]automationmodel.AutomationRuleDefinition, error)
	Put(context.Context, automationmodel.AutomationRuleDefinition, int) (automationmodel.AutomationRuleDefinition, bool, error)
}
