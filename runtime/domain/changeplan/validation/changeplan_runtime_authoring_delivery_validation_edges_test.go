package validation

import (
	"strings"
	"testing"

	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
)

func TestRuntimeAuthoringCoverageCoversEmptyDuplicateAndMissingCollections(t *testing.T) {
	snapshot := changeplanmodel.Snapshot{
		CapabilityKeys:  []string{"schema.object"},
		ResourceSources: []changeplanmodel.ResourceSource{{ResourceType: "object", ResourceKey: "asset", SourceKind: " "}},
	}
	for _, ledger := range []*changeplanmodel.RuntimeAuthoringCoverageLedger{
		{Version: changeplanmodel.RuntimeAuthoringCoverageLedgerVersion},
		{Version: changeplanmodel.RuntimeAuthoringCoverageLedgerVersion, Requirements: []changeplanmodel.RuntimeAuthoringCoverageRequirement{
			{RequirementID: "asset", CapabilityKeys: nil, Resources: nil, ScenarioIDs: nil},
			{RequirementID: "asset", CapabilityKeys: []string{""}, Resources: []changeplanmodel.RuntimeAuthoringCoverageResource{{ResourceType: "object", ResourceKey: "asset"}}, ScenarioIDs: []string{"asset.success"}},
		}},
	} {
		if report := ValidateRuntimeAuthoringCoverage(ledger, snapshot); report.Status != "invalid" {
			t.Fatalf("ledger=%#v report=%#v", ledger, report)
		}
	}
	ordered := ValidateRuntimeAuthoringCoverage(&changeplanmodel.RuntimeAuthoringCoverageLedger{Version: changeplanmodel.RuntimeAuthoringCoverageLedgerVersion}, changeplanmodel.Snapshot{ResourceSources: []changeplanmodel.ResourceSource{
		{ResourceType: "object", ResourceKey: "z", SourceKind: "builder"},
		{ResourceType: "object", ResourceKey: "a", SourceKind: "builder"},
	}})
	if len(ordered.UnreachableResources) != 2 || ordered.UnreachableResources[0].ResourceKey != "a" {
		t.Fatalf("ordered report=%#v", ordered)
	}
}

func TestRuntimeAuthoringDeliveryRejectsEveryUntrustedEvidenceDimension(t *testing.T) {
	binding, valid := runtimeAuthoringDeliveryEdgeFixture()

	empty := changeplanmodel.RuntimeAuthoringDeliveryEvidence{}
	if report := ValidateRuntimeAuthoringDelivery(empty, binding, false); report.Valid || report.Status != "invalid" || len(report.Issues) == 0 {
		t.Fatalf("empty report=%#v", report)
	}

	undeclared := valid
	undeclared.Scenarios = append(append([]changeplanmodel.RuntimeAuthoringScenarioEvidence(nil), valid.Scenarios...), valid.Scenarios[0])
	undeclared.Scenarios[0].Version = "wrong"
	undeclared.Scenarios[0].ScenarioID = ""
	undeclared.Scenarios[0].Passed = false
	undeclared.Scenarios[1].ScenarioID = "undeclared"
	undeclared.Scenarios[1].Categories = []string{"invalid"}
	if report := ValidateRuntimeAuthoringDelivery(undeclared, binding, true); report.Valid {
		t.Fatalf("undeclared report=%#v", report)
	}

	duplicate := valid
	duplicate.Scenarios = append(append([]changeplanmodel.RuntimeAuthoringScenarioEvidence(nil), valid.Scenarios...), valid.Scenarios[0])
	if report := ValidateRuntimeAuthoringDelivery(duplicate, binding, true); report.Valid || !runtimeDeliveryIssuesContain(report.Issues, "scenario_duplicate") {
		t.Fatalf("duplicate report=%#v", report)
	}

	missing := valid
	missing.Coverage.Requirements[0].ScenarioIDs = append(missing.Coverage.Requirements[0].ScenarioIDs, "asset.missing")
	missing.Scenarios[0].Categories = nil
	missing.Scenarios[0].Steps = nil
	if report := ValidateRuntimeAuthoringDelivery(missing, binding, true); report.Valid || !runtimeDeliveryIssuesContain(report.Issues, "scenario_evidence_missing") || !runtimeDeliveryIssuesContain(report.Issues, "scenario_category_missing") {
		t.Fatalf("missing report=%#v", report)
	}

	for _, mutate := range []func(*changeplanmodel.RuntimeAuthoringDeliveryEvidence){
		func(evidence *changeplanmodel.RuntimeAuthoringDeliveryEvidence) { evidence.Binding.RuntimeVersion = "" },
		func(evidence *changeplanmodel.RuntimeAuthoringDeliveryEvidence) {
			evidence.Binding.ContractHash = "stale"
		},
		func(evidence *changeplanmodel.RuntimeAuthoringDeliveryEvidence) {
			evidence.Binding.ResourceHashes = nil
		},
		func(evidence *changeplanmodel.RuntimeAuthoringDeliveryEvidence) {
			evidence.Binding.ResourceHashes["object:asset"] = "stale"
		},
	} {
		candidate := runtimeAuthoringDeliveryClone(valid)
		mutate(&candidate)
		if report := ValidateRuntimeAuthoringDelivery(candidate, binding, true); report.Valid || report.Checks["binding"] != "invalid" {
			t.Fatalf("binding report=%#v", report)
		}
	}
}

