package validation

import (
	"sort"
	"strings"

	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
)

func ValidateRuntimeAuthoringCoverage(ledger *changeplanmodel.RuntimeAuthoringCoverageLedger, snapshot changeplanmodel.Snapshot) changeplanmodel.RuntimeAuthoringCoverageReport {
	report := changeplanmodel.RuntimeAuthoringCoverageReport{
		Status: "complete",
		Issues: []string{}, Entries: []changeplanmodel.RuntimeAuthoringCoverageRequirementReport{},
		SourcelessResources: []changeplanmodel.RuntimeAuthoringCoverageResource{}, UnreachableResources: []changeplanmodel.RuntimeAuthoringCoverageResource{},
	}
	if ledger == nil {
		report.Status = "invalid"
		report.Issues = append(report.Issues, "coverage_ledger_required")
		return report
	}
	if len(ledger.Requirements) == 0 {
		report.Status = "invalid"
		report.Issues = append(report.Issues, "requirements_required")
	}

	capabilities := make(map[string]bool, len(snapshot.CapabilityKeys))
	for _, key := range snapshot.CapabilityKeys {
		capabilities[strings.TrimSpace(key)] = true
	}
	resources := make(map[string]changeplanmodel.ResourceSource, len(snapshot.ResourceSources))
	for _, resource := range snapshot.ResourceSources {
		if resource.Disabled {
			continue
		}
		key := coverageResourceKey(resource.ResourceType, resource.ResourceKey)
		resources[key] = resource
		if kind := strings.TrimSpace(resource.SourceKind); kind == "" || kind == "unknown" {
			report.SourcelessResources = append(report.SourcelessResources, coverageResource(resource.ResourceType, resource.ResourceKey))
		}
	}

	seenRequirements, reachable := map[string]bool{}, map[string]bool{}
	for _, requirement := range ledger.Requirements {
		entry := changeplanmodel.RuntimeAuthoringCoverageRequirementReport{
			RequirementID: strings.TrimSpace(requirement.RequirementID), Status: "covered", Issues: []string{},
		}
		if entry.RequirementID == "" {
			entry.Issues = append(entry.Issues, "requirement_id_required")
		} else if seenRequirements[entry.RequirementID] {
			entry.Issues = append(entry.Issues, "requirement_id_duplicate")
		}
		seenRequirements[entry.RequirementID] = true
		if len(requirement.CapabilityKeys) == 0 {
			entry.Issues = append(entry.Issues, "capability_required")
		}
		for _, key := range requirement.CapabilityKeys {
			key = strings.TrimSpace(key)
			if key == "" || !capabilities[key] {
				entry.Issues = append(entry.Issues, "capability_not_found:"+key)
			}
		}
		if len(requirement.Resources) == 0 {
			entry.Issues = append(entry.Issues, "resource_required")
		}
		for _, resource := range requirement.Resources {
			key := coverageResourceKey(resource.ResourceType, resource.ResourceKey)
			if _, found := resources[key]; !found {
				entry.Issues = append(entry.Issues, "resource_not_found:"+strings.TrimSpace(resource.ResourceType)+":"+strings.TrimSpace(resource.ResourceKey))
				continue
			}
			reachable[key] = true
		}
		if len(requirement.ScenarioIDs) == 0 {
			entry.Issues = append(entry.Issues, "scenario_required")
		}
		for _, scenarioID := range requirement.ScenarioIDs {
			if strings.TrimSpace(scenarioID) == "" {
				entry.Issues = append(entry.Issues, "scenario_id_required")
			}
		}
		if len(entry.Issues) > 0 {
			entry.Status = "uncovered"
			report.Status = "invalid"
		} else {
			report.CoveredCount++
		}
		report.Entries = append(report.Entries, entry)
	}
	report.RequirementCount = len(report.Entries)
	for key, resource := range resources {
		if !reachable[key] {
			report.UnreachableResources = append(report.UnreachableResources, coverageResource(resource.ResourceType, resource.ResourceKey))
		}
	}
	coverageSortResources(report.SourcelessResources)
	coverageSortResources(report.UnreachableResources)
	if len(report.SourcelessResources) > 0 || len(report.UnreachableResources) > 0 {
		report.Status = "invalid"
	}
	return report
}

func coverageResourceKey(resourceType, resourceKey string) string {
	return strings.TrimSpace(resourceType) + "\x00" + strings.TrimSpace(resourceKey)
}

func coverageResource(resourceType, resourceKey string) changeplanmodel.RuntimeAuthoringCoverageResource {
	return changeplanmodel.RuntimeAuthoringCoverageResource{ResourceType: strings.TrimSpace(resourceType), ResourceKey: strings.TrimSpace(resourceKey)}
}

func coverageSortResources(resources []changeplanmodel.RuntimeAuthoringCoverageResource) {
	sort.Slice(resources, func(i, j int) bool {
		return coverageResourceKey(resources[i].ResourceType, resources[i].ResourceKey) < coverageResourceKey(resources[j].ResourceType, resources[j].ResourceKey)
	})
}
