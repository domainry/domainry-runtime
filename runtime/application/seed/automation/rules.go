package automationseed

import (
	"strings"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
)

func MergeRules(existing, generated []automationmodel.AutomationRuleSchema) []automationmodel.AutomationRuleSchema {
	out := append([]automationmodel.AutomationRuleSchema(nil), existing...)
	seen := map[string]bool{}
	for _, rule := range out {
		seen[strings.TrimSpace(rule.Key)] = true
	}
	for _, rule := range generated {
		if key := strings.TrimSpace(rule.Key); key != "" && !seen[key] {
			seen[key] = true
			out = append(out, rule)
		}
	}
	return out
}
