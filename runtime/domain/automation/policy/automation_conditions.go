package policy

import (
	"strings"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
)

type AutomationConditionMatcher func(automationmodel.AutomationConditionClause) bool

func AutomationConditionGroupMatches(group automationmodel.AutomationConditionGroup, matches AutomationConditionMatcher) bool {
	if len(group.Clauses) == 0 && len(group.Groups) == 0 {
		return true
	}
	mode := strings.ToLower(strings.TrimSpace(group.Mode))
	if mode == "" {
		mode = "all"
	}
	results := make([]bool, 0, len(group.Clauses)+len(group.Groups))
	for _, clause := range group.Clauses {
		results = append(results, matches(clause))
	}
	for _, nested := range group.Groups {
		results = append(results, AutomationConditionGroupMatches(nested, matches))
	}
	if mode == "any" {
		for _, matched := range results {
			if matched {
				return true
			}
		}
		return false
	}
	for _, matched := range results {
		if !matched {
			return false
		}
	}
	return true
}
