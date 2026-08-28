package policy

import (
	"testing"

	automationmodel "github.com/domainry/domainry-runtime/runtime/domain/automation/model"
)

func TestConditionGroupMatchesNestedAllAndAny(t *testing.T) {
	group := automationmodel.AutomationConditionGroup{
		Mode:    "all",
		Clauses: []automationmodel.AutomationConditionClause{{Reference: "first"}},
		Groups: []automationmodel.AutomationConditionGroup{{
			Mode:    "any",
			Clauses: []automationmodel.AutomationConditionClause{{Reference: "second"}, {Reference: "third"}},
		}},
	}
	matched := AutomationConditionGroupMatches(group, func(clause automationmodel.AutomationConditionClause) bool {
		return clause.Reference == "first" || clause.Reference == "third"
	})
	if !matched {
		t.Fatal("expected nested all/any group to match")
	}
}

func TestConditionGroupMatchesEmptyGroup(t *testing.T) {
	if !AutomationConditionGroupMatches(automationmodel.AutomationConditionGroup{}, func(automationmodel.AutomationConditionClause) bool { return false }) {
		t.Fatal("empty condition group must match")
	}
}

func TestConditionGroupMatchesCompleteModeAndResultMatrix(t *testing.T) {
	matcher := func(clause automationmodel.AutomationConditionClause) bool { return clause.Reference == "yes" }
	tests := []struct {
		name  string
		group automationmodel.AutomationConditionGroup
		want  bool
	}{
		{name: "default all true", group: automationmodel.AutomationConditionGroup{Clauses: []automationmodel.AutomationConditionClause{{Reference: "yes"}}}, want: true},
		{name: "normalized all false", group: automationmodel.AutomationConditionGroup{Mode: " ALL ", Clauses: []automationmodel.AutomationConditionClause{{Reference: "yes"}, {Reference: "no"}}}, want: false},
		{name: "any true", group: automationmodel.AutomationConditionGroup{Mode: " ANY ", Clauses: []automationmodel.AutomationConditionClause{{Reference: "no"}, {Reference: "yes"}}}, want: true},
		{name: "any false", group: automationmodel.AutomationConditionGroup{Mode: "any", Clauses: []automationmodel.AutomationConditionClause{{Reference: "no"}}}, want: false},
		{name: "nested only", group: automationmodel.AutomationConditionGroup{Groups: []automationmodel.AutomationConditionGroup{{Clauses: []automationmodel.AutomationConditionClause{{Reference: "yes"}}}}}, want: true},
		{name: "unknown mode uses all", group: automationmodel.AutomationConditionGroup{Mode: "unknown", Clauses: []automationmodel.AutomationConditionClause{{Reference: "yes"}, {Reference: "no"}}}, want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := AutomationConditionGroupMatches(test.group, matcher); got != test.want {
				t.Fatalf("match = %v, want %v", got, test.want)
			}
		})
	}
}