func TestRuntimeAuthoringDeliveryRejectsForgedStepProofs(t *testing.T) {
	binding, valid := runtimeAuthoringDeliveryEdgeFixture()
	base := valid.Scenarios[0]
	invalidStep := changeplanmodel.RuntimeAuthoringScenarioStepEvidence{
		Label: " ", Path: "/none", ExpectedStatus: []int{200}, ActualStatus: 500,
		RequestHash: "short", ResponseHash: strings.Repeat("z", 64), Passed: false,
		IdempotencyKey: "single", IdempotencyReplayed: false,
	}
	invalidStep.Label = "duplicate"
	base.Steps = []changeplanmodel.RuntimeAuthoringScenarioStepEvidence{invalidStep, invalidStep, invalidStep}
	base.Steps[1].Label = "duplicate"
	base.Steps[1].IdempotencyKey = "other"
	base.Steps[1].RequestHash = strings.Repeat("a", 64)
	base.Steps[1].ResponseHash = "short"
	base.Steps[1].Passed = true
	base.Steps[2].Label = " "
	base.Steps[2].IdempotencyKey = ""
	base.BeforeStateHash, base.AfterStateHash = "before", "after"
	valid.Scenarios[0] = base
	report := ValidateRuntimeAuthoringDelivery(valid, binding, true)
	if report.Valid || report.Checks["scenarios"] != "invalid" {
		t.Fatalf("forged report=%#v", report)
	}
	for _, expected := range []string{"label_invalid", "status_not_passed", "hash_invalid", "success_missing", "permission_denial_missing", "precondition_rejection_missing", "rollback_state_mismatch", "idempotent_replay_missing", "audit_evidence_missing", "event_evidence_missing", "outbox_evidence_missing"} {
		if !runtimeDeliveryIssuesContain(report.Issues, expected) {
			t.Fatalf("missing %s in %#v", expected, report.Issues)
		}
	}

	blankRollback := runtimeAuthoringDeliveryClone(valid)
	blankRollback.Scenarios[0] = runtimeAuthoringDeliveryEdgeFixtureScenario()
	blankRollback.Scenarios[0].BeforeStateHash = ""
	blankRollback.Scenarios[0].AfterStateHash = "different"
	if result := ValidateRuntimeAuthoringDelivery(blankRollback, binding, true); result.Valid || !runtimeDeliveryIssuesContain(result.Issues, "rollback_state_mismatch") {
		t.Fatalf("blank rollback report=%#v", result)
	}

	noReplay := runtimeAuthoringDeliveryClone(valid)
	noReplay.Scenarios[0] = runtimeAuthoringDeliveryEdgeFixtureScenario()
	for index := range noReplay.Scenarios[0].Steps {
		noReplay.Scenarios[0].Steps[index].IdempotencyReplayed = false
	}
	if result := ValidateRuntimeAuthoringDelivery(noReplay, binding, true); result.Valid || !runtimeDeliveryIssuesContain(result.Issues, "idempotent_replay_missing") {
		t.Fatalf("no replay report=%#v", result)
	}
}

