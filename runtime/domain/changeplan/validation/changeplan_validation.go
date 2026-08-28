package validation

import (
	"encoding/json"
	"fmt"
	"strings"

	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	changeplanpolicy "github.com/domainry/domainry-runtime/runtime/domain/changeplan/policy"
)

func ValidateBusinessSystemChangePlan(plan changeplanmodel.BusinessSystemChangePlan, snapshot changeplanmodel.Snapshot, graph changeplanmodel.ReferenceGraph) changeplanmodel.BusinessChangePlanValidation {
	validator := businessChangePlanValidator{plan: plan, snapshot: snapshot, graph: graph, result: changeplanmodel.BusinessChangePlanValidation{CurrentSnapshotHash: snapshot.SnapshotHash, CurrentGraphHash: graph.Hash, RiskSummary: map[string]int{}, Diffs: []changeplanmodel.BusinessChangeDiff{}, Issues: []changeplanmodel.BusinessChangePlanValidationIssue{}}}
	validator.validateIdentityAndVersions()
	validator.validateItems()
	validator.result.Valid = !validator.hasErrors()
	validator.result.ApplyAllowed = validator.result.Valid && (!validator.requiresReview() || plan.Reviewed)
	return validator.result
}

type businessChangePlanValidator struct {
	plan     changeplanmodel.BusinessSystemChangePlan
	snapshot changeplanmodel.Snapshot
	graph    changeplanmodel.ReferenceGraph
	result   changeplanmodel.BusinessChangePlanValidation
}

func (validator *businessChangePlanValidator) issue(itemID, fieldPath, code string, params ...string) {
	values := map[string]string{}
	for index := 0; index+1 < len(params); index += 2 {
		values[params[index]] = params[index+1]
	}
	validator.result.Issues = append(validator.result.Issues, changeplanmodel.BusinessChangePlanValidationIssue{ItemID: itemID, FieldPath: fieldPath, Code: code, Severity: "error", Params: values})
}

func (validator *businessChangePlanValidator) validateIdentityAndVersions() {
	if validator.plan.PlanVersion != changeplanmodel.BusinessSystemChangePlanVersion {
		validator.issue("", "plan_version", "backend.change_plan.version_invalid", "expected", changeplanmodel.BusinessSystemChangePlanVersion, "actual", validator.plan.PlanVersion)
	}
	if strings.TrimSpace(validator.plan.PlanID) == "" {
		validator.issue("", "plan_id", "backend.change_plan.plan_id_required")
	}
	if strings.TrimSpace(validator.plan.BusinessReason) == "" {
		validator.issue("", "business_reason", "backend.change_plan.business_reason_required")
	}
	if validator.plan.SnapshotHash != validator.snapshot.SnapshotHash {
		validator.issue("", "snapshot_hash", "backend.change_plan.snapshot_stale", "expected", validator.snapshot.SnapshotHash, "actual", validator.plan.SnapshotHash)
	}
	if validator.plan.ReferenceGraphHash != validator.graph.Hash {
		validator.issue("", "reference_graph_hash", "backend.change_plan.reference_graph_stale", "expected", validator.graph.Hash, "actual", validator.plan.ReferenceGraphHash)
	}
	if validator.plan.RuntimeVersion != validator.snapshot.RuntimeVersion {
		validator.issue("", "runtime_version", "backend.change_plan.runtime_version_mismatch", "expected", validator.snapshot.RuntimeVersion, "actual", validator.plan.RuntimeVersion)
	}
	if validator.plan.AuthoringContractVersion != validator.snapshot.AuthoringContractVersion || validator.plan.AuthoringContractHash != validator.snapshot.AuthoringContractHash {
		validator.issue("", "authoring_contract_hash", "backend.change_plan.authoring_contract_mismatch", "expected_version", validator.snapshot.AuthoringContractVersion, "expected_hash", validator.snapshot.AuthoringContractHash)
	}
	if frontend := validator.snapshot.FrontendCapabilities; frontend.Manifest != nil {
		if validator.plan.FrontendManifestVersion != frontend.Manifest.ManifestVersion || validator.plan.FrontendManifestHash != frontend.ManifestHash {
			validator.issue("", "frontend_manifest_hash", "backend.change_plan.frontend_manifest_stale", "expected_version", frontend.Manifest.ManifestVersion, "expected_hash", frontend.ManifestHash, "actual_version", validator.plan.FrontendManifestVersion, "actual_hash", validator.plan.FrontendManifestHash)
		}
	}
	if len(validator.plan.Items) == 0 {
		validator.issue("", "items", "backend.change_plan.items_required")
	}
	if len(validator.snapshot.HiddenResourceCategories) > 0 {
		validator.issue("", "snapshot_hash", "backend.change_plan.snapshot_incomplete", "hidden_categories", strings.Join(validator.snapshot.HiddenResourceCategories, ","))
	}
	if validator.plan.Reviewed && validator.requiresReview() && strings.TrimSpace(validator.plan.ReviewedBy) == "" {
		validator.issue("", "reviewed_by", "backend.change_plan.reviewer_required")
	}
}

func (validator *businessChangePlanValidator) hasErrors() bool {
	for _, issue := range validator.result.Issues {
		if issue.Severity == "error" {
			return true
		}
	}
	return false
}

func (validator *businessChangePlanValidator) requiresReview() bool {
	for _, item := range validator.plan.Items {
		if businessChangeRequiresReview(item) {
			return true
		}
	}
	return false
}

func businessChangeRequiresReview(item changeplanmodel.BusinessSystemChangeItem) bool {
	return item.Operation == "archive" || item.Operation == "delete" || item.ChangeKind == "breaking" || item.ChangeKind == "destructive" || item.RiskLevel == "high" || item.RiskLevel == "critical" || changeplanpolicy.ChangePlanSensitiveUpdate(item.Operation, changeplanpolicy.ChangePlanCanonicalResourceType(item.ResourceType), item.Before, item.After)
}

func rawJSONPresent(value json.RawMessage) bool {
	return len(strings.TrimSpace(string(value))) > 0 && string(value) != "null"
}
func changePlanValueAllowed(value string, allowed ...string) bool {
	for _, candidate := range allowed {
		if value == candidate {
			return true
		}
	}
	return false
}
func changePlanIndexPath(index int, field string) string {
	return fmt.Sprintf("items[%d].%s", index, field)
}

func RuntimeBusinessChangeOperations() []string {
	return changeplanmodel.BusinessChangeOperations()
}
func RuntimeBusinessChangeKinds() []string {
	return changeplanmodel.BusinessChangeKinds()
}
func RuntimeBusinessChangeRiskLevels() []string { return changeplanmodel.BusinessChangeRiskLevels() }
func RuntimeBusinessResourceOwners() []string {
	return changeplanmodel.BusinessResourceOwners()
}
