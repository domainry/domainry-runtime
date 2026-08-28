package datamigration

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

func BuildPlan(source, target Inventory) Plan {
	plan := Plan{
		Source: source, Target: target, GeneratedAt: time.Now().UTC(),
		RequiresStopWriteCutover: true, RequiresIsolatedRollback: true,
		ForbidsWorkspaceFallback: true, HighCostIndexesAfterCopy: true, ConstraintsAfterCopy: true,
	}
	groups := map[string]*PreviewGroup{}
	for _, phase := range []string{"create", "alter", "backfill", "disable", "drop"} {
		group := PreviewGroup{Phase: phase, Operations: []string{}, Destructive: phase == "drop"}
		plan.PreviewGroups = append(plan.PreviewGroups, group)
	}
	for index := range plan.PreviewGroups {
		groups[plan.PreviewGroups[index].Phase] = &plan.PreviewGroups[index]
	}
	groups["disable"].Operations = append(groups["disable"].Operations, "stop source writes and drain workers before final delta")
	groups["drop"].Operations = append(groups["drop"].Operations, "retire source only after observation window and approved backup evidence")
	for _, table := range source.Tables {
		plan.EstimatedTargetBytes += max64(table.EstimatedBytes, estimateTableBytes(source, table)) * 13 / 10
		targetTable, found := findTable(target.Tables, table.Name)
		tablePlan := TablePlan{Name: table.Name, Rows: table.Rows, CheckpointKey: append([]string(nil), table.PrimaryKey...), MissingTarget: !found, WorkspaceScoped: table.WorkspaceScoped}
		groups["backfill"].Operations = append(groups["backfill"].Operations, "copy "+table.Name+" by checkpoint key")
		if len(table.PrimaryKey) == 1 {
			tablePlan.BatchCopyEligible = true
		} else if len(table.PrimaryKey) == 0 {
			plan.Blockers = append(plan.Blockers, fmt.Sprintf("table %s has no primary key for resumable copy", table.Name))
		} else {
			plan.Blockers = append(plan.Blockers, fmt.Sprintf("table %s has a composite primary key; explicit checkpoint ordering is required", table.Name))
		}
		if table.InvalidWorkspaceRows > 0 {
			plan.Blockers = append(plan.Blockers, fmt.Sprintf("table %s contains %d rows with missing workspace_id", table.Name, table.InvalidWorkspaceRows))
		}
		if !found {
			groups["create"].Operations = append(groups["create"].Operations, "create target table "+table.Name)
			plan.Blockers = append(plan.Blockers, fmt.Sprintf("target table %s is missing; provision target schema before copy", table.Name))
		}
		for _, column := range table.Columns {
			targetColumn, columnFound := findColumn(targetTable.Columns, column.Name)
			if !columnFound {
				groups["create"].Operations = append(groups["create"].Operations, "create target column "+table.Name+"."+column.Name)
				tablePlan.MissingColumns = append(tablePlan.MissingColumns, column.Name)
				if found {
					plan.Blockers = append(plan.Blockers, fmt.Sprintf("target column %s.%s is missing", table.Name, column.Name))
				}
			}
			conversion := conversionFor(column, targetColumn, columnFound)
			tablePlan.Conversions = append(tablePlan.Conversions, conversion)
			if conversion.RequiresReview {
				groups["alter"].Operations = append(groups["alter"].Operations, "review conversion "+table.Name+"."+column.Name+" from "+conversion.SourceType+" to "+conversion.TargetType)
				tablePlan.ConflictingTypes = append(tablePlan.ConflictingTypes, column.Name)
				plan.Warnings = append(plan.Warnings, fmt.Sprintf("review conversion %s.%s: %s to %s", table.Name, column.Name, conversion.SourceType, conversion.TargetType))
			}
		}
		if found {
			for _, index := range table.Indexes {
				if !containsIndex(targetTable.Indexes, index) {
					tablePlan.DeferredIndexes = append(tablePlan.DeferredIndexes, index)
					plan.Warnings = append(plan.Warnings, fmt.Sprintf("create target index %s after table %s copy", index.Name, table.Name))
				}
			}
			for _, foreign := range table.ForeignKeys {
				if !containsForeignKey(targetTable.ForeignKeys, foreign) {
					tablePlan.DeferredForeignKeys = append(tablePlan.DeferredForeignKeys, foreign)
					plan.Warnings = append(plan.Warnings, fmt.Sprintf("create target foreign key on %s(%s) after copy", table.Name, strings.Join(foreign.Columns, ",")))
				}
			}
		}
		plan.Tables = append(plan.Tables, tablePlan)
	}
	ordered, orderingBlockers := orderPlansByForeignKeys(source, plan.Tables)
	plan.Tables = ordered
	plan.Blockers = append(plan.Blockers, orderingBlockers...)
	for _, sequence := range source.Sequences {
		sequencePlan := SequencePlan{SourceName: sequence.Name, CurrentValue: sequence.CurrentValue, Strategy: "setval_after_copy"}
		if targetSequence, found := matchTargetSequence(sequence, target.Sequences); found {
			sequencePlan.TargetName = targetSequence.Name
		} else {
			sequencePlan.Strategy = "explicit_sequence_mapping_required"
			sequencePlan.Blocked = true
			plan.Blockers = append(plan.Blockers, fmt.Sprintf("source sequence %s has no target mapping", sequence.Name))
		}
		plan.Sequences = append(plan.Sequences, sequencePlan)
	}
	sort.Strings(plan.Blockers)
	sort.Strings(plan.Warnings)
	return plan
}