func runtimeAuthoringDeliveryEdgeFixture() (changeplanmodel.RuntimeAuthoringEvidenceBinding, changeplanmodel.RuntimeAuthoringDeliveryEvidence) {
	binding := changeplanmodel.RuntimeAuthoringEvidenceBinding{
		RuntimeVersion: "runtime", ContractHash: "contract", InstanceHash: "instance", SnapshotHash: "snapshot", CoverageHash: "coverage",
		ResourceHashes: map[string]string{"object:asset": "resource"},
	}
	evidence := changeplanmodel.RuntimeAuthoringDeliveryEvidence{
		Version: changeplanmodel.RuntimeAuthoringDeliveryEvidenceVersion, Binding: binding,
		Coverage:  changeplanmodel.RuntimeAuthoringCoverageLedger{Version: changeplanmodel.RuntimeAuthoringCoverageLedgerVersion, Requirements: []changeplanmodel.RuntimeAuthoringCoverageRequirement{{RequirementID: "asset", ScenarioIDs: []string{"asset.lifecycle"}}}},
		Scenarios: []changeplanmodel.RuntimeAuthoringScenarioEvidence{runtimeAuthoringDeliveryEdgeFixtureScenario()},
	}
	return binding, evidence
}

func runtimeAuthoringDeliveryEdgeFixtureScenario() changeplanmodel.RuntimeAuthoringScenarioEvidence {
	hash := func(value string) string { return strings.Repeat(value, 64) }
	step := func(label, path string, status int) changeplanmodel.RuntimeAuthoringScenarioStepEvidence {
		return changeplanmodel.RuntimeAuthoringScenarioStepEvidence{Label: label, Path: path, ExpectedStatus: []int{status}, ActualStatus: status, RequestHash: hash("a"), ResponseHash: hash("b"), Passed: true}
	}
	replayOne, replayTwo := step("replay-one", "/objects/assets", 200), step("replay-two", "/objects/assets", 200)
	replayOne.IdempotencyKey, replayTwo.IdempotencyKey, replayTwo.IdempotencyReplayed = "key", "key", true
	return changeplanmodel.RuntimeAuthoringScenarioEvidence{
		Version: changeplanmodel.RuntimeAuthoringScenarioEvidenceVersion, ScenarioID: "asset.lifecycle", Passed: true,
		Categories: append([]string(nil), changeplanmodel.RuntimeAuthoringRequiredScenarioCategories...), BeforeStateHash: "state", AfterStateHash: "state",
		Steps: []changeplanmodel.RuntimeAuthoringScenarioStepEvidence{
			step("success", "/objects/assets/asset-1", 201), step("denied", "/objects/assets/asset-1", 404), step("precondition", "/objects/assets/asset-1/actions/update", 409),
			replayOne, replayTwo, step("audit", "/audit-events", 200), step("event", "/events", 200), step("outbox", "/outbox", 200),
		},
	}
}

func runtimeAuthoringDeliveryClone(source changeplanmodel.RuntimeAuthoringDeliveryEvidence) changeplanmodel.RuntimeAuthoringDeliveryEvidence {
	result := source
	result.Binding.ResourceHashes = map[string]string{}
	for key, value := range source.Binding.ResourceHashes {
		result.Binding.ResourceHashes[key] = value
	}
	result.Coverage.Requirements = append([]changeplanmodel.RuntimeAuthoringCoverageRequirement(nil), source.Coverage.Requirements...)
	result.Scenarios = append([]changeplanmodel.RuntimeAuthoringScenarioEvidence(nil), source.Scenarios...)
	for index := range result.Scenarios {
		result.Scenarios[index].Categories = append([]string(nil), source.Scenarios[index].Categories...)
		result.Scenarios[index].Steps = append([]changeplanmodel.RuntimeAuthoringScenarioStepEvidence(nil), source.Scenarios[index].Steps...)
	}
	return result
}

func runtimeDeliveryIssuesContain(issues []string, expected string) bool {
	for _, issue := range issues {
		if strings.Contains(issue, expected) {
			return true
		}
	}
	return false
}
