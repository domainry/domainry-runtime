package validation

import (
	"testing"

	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
)

func TestRuntimeAuthoringCoverageRequiresCompleteReachableLedger(t *testing.T) {
	snapshot := changeplanmodel.Snapshot{
		CapabilityKeys: []string{"schema.object", "action.definition"},
		ResourceSources: []changeplanmodel.ResourceSource{
			{ResourceType: "object", ResourceKey: "order", SourceKind: "builder_v4"},
			{ResourceType: "action", ResourceKey: "order.complete", SourceKind: "builder_v4"},
			{ResourceType: "field", ResourceKey: "order.legacy", SourceKind: "unknown"},
			{ResourceType: "field", ResourceKey: "order.disabled", Disabled: true},
		},
	}
	ledger := &changeplanmodel.RuntimeAuthoringCoverageLedger{
		Requirements: []changeplanmodel.RuntimeAuthoringCoverageRequirement{{
			RequirementID: "order.complete", CapabilityKeys: []string{"action.definition"},
			Resources:   []changeplanmodel.RuntimeAuthoringCoverageResource{{ResourceType: "object", ResourceKey: "order"}, {ResourceType: "action", ResourceKey: "order.complete"}},
			ScenarioIDs: []string{"order.complete.success", "order.complete.denied"},
		}},
	}
	report := ValidateRuntimeAuthoringCoverage(ledger, snapshot)
	if report.Status != "invalid" || report.CoveredCount != 1 || len(report.SourcelessResources) != 1 || len(report.UnreachableResources) != 1 || report.UnreachableResources[0].ResourceKey != "order.legacy" {
		t.Fatalf("report=%#v", report)
	}

	ledger.Requirements[0].Resources = append(ledger.Requirements[0].Resources, changeplanmodel.RuntimeAuthoringCoverageResource{ResourceType: "field", ResourceKey: "order.legacy"})
	snapshot.ResourceSources[2].SourceKind = "builder_v4"
	report = ValidateRuntimeAuthoringCoverage(ledger, snapshot)
	if report.Status != "complete" || report.RequirementCount != 1 || report.CoveredCount != 1 || len(report.SourcelessResources) != 0 || len(report.UnreachableResources) != 0 {
		t.Fatalf("complete report=%#v", report)
	}
}

func TestRuntimeAuthoringCoverageRejectsMissingAndUncoveredRequirements(t *testing.T) {
	snapshot := changeplanmodel.Snapshot{CapabilityKeys: []string{"schema.object"}, ResourceSources: []changeplanmodel.ResourceSource{{ResourceType: "object", ResourceKey: "order", SourceKind: "builder_v4"}}}
	if report := ValidateRuntimeAuthoringCoverage(nil, snapshot); report.Status != "invalid" || len(report.Issues) != 1 || report.Issues[0] != "coverage_ledger_required" {
		t.Fatalf("missing report=%#v", report)
	}
	ledger := &changeplanmodel.RuntimeAuthoringCoverageLedger{Requirements: []changeplanmodel.RuntimeAuthoringCoverageRequirement{{
		RequirementID: "", CapabilityKeys: []string{"missing"}, Resources: []changeplanmodel.RuntimeAuthoringCoverageResource{{ResourceType: "object", ResourceKey: "missing"}}, ScenarioIDs: []string{""},
	}}}
	report := ValidateRuntimeAuthoringCoverage(ledger, snapshot)
	if report.Status != "invalid" || report.CoveredCount != 0 || report.Entries[0].Status != "uncovered" || len(report.Entries[0].Issues) != 4 || len(report.UnreachableResources) != 1 {
		t.Fatalf("invalid report=%#v", report)
	}
}
