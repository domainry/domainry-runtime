package appschema

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"github.com/domainry/domainry-orm/query"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
)

const (
	metadataUpgradeCreateUniqueIndexOperation = "create_unique_index"
	metadataUpgradeRetainTableOperation       = "retain_table"
	metadataUpgradeRetainColumnOperation      = "retain_column"
)

// UpgradePlan evaluates the physical changes needed to move from the previous
// definition version (nil on a database that has no projection yet) to the
// next one. It never mutates the database: table shapes, row counts and unique
// duplicates are only read so the caller can decide whether to apply, verify or
// print the plan.
func (r ApplicationSchemaStore) UpgradePlan(ctx context.Context, scope principalmodel.SystemScope, previous *manifestmodel.ManifestSchema, next manifestmodel.ManifestSchema) (appschemamodel.ApplicationSchemaUpgradePlan, error) {
	plan := appschemamodel.ApplicationSchemaUpgradePlan{
		ContractVersion: appschemamodel.ApplicationSchemaUpgradePlanContractVersion,
		ToVersion:       strings.TrimSpace(next.Version),
		Steps:           []appschemamodel.ApplicationSchemaUpgradeStep{},
		Diagnostics:     []appschemamodel.ApplicationSchemaUpgradeDiagnostic{},
	}
	if previous != nil {
		plan.FromVersion = strings.TrimSpace(previous.Version)
	}
	if err := requireMetadataInstallationScope(scope); err != nil {
		return plan, err
	}
	if err := ctx.Err(); err != nil {
		return plan, err
	}
	for _, object := range next.Objects {
		table := strings.TrimSpace(object.Key)
		if table == "" {
			continue
		}
		existingTypes, err := r.tableColumnTypes(ctx, table)
		if err != nil {
			if contextErr := ctx.Err(); contextErr != nil {
				return plan, contextErr
			}
			plan.Steps = append(plan.Steps, compatibleUpgradeStep(metadataCreateTableStep(object, table)))
			continue
		}
		var rowCount int64
		rowCounted := false
		countRows := func() (int64, error) {
			if rowCounted {
				return rowCount, nil
			}
			count, countErr := r.tableRowCount(ctx, table)
			if countErr != nil {
				return 0, countErr
			}
			rowCount, rowCounted = count, true
			return rowCount, nil
		}
		for _, column := range r.planObjectColumns(object, existingTypes) {
			if column.mismatch {
				mismatch := metadataPhysicalSchemaMismatch(object.Key, column.column, column.targetType, column.currentType).(*appschemamodel.ApplicationSchemaPhysicalSchemaMismatchError)
				plan.Steps = append(plan.Steps, appschemamodel.ApplicationSchemaUpgradeStep{
					ApplicationSchemaMigrationStep: appschemamodel.ApplicationSchemaMigrationStep{ObjectKey: object.Key, Table: table, Operation: "alter_column", ColumnKey: column.column, ColumnType: column.targetType, Description: "backend.metadata.migration.physicalTypeMismatch"},
					Classification:                 appschemamodel.ApplicationSchemaUpgradeIncompatible,
					Blocking:                       true,
					ErrorCode:                      mismatch.ErrorCode(),
					Params:                         mismatch.ErrorParams(),
				})
				continue
			}
			step := compatibleUpgradeStep(column.migrationStep(object, table))
			if column.operation == metadataColumnAddOperation && column.field.Required && column.field.FieldBackfillValue() == nil && !column.field.FieldExemptsExistingRows() {
				rows, countErr := countRows()
				if countErr != nil {
					return plan, countErr
				}
				if rows > 0 {
					step.Classification = appschemamodel.ApplicationSchemaUpgradeRequiresRule
					step.Blocking = true
					step.ErrorCode = appschemamodel.ApplicationSchemaUpgradeRequiredFieldRuleMissingCode
					step.ExistingRows = rows
					step.Params = map[string]string{"object": object.Key, "field": column.column, "existing_rows": strconv.FormatInt(rows, 10)}
				}
			}
			plan.Steps = append(plan.Steps, step)
		}
		uniqueSteps, err := r.planUniqueIndexes(ctx, object, table, existingTypes)
		if err != nil {
			return plan, err
		}
		plan.Steps = append(plan.Steps, uniqueSteps...)
	}
	if previous != nil {
		plan.Steps = append(plan.Steps, retainedUpgradeSteps(*previous, next)...)
	}
	for _, step := range plan.Steps {
		if step.Blocking {
			plan.Blocking = true
			break
		}
	}
	return plan, nil
}

func compatibleUpgradeStep(step appschemamodel.ApplicationSchemaMigrationStep) appschemamodel.ApplicationSchemaUpgradeStep {
	return appschemamodel.ApplicationSchemaUpgradeStep{ApplicationSchemaMigrationStep: step, Classification: appschemamodel.ApplicationSchemaUpgradeCompatible}
}

