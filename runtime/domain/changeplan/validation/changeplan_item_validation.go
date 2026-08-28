package validation

import (
	"fmt"
	changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"
	"strings"

	changeplanpolicy "github.com/domainry/domainry-runtime/runtime/domain/changeplan/policy"
)

func (validator *businessChangePlanValidator) validateItems() {
	capabilities := map[string]bool{}
	for _, key := range validator.snapshot.CapabilityKeys {
		capabilities[key] = true
	}
	seen := map[string]bool{}
	for index, item := range validator.plan.Items {
		prefix := fmt.Sprintf("items[%d]", index)
		if strings.TrimSpace(item.ItemID) == "" || seen[item.ItemID] {
			validator.issue(item.ItemID, prefix+".item_id", "backend.change_plan.item_id_invalid")
		} else {
			seen[item.ItemID] = true
		}
		if !changePlanValueAllowed(item.Operation, RuntimeBusinessChangeOperations()...) {
			validator.issue(item.ItemID, prefix+".operation", "backend.change_plan.operation_invalid", "actual", item.Operation)
		}
		if !changePlanValueAllowed(item.ChangeKind, RuntimeBusinessChangeKinds()...) {
			validator.issue(item.ItemID, prefix+".change_kind", "backend.change_plan.change_kind_invalid", "actual", item.ChangeKind)
		}
		if expected := changeplanpolicy.ChangePlanExpectedChangeKind(item.Operation, businessReferenceResourceType(item.ResourceType), item.Before, item.After); expected != "" && item.ChangeKind != expected {
			validator.issue(item.ItemID, prefix+".change_kind", "backend.change_plan.change_kind_understated", "expected", expected, "actual", item.ChangeKind)
		}
		if !changePlanValueAllowed(item.RiskLevel, RuntimeBusinessChangeRiskLevels()...) {
			validator.issue(item.ItemID, prefix+".risk_level", "backend.change_plan.risk_level_invalid", "actual", item.RiskLevel)
		} else {
			validator.result.RiskSummary[item.RiskLevel]++
		}
		if changeplanpolicy.ChangePlanSensitiveUpdate(item.Operation, businessReferenceResourceType(item.ResourceType), item.Before, item.After) && item.RiskLevel != "high" && item.RiskLevel != "critical" {
			validator.issue(item.ItemID, prefix+".risk_level", "backend.change_plan.risk_understated", "minimum", "high", "actual", item.RiskLevel)
		}
		if strings.TrimSpace(item.ResourceType) == "" {
			validator.issue(item.ItemID, prefix+".resource_type", "backend.change_plan.resource_type_required")
		}
		if strings.TrimSpace(item.ResourceKey) == "" {
			validator.issue(item.ItemID, prefix+".resource_key", "backend.change_plan.resource_key_required")
		}
		validator.validateOwner(index, item)
		validator.validateExpectedResourceVersion(index, item)
		if !capabilities[item.CapabilityKey] {
			validator.issue(item.ItemID, prefix+".capability_key", "backend.change_plan.capability_unknown", "capability", item.CapabilityKey)
		}
		if (item.Operation == "update" || item.Operation == "archive" || item.Operation == "delete") && !rawJSONPresent(item.Before) {
			validator.issue(item.ItemID, prefix+".before", "backend.change_plan.before_required")
		}
		if (item.Operation == "create" || item.Operation == "update") && !rawJSONPresent(item.After) {
			validator.issue(item.ItemID, prefix+".after", "backend.change_plan.after_required")
		}
		if len(item.ValidationMethods) == 0 && item.Operation != "noop" {
			validator.issue(item.ItemID, prefix+".validation_methods", "backend.change_plan.validation_required")
		}
		if businessChangeRequiresReview(item) && !validator.plan.Reviewed {
			validator.issue(item.ItemID, prefix+".operation", "backend.change_plan.review_required")
		}
		validator.validateReferenceImpact(index, item)
		validator.validateRuntimeDependencies(index, item)
		validator.validateFrontendCompatibility(index, item)
	}
	validator.validateOrder("release_order", validator.plan.ReleaseOrder, seen)
	validator.validateOrder("rollback_order", validator.plan.RollbackOrder, seen)
}

func (validator *businessChangePlanValidator) validateExpectedResourceVersion(index int, item changeplanmodel.BusinessSystemChangeItem) {
	if item.Operation != "update" && item.Operation != "archive" && item.Operation != "delete" {
		return
	}
	for _, source := range validator.snapshot.ResourceSources {
		if businessReferenceResourceType(source.ResourceType) != businessReferenceResourceType(item.ResourceType) || source.ResourceKey != item.ResourceKey || source.SchemaHash == "" {
			continue
		}
		path := changePlanIndexPath(index, "expected_resource_hash")
		if strings.TrimSpace(item.ExpectedResourceHash) == "" {
			validator.issue(item.ItemID, path, "backend.change_plan.resource_version_required", "expected", source.SchemaHash)
			return
		}
		if item.ExpectedResourceHash != source.SchemaHash {
			validator.issue(item.ItemID, path, "backend.change_plan.resource_version_conflict", "expected", source.SchemaHash, "actual", item.ExpectedResourceHash)
			return
		}
	}
}

