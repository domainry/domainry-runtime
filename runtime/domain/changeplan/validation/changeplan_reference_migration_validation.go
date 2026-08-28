package validation

import changeplanmodel "github.com/domainry/domainry-runtime/runtime/domain/changeplan/model"

import (
	"fmt"
	"strings"
)

func (validator *businessChangePlanValidator) validateReferenceMigrations(itemIndex int, item changeplanmodel.BusinessSystemChangeItem, consumers []changeplanmodel.ReferenceEdge) {
	migrations := map[string]changeplanmodel.BusinessReferenceMigration{}
	for _, migration := range item.ReferenceMigrations {
		key := businessReferenceResourceType(migration.Consumer.ResourceType) + "\x00" + migration.Consumer.ResourceKey
		migrations[key] = migration
	}
	for consumerIndex, edge := range consumers {
		path := fmt.Sprintf("items[%d].reference_migrations[%d]", itemIndex, consumerIndex)
		migration, exists := migrations[edge.FromType+"\x00"+edge.FromKey]
		if !exists {
			validator.issue(item.ItemID, path, "backend.change_plan.reference_migration_required", "consumer_type", edge.FromType, "consumer_key", edge.FromKey)
			continue
		}
		if strings.TrimSpace(migration.Reason) == "" || migration.Replacement.ResourceType != item.Replacement.ResourceType || migration.Replacement.ResourceKey != item.Replacement.ResourceKey {
			validator.issue(item.ItemID, path, "backend.change_plan.reference_migration_invalid", "consumer_type", edge.FromType, "consumer_key", edge.FromKey)
			continue
		}
		switch migration.Strategy {
		case "update_reference":
			if !validator.migrationChangeItemPrecedes(migration.ChangeItemID, item.ItemID, edge) {
				validator.issue(item.ItemID, path+".change_item_id", "backend.change_plan.reference_migration_order_invalid", "change_item_id", migration.ChangeItemID, "consumer_type", edge.FromType, "consumer_key", edge.FromKey)
			}
		case "preserve_in_flight":
			if !runtimeEvidenceResourceType(edge.FromType) {
				validator.issue(item.ItemID, path+".strategy", "backend.change_plan.reference_migration_invalid", "consumer_type", edge.FromType, "consumer_key", edge.FromKey)
			}
		case "manual":
			if !validator.plan.Reviewed {
				validator.issue(item.ItemID, path+".strategy", "backend.change_plan.review_required")
			}
		default:
			validator.issue(item.ItemID, path+".strategy", "backend.change_plan.reference_migration_invalid", "actual", migration.Strategy)
		}
	}
}

func (validator *businessChangePlanValidator) migrationChangeItemPrecedes(changeItemID, deletionItemID string, edge changeplanmodel.ReferenceEdge) bool {
	changeItemID = strings.TrimSpace(changeItemID)
	if changeItemID == "" {
		return false
	}
	validItem := false
	for _, candidate := range validator.plan.Items {
		if candidate.ItemID == changeItemID && businessReferenceResourceType(candidate.ResourceType) == edge.FromType && candidate.ResourceKey == edge.FromKey && candidate.Operation == "update" {
			validItem = true
		}
	}
	if !validItem {
		return false
	}
	changeIndex, deletionIndex := -1, -1
	for index, itemID := range validator.plan.ReleaseOrder {
		if itemID == changeItemID {
			changeIndex = index
		}
		if itemID == deletionItemID {
			deletionIndex = index
		}
	}
	return changeIndex >= 0 && deletionIndex >= 0 && changeIndex < deletionIndex
}

func runtimeEvidenceResourceType(resourceType string) bool {
	switch resourceType {
	case "workflow_process", "scheduler_run", "scheduler_dead_letter", "outbox_message":
		return true
	default:
		return false
	}
}