// planUniqueIndexes reports unique indexes the next definition adds on columns
// that already exist. Duplicates in the stored data would make the index
// creation fail, so the plan probes them up front.
func (r ApplicationSchemaStore) planUniqueIndexes(ctx context.Context, object definitionmodel.ObjectSchema, table string, existingTypes map[string]string) ([]appschemamodel.ApplicationSchemaUpgradeStep, error) {
	indexes, err := r.tableIndexes(ctx, table)
	if err != nil {
		return nil, err
	}
	steps := []appschemamodel.ApplicationSchemaUpgradeStep{}
	probe := func(indexName string, columnKey string, fields []string) error {
		if indexes[indexName] {
			return nil
		}
		for _, field := range fields {
			if _, exists := existingTypes[field]; !exists {
				// A column that is still being added holds no data, so its unique
				// index cannot conflict; the add_column step already covers it.
				return nil
			}
		}
		duplicates, probeErr := r.uniqueDuplicateGroups(ctx, table, append([]string{"workspace_id"}, fields...))
		if probeErr != nil {
			return probeErr
		}
		step := appschemamodel.ApplicationSchemaUpgradeStep{
			ApplicationSchemaMigrationStep: appschemamodel.ApplicationSchemaMigrationStep{ObjectKey: object.Key, Table: table, Operation: metadataUpgradeCreateUniqueIndexOperation, ColumnKey: columnKey, Reversible: true, Description: "backend.metadata.migration.uniqueIndex"},
			Classification:                 appschemamodel.ApplicationSchemaUpgradeDataDependent,
			Params:                         map[string]string{"object": object.Key, "fields": strings.Join(fields, ","), "duplicates": strconv.FormatInt(duplicates, 10)},
		}
		if duplicates > 0 {
			step.Blocking = true
			step.ErrorCode = appschemamodel.ApplicationSchemaUpgradeUniqueConflictCode
		}
		steps = append(steps, step)
		return nil
	}
	for _, field := range object.Fields {
		fieldKey := strings.TrimSpace(field.Key)
		if fieldKey == "" || strings.TrimSpace(field.DisabledAt) != "" || !field.Unique {
			continue
		}
		if err := probe(r.metadataFieldIndexName(table, fieldKey, true), fieldKey, []string{fieldKey}); err != nil {
			return nil, err
		}
	}
	for _, validation := range object.Validations {
		if strings.TrimSpace(validation.Type) != "composite_unique" || len(validation.Fields) == 0 {
			continue
		}
		fields := []string{}
		for _, field := range validation.Fields {
			if field = strings.TrimSpace(field); field != "" {
				fields = append(fields, field)
			}
		}
		if len(fields) == 0 {
			continue
		}
		if err := probe(r.uniqueIndexName(table, validation.Fields), strings.Join(fields, ","), fields); err != nil {
			return nil, err
		}
	}
	return steps, nil
}

func retainedUpgradeSteps(previous, next manifestmodel.ManifestSchema) []appschemamodel.ApplicationSchemaUpgradeStep {
	nextObjects := make(map[string]definitionmodel.ObjectSchema, len(next.Objects))
	for _, object := range next.Objects {
		if key := strings.TrimSpace(object.Key); key != "" {
			nextObjects[key] = object
		}
	}
	steps := []appschemamodel.ApplicationSchemaUpgradeStep{}
	retained := func(step appschemamodel.ApplicationSchemaMigrationStep) {
		step.Reversible = true
		step.Description = appschemamodel.ApplicationSchemaRetainedMigrationDescription
		steps = append(steps, appschemamodel.ApplicationSchemaUpgradeStep{ApplicationSchemaMigrationStep: step, Classification: appschemamodel.ApplicationSchemaUpgradeRetained})
	}
	for _, object := range previous.Objects {
		table := strings.TrimSpace(object.Key)
		if table == "" {
			continue
		}
		nextObject, exists := nextObjects[table]
		if !exists {
			retained(appschemamodel.ApplicationSchemaMigrationStep{ObjectKey: object.Key, Table: table, Operation: metadataUpgradeRetainTableOperation})
			continue
		}
		nextFields := make(map[string]definitionmodel.FieldSchema, len(nextObject.Fields))
		for _, field := range nextObject.Fields {
			if key := strings.TrimSpace(field.Key); key != "" {
				nextFields[key] = field
			}
		}
		for _, field := range object.Fields {
			column := strings.TrimSpace(field.Key)
			if column == "" || strings.TrimSpace(field.DisabledAt) != "" {
				continue
			}
			if nextField, exists := nextFields[column]; exists && strings.TrimSpace(nextField.DisabledAt) == "" {
				continue
			}
			retained(appschemamodel.ApplicationSchemaMigrationStep{ObjectKey: object.Key, Table: table, Operation: metadataUpgradeRetainColumnOperation, ColumnKey: column})
		}
	}
	return steps
}

func (r ApplicationSchemaStore) tableRowCount(ctx context.Context, table string) (int64, error) {
	statement, args, err := query.NewSelectBuilder(r.store.SQLRenderer, table).Projections(query.Project(query.CountAll())).Build()
	if err != nil {
		return 0, fmt.Errorf("build row count for %s: %w", table, err)
	}
	var count int64
	if err := r.database().QueryRowContext(ctx, statement, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("count rows of %s: %w", table, err)
	}
	return count, nil
}

// uniqueDuplicateGroups counts the value tuples that occur more than once.
// NULL members are excluded because every supported engine treats NULL as
// distinct inside a unique index.
func (r ApplicationSchemaStore) uniqueDuplicateGroups(ctx context.Context, table string, columns []string) (int64, error) {
	notNull := make([]query.Predicate, 0, len(columns))
	groups := make([]query.Expression, 0, len(columns))
	for _, column := range columns {
		notNull = append(notNull, query.IsNotNull(column))
		groups = append(groups, query.Column(column))
	}
	duplicates := query.NewSelectBuilder(r.store.SQLRenderer, table).Columns(columns...).Where(query.And(notNull...)).GroupBy(groups...).Having(query.GreaterThanExpression(query.CountAll(), 1))
	statement, args, err := query.NewSelectFromSubquery(r.store.SQLRenderer, duplicates, "duplicate_groups").Projections(query.Project(query.CountAll())).Build()
	if err != nil {
		return 0, fmt.Errorf("build unique duplicate probe for %s: %w", table, err)
	}
	var count int64
	if err := r.database().QueryRowContext(ctx, statement, args...).Scan(&count); err != nil {
		return 0, fmt.Errorf("probe unique duplicates of %s: %w", table, err)
	}
	return count, nil
}