func (validator *businessChangePlanValidator) validateFrontendCompatibility(index int, item changeplanmodel.BusinessSystemChangeItem) {
	supportKey := strings.TrimSpace(item.FrontendSupportKey)
	if supportKey == "" {
		return
	}
	frontend := validator.snapshot.FrontendCapabilities
	if frontend.Manifest == nil {
		validator.issue(item.ItemID, changePlanIndexPath(index, "frontend_support_key"), "backend.change_plan.frontend_manifest_unknown", "support_key", supportKey)
		return
	}
	found := false
	for _, entry := range frontend.Manifest.Entries {
		if entry.SupportKey != supportKey {
			continue
		}
		for _, capabilityKey := range entry.CapabilityKeys {
			if capabilityKey == item.CapabilityKey {
				found = true
			}
		}
	}
	if !found {
		validator.issue(item.ItemID, changePlanIndexPath(index, "frontend_support_key"), "backend.change_plan.frontend_support_missing", "support_key", supportKey, "capability", item.CapabilityKey, "manifest_hash", frontend.ManifestHash)
	}
}

func (validator *businessChangePlanValidator) validateOwner(index int, item changeplanmodel.BusinessSystemChangeItem) {
	prefix := changePlanIndexPath(index, "resource_owner")
	if !changePlanValueAllowed(item.ResourceOwner, RuntimeBusinessResourceOwners()...) {
		validator.issue(item.ItemID, prefix, "backend.change_plan.owner_invalid", "actual", item.ResourceOwner)
		return
	}
	if item.ResourceOwner == "unknown" {
		validator.issue(item.ItemID, prefix, "backend.change_plan.owner_unknown_protected")
	}
	if item.ResourceOwner == "plugin" && item.Operation != "noop" {
		validator.issue(item.ItemID, prefix, "backend.change_plan.plugin_lifecycle_required")
	}
	if item.ResourceOwner != "builder" && item.Operation != "noop" && !item.OwnerAuthorized {
		validator.issue(item.ItemID, changePlanIndexPath(index, "owner_authorized"), "backend.change_plan.owner_authorization_required", "owner", item.ResourceOwner)
	}
	for _, source := range validator.snapshot.ResourceSources {
		if businessReferenceResourceType(source.ResourceType) == businessReferenceResourceType(item.ResourceType) && source.ResourceKey == item.ResourceKey && source.SourceKind != "" && source.SourceKind != "unknown" {
			expectedOwner := changeplanpolicy.ChangePlanResourceOwnerForSourceKind(source.SourceKind)
			if expectedOwner != item.ResourceOwner {
				validator.issue(item.ItemID, prefix, "backend.change_plan.owner_mismatch", "expected", expectedOwner, "actual", item.ResourceOwner)
				return
			}
		}
	}
}

func (validator *businessChangePlanValidator) validateReferenceImpact(index int, item changeplanmodel.BusinessSystemChangeItem) {
	if item.Operation != "archive" && item.Operation != "delete" {
		return
	}
	impact := changePlanReferenceImpact(validator.graph, businessReferenceResourceType(item.ResourceType), item.ResourceKey)
	if !impact.DeletionBlocked {
		return
	}
	if item.Replacement == nil || strings.TrimSpace(item.Replacement.ResourceKey) == "" {
		validator.issue(item.ItemID, changePlanIndexPath(index, "replacement"), "backend.change_plan.replacement_required", "direct_consumers", fmt.Sprint(len(impact.DirectConsumers)), "indirect_consumers", fmt.Sprint(len(impact.IndirectConsumers)), "graph_hash", impact.GraphHash)
		return
	}
	validator.validateReferenceMigrations(index, item, impact.DirectConsumers)
}

func (validator *businessChangePlanValidator) validateOrder(field string, order []string, items map[string]bool) {
	seen := map[string]bool{}
	for index, itemID := range order {
		path := fmt.Sprintf("%s[%d]", field, index)
		if !items[itemID] {
			validator.issue(itemID, path, "backend.change_plan.order_item_unknown", "item_id", itemID)
		} else if seen[itemID] {
			validator.issue(itemID, path, "backend.change_plan.order_item_duplicate", "item_id", itemID)
		}
		seen[itemID] = true
	}
	for itemID := range items {
		if !seen[itemID] {
			validator.issue(itemID, field, "backend.change_plan.order_item_missing", "item_id", itemID)
		}
	}
}