func containsIndex(indexes []IndexInventory, expected IndexInventory) bool {
	for _, index := range indexes {
		if index.Unique == expected.Unique && equalStrings(index.Columns, expected.Columns) {
			return true
		}
	}
	return false
}

func containsForeignKey(foreignKeys []ForeignInventory, expected ForeignInventory) bool {
	for _, foreign := range foreignKeys {
		if foreign.ReferencedTable == expected.ReferencedTable && equalStrings(foreign.Columns, expected.Columns) && equalStrings(foreign.ReferencedColumns, expected.ReferencedColumns) {
			return true
		}
	}
	return false
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func matchTargetSequence(source SequenceInventory, targets []SequenceInventory) (SequenceInventory, bool) {
	for _, target := range targets {
		if target.Name == source.Name || source.OwnedTable != "" && target.OwnedTable == source.OwnedTable && target.OwnedColumn == source.OwnedColumn {
			return target, true
		}
	}
	return SequenceInventory{}, false
}

func conversionFor(source, target ColumnInventory, targetFound bool) ConversionPlan {
	sourceType := normalizeType(source.Type)
	targetType := normalizeType(target.Type)
	if !targetFound {
		targetType = recommendedPostgresType(sourceType, source.Name)
	}
	conversion := ConversionPlan{Column: source.Name, SourceType: sourceType, TargetType: targetType, Strategy: "identity", Reversible: true}
	if sourceType == targetType || compatibleTypes(sourceType, targetType) {
		return conversion
	}
	switch {
	case isBooleanType(targetType) && isIntegerType(sourceType):
		conversion.Strategy = "sqlite_zero_one_to_boolean"
		conversion.RequiresReview = true
	case strings.Contains(targetType, "timestamp") && isTextType(sourceType):
		conversion.Strategy = "rfc3339_text_to_timestamptz"
		conversion.RequiresReview = true
	case targetType == "json" || targetType == "jsonb":
		conversion.Strategy = "validate_json_text"
		conversion.RequiresReview = true
	case targetType == "bytea" && strings.Contains(sourceType, "blob"):
		conversion.Strategy = "blob_to_bytea"
	default:
		conversion.Strategy = "explicit_cast_required"
		conversion.RequiresReview = true
		conversion.Reversible = false
	}
	return conversion
}

func recommendedPostgresType(sourceType, column string) string {
	switch {
	case strings.Contains(sourceType, "blob"):
		return "bytea"
	case isIntegerType(sourceType):
		return "bigint"
	case strings.Contains(sourceType, "real"), strings.Contains(sourceType, "float"), strings.Contains(sourceType, "double"):
		return "double precision"
	case strings.Contains(strings.ToLower(column), "_at"):
		return "text"
	default:
		return "text"
	}
}

func normalizeType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.Join(strings.Fields(value), " ")
	return value
}

func compatibleTypes(source, target string) bool {
	return isTextType(source) && isTextType(target) || isIntegerType(source) && isIntegerType(target)
}

func isTextType(value string) bool {
	return value == "text" || strings.Contains(value, "char") || value == "clob"
}

func isIntegerType(value string) bool {
	return strings.Contains(value, "int") || value == "serial" || value == "bigserial"
}

func isBooleanType(value string) bool { return value == "boolean" || value == "bool" }

func findTable(tables []TableInventory, name string) (TableInventory, bool) {
	for _, table := range tables {
		if table.Name == name {
			return table, true
		}
	}
	return TableInventory{}, false
}

func findColumn(columns []ColumnInventory, name string) (ColumnInventory, bool) {
	for _, column := range columns {
		if column.Name == name {
			return column, true
		}
	}
	return ColumnInventory{}, false
}

func estimateTableBytes(inventory Inventory, table TableInventory) int64 {
	if inventory.DatabaseBytes <= 0 || table.Rows <= 0 {
		return table.Rows * 512
	}
	var totalRows int64
	for _, candidate := range inventory.Tables {
		totalRows += candidate.Rows
	}
	if totalRows == 0 {
		return 0
	}
	return inventory.DatabaseBytes * table.Rows / totalRows
}

func max64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}
