package validation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
)

func ValidateRuntimeAuthoringDelivery(evidence changeplanmodel.RuntimeAuthoringDeliveryEvidence, expected changeplanmodel.RuntimeAuthoringEvidenceBinding, globalValid bool) changeplanmodel.RuntimeAuthoringDeliveryReport {
	report := changeplanmodel.RuntimeAuthoringDeliveryReport{
		Version: changeplanmodel.RuntimeAuthoringDeliveryEvidenceVersion, Status: "valid", Binding: expected,
		Checks: map[string]string{"global_validation": "ok", "binding": "ok", "coverage": "ok", "scenarios": "ok", "required_categories": "ok"}, Issues: []string{},
	}
	raw, _ := json.Marshal(evidence)
	sum := sha256.Sum256(raw)
	report.EvidenceHash = hex.EncodeToString(sum[:])
	if !globalValid {
		deliveryIssue(&report, "global_validation", "global_validation_invalid")
	}
	if strings.TrimSpace(evidence.Version) != changeplanmodel.RuntimeAuthoringDeliveryEvidenceVersion {
		deliveryIssue(&report, "binding", "delivery_version_invalid")
	}
	validateDeliveryBinding(&report, evidence.Binding, expected)

	declared := map[string]bool{}
	for _, requirement := range evidence.Coverage.Requirements {
		for _, scenarioID := range requirement.ScenarioIDs {
			declared[strings.TrimSpace(scenarioID)] = true
		}
	}
	if len(declared) == 0 {
		deliveryIssue(&report, "coverage", "coverage_scenarios_required")
	}
	seen, categories := map[string]bool{}, map[string]bool{}
	for index, scenario := range evidence.Scenarios {
		prefix := fmt.Sprintf("scenarios[%d]", index)
		id := strings.TrimSpace(scenario.ScenarioID)
		if strings.TrimSpace(scenario.Version) != changeplanmodel.RuntimeAuthoringScenarioEvidenceVersion {
			deliveryIssue(&report, "scenarios", prefix+".version_invalid")
		}
		if id == "" || !declared[id] {
			deliveryIssue(&report, "scenarios", prefix+".scenario_not_declared")
		} else if seen[id] {
			deliveryIssue(&report, "scenarios", prefix+".scenario_duplicate")
		}
		seen[id] = true
		if !scenario.Passed || len(scenario.Steps) == 0 {
			deliveryIssue(&report, "scenarios", prefix+".scenario_not_passed")
		}
		for _, category := range scenario.Categories {
			category = strings.TrimSpace(category)
			if !deliveryScenarioCategoryAllowed(category) {
				deliveryIssue(&report, "required_categories", prefix+".category_invalid:"+category)
				continue
			}
			categories[category] = true
		}
		validateDeliveryScenario(&report, prefix, scenario)
	}
	for id := range declared {
		if !seen[id] {
			deliveryIssue(&report, "coverage", "scenario_evidence_missing:"+id)
		}
	}
	for _, category := range changeplanmodel.RuntimeAuthoringRequiredScenarioCategories {
		if !categories[category] {
			deliveryIssue(&report, "required_categories", "scenario_category_missing:"+category)
		}
	}
	report.Valid = len(report.Issues) == 0
	if !report.Valid {
		report.Status = "invalid"
	}
	return report
}

func validateDeliveryBinding(report *changeplanmodel.RuntimeAuthoringDeliveryReport, actual, expected changeplanmodel.RuntimeAuthoringEvidenceBinding) {
	for name, values := range map[string][2]string{
		"runtime_version": {actual.RuntimeVersion, expected.RuntimeVersion}, "contract_hash": {actual.ContractHash, expected.ContractHash},
		"instance_hash": {actual.InstanceHash, expected.InstanceHash}, "snapshot_hash": {actual.SnapshotHash, expected.SnapshotHash}, "coverage_hash": {actual.CoverageHash, expected.CoverageHash},
	} {
		if strings.TrimSpace(values[0]) == "" || values[0] != values[1] {
			deliveryIssue(report, "binding", "binding_mismatch:"+name)
		}
	}
	if len(actual.ResourceHashes) != len(expected.ResourceHashes) {
		deliveryIssue(report, "binding", "binding_mismatch:resource_hashes")
		return
	}
	for key, hash := range expected.ResourceHashes {
		if actual.ResourceHashes[key] != hash {
			deliveryIssue(report, "binding", "resource_hash_mismatch:"+key)
		}
	}
}

