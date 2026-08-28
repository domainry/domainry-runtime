package validation

import (
	"strings"
	"testing"

	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
)

func TestRuntimeAuthoringDeliveryRequiresBoundCompleteScenarioEvidence(t *testing.T) {
	binding := changeplanmodel.RuntimeAuthoringEvidenceBinding{
		RuntimeVersion: "runtime-v1", ContractHash: "contract-hash", InstanceHash: "instance-hash", SnapshotHash: "snapshot-hash", CoverageHash: "coverage-hash",
		ResourceHashes: map[string]string{"object:order": "resource-hash"},
	}
	coverage := changeplanmodel.RuntimeAuthoringCoverageLedger{Version: changeplanmodel.RuntimeAuthoringCoverageLedgerVersion, Requirements: []changeplanmodel.RuntimeAuthoringCoverageRequirement{{
		RequirementID: "order-lifecycle", CapabilityKeys: []string{"action.definition"},
		Resources: []changeplanmodel.RuntimeAuthoringCoverageResource{{ResourceType: "object", ResourceKey: "order"}}, ScenarioIDs: []string{"order-lifecycle"},
	}}}
	step := func(label, path string, actual int, responseHash string) changeplanmodel.RuntimeAuthoringScenarioStepEvidence {
		return changeplanmodel.RuntimeAuthoringScenarioStepEvidence{Label: label, Method: "GET", Path: path, ExpectedStatus: []int{actual}, ActualStatus: actual, RequestHash: strings.Repeat("a", 64), ResponseHash: responseHash, Passed: true}
	}
	replayOne, replayTwo := step("replay-one", "/objects/order/records", 200, strings.Repeat("b", 64)), step("replay-two", "/objects/order/records", 200, strings.Repeat("b", 64))
	replayOne.IdempotencyKey, replayTwo.IdempotencyKey = "order-create-1", "order-create-1"
	replayTwo.IdempotencyReplayed = true
	evidence := changeplanmodel.RuntimeAuthoringDeliveryEvidence{
		Version: changeplanmodel.RuntimeAuthoringDeliveryEvidenceVersion, Binding: binding, Coverage: coverage,
		Scenarios: []changeplanmodel.RuntimeAuthoringScenarioEvidence{{
			Version: changeplanmodel.RuntimeAuthoringScenarioEvidenceVersion, ScenarioID: "order-lifecycle", Passed: true,
			Categories: append([]string(nil), changeplanmodel.RuntimeAuthoringRequiredScenarioCategories...), BeforeStateHash: "state", AfterStateHash: "state",
			Steps: []changeplanmodel.RuntimeAuthoringScenarioStepEvidence{
				step("success", "/objects/order/records/order-1", 200, strings.Repeat("c", 64)),
				step("denied", "/objects/order/records/order-1", 403, strings.Repeat("d", 64)),
				step("precondition", "/objects/order/records/order-1/actions/complete", 422, strings.Repeat("e", 64)),
				replayOne, replayTwo,
				step("audit", "/audit-events", 200, strings.Repeat("f", 64)),
				step("event", "/events", 200, strings.Repeat("1", 64)),
				step("outbox", "/integration-outbox", 200, strings.Repeat("2", 64)),
			},
		}},
	}
	report := ValidateRuntimeAuthoringDelivery(evidence, binding, true)
	if !report.Valid || report.Status != "valid" || report.EvidenceHash == "" {
		t.Fatalf("report=%#v", report)
	}

	evidence.Binding.InstanceHash = "stale"
	evidence.Scenarios[0].AfterStateHash = "changed"
	evidence.Scenarios[0].Steps[1].ActualStatus = 500
	report = ValidateRuntimeAuthoringDelivery(evidence, binding, false)
	if report.Valid || report.Checks["global_validation"] != "invalid" || report.Checks["binding"] != "invalid" || report.Checks["scenarios"] != "invalid" {
		t.Fatalf("invalid report=%#v", report)
	}
}