func validateDeliveryScenario(report *changeplanmodel.RuntimeAuthoringDeliveryReport, prefix string, scenario changeplanmodel.RuntimeAuthoringScenarioEvidence) {
	seenLabels, idempotent := map[string]bool{}, map[string][]bool{}
	statuses, paths := []int{}, []string{}
	for index, step := range scenario.Steps {
		path := fmt.Sprintf("%s.steps[%d]", prefix, index)
		label := strings.TrimSpace(step.Label)
		if label == "" || seenLabels[label] {
			deliveryIssue(report, "scenarios", path+".label_invalid")
		}
		seenLabels[label] = true
		if !step.Passed || !deliveryStatusExpected(step.ActualStatus, step.ExpectedStatus) {
			deliveryIssue(report, "scenarios", path+".status_not_passed")
		}
		if !deliverySHA256(step.RequestHash) || !deliverySHA256(step.ResponseHash) {
			deliveryIssue(report, "scenarios", path+".hash_invalid")
		}
		statuses, paths = append(statuses, step.ActualStatus), append(paths, strings.ToLower(strings.TrimSpace(step.Path)))
		if key := strings.TrimSpace(step.IdempotencyKey); key != "" {
			idempotent[key] = append(idempotent[key], step.IdempotencyReplayed)
		}
	}
	for _, category := range scenario.Categories {
		switch strings.TrimSpace(category) {
		case "success":
			if !deliveryHasStatus(statuses, 200, 201, 202, 204) {
				deliveryIssue(report, "scenarios", prefix+".success_missing")
			}
		case "permission_denied":
			if !deliveryHasStatus(statuses, 403, 404) {
				deliveryIssue(report, "scenarios", prefix+".permission_denial_missing")
			}
		case "precondition_rejected":
			if !deliveryHasStatus(statuses, 409, 412, 422) {
				deliveryIssue(report, "scenarios", prefix+".precondition_rejection_missing")
			}
		case "atomic_rollback":
			if scenario.BeforeStateHash == "" || scenario.BeforeStateHash != scenario.AfterStateHash {
				deliveryIssue(report, "scenarios", prefix+".rollback_state_mismatch")
			}
		case "idempotent_replay":
			if !deliveryHasIdempotentReplay(idempotent) {
				deliveryIssue(report, "scenarios", prefix+".idempotent_replay_missing")
			}
		case "audit", "event", "outbox":
			if !deliveryHasPath(paths, category) {
				deliveryIssue(report, "scenarios", prefix+"."+category+"_evidence_missing")
			}
		}
	}
}

func deliveryIssue(report *changeplanmodel.RuntimeAuthoringDeliveryReport, check, issue string) {
	report.Checks[check] = "invalid"
	report.Issues = append(report.Issues, issue)
}

func deliveryScenarioCategoryAllowed(value string) bool {
	for _, category := range changeplanmodel.RuntimeAuthoringRequiredScenarioCategories {
		if value == category {
			return true
		}
	}
	return false
}

func deliverySHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}

func deliveryStatusExpected(actual int, expected []int) bool {
	for _, status := range expected {
		if actual == status {
			return true
		}
	}
	return false
}

func deliveryHasStatus(actual []int, expected ...int) bool {
	for _, status := range actual {
		if deliveryStatusExpected(status, expected) {
			return true
		}
	}
	return false
}

func deliveryHasPath(paths []string, part string) bool {
	for _, path := range paths {
		if strings.Contains(path, part) {
			return true
		}
	}
	return false
}

func deliveryHasIdempotentReplay(values map[string][]bool) bool {
	for _, replayed := range values {
		if len(replayed) >= 2 {
			for _, value := range replayed {
				if value {
					return true
				}
			}
		}
	}
	return false
}
