package appschema

import (
	"context"
	"fmt"
	"strings"

	"github.com/domainry/domainry-orm/query"
	recordschema "github.com/domainry/domainry-orm/recordschema"
	ormschema "github.com/domainry/domainry-orm/schema"

	appschemamodel "github.com/domainry/domainry-runtime/runtime/domain/appschema/model"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordmodel "github.com/domainry/domainry-runtime/runtime/domain/record/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
	appschemastorage "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/appschema/storage"
)

// metadataColumnPlan is the shared column inspection used by MigrationPlan
// and UpgradePlan: it compares one declared field against the physical
// column types of an existing table.
type metadataColumnPlan struct {
	field       definitionmodel.FieldSchema
	column      string
	operation   string
	currentType string
	targetType  string
	mismatch    bool
}

const (
	metadataColumnAddOperation          = "add_column"
	metadataColumnExactDecimalOperation = "alter_column_exact_decimal"
)

func (r ApplicationSchemaStore) planObjectColumns(object definitionmodel.ObjectSchema, existingTypes map[string]string) []metadataColumnPlan {
	plans := []metadataColumnPlan{}
	for _, field := range object.Fields {
		column := strings.TrimSpace(field.Key)
		if column == "" || strings.TrimSpace(field.DisabledAt) != "" {
			continue
		}
		targetType := r.metadataSQLTypeForField(field)
		currentType, exists := existingTypes[column]
		if !exists {
			plans = append(plans, metadataColumnPlan{field: field, column: column, operation: metadataColumnAddOperation, targetType: targetType})
			continue
		}
		if !metadataRequiresExactPhysicalType(field) || metadataColumnTypeMatches(currentType, targetType) {
			continue
		}
		if r.storage.ExactDecimalUpgradeAllowed(currentType, field) {
			plans = append(plans, metadataColumnPlan{field: field, column: column, operation: metadataColumnExactDecimalOperation, currentType: currentType, targetType: targetType})
			continue
		}
		plans = append(plans, metadataColumnPlan{field: field, column: column, currentType: currentType, targetType: targetType, mismatch: true})
	}
	return plans
}

func (plan metadataColumnPlan) migrationStep(object definitionmodel.ObjectSchema, table string) appschemamodel.ApplicationSchemaMigrationStep {
	switch plan.operation {
	case metadataColumnExactDecimalOperation:
		return appschemamodel.ApplicationSchemaMigrationStep{ObjectKey: object.Key, Table: table, Operation: metadataColumnExactDecimalOperation, ColumnKey: plan.column, ColumnType: plan.targetType, Reversible: false, Description: "backend.metadata.migration.exactDecimal"}
	default:
		return appschemamodel.ApplicationSchemaMigrationStep{ObjectKey: object.Key, Table: table, Operation: metadataColumnAddOperation, ColumnKey: plan.column, ColumnType: plan.targetType, Description: "backend.metadata.migration.addColumn"}
	}
}

func metadataCreateTableStep(object definitionmodel.ObjectSchema, table string) appschemamodel.ApplicationSchemaMigrationStep {
	return appschemamodel.ApplicationSchemaMigrationStep{ObjectKey: object.Key, Table: table, Operation: "create_table", Description: "backend.metadata.migration.createTable"}
}

func (r ApplicationSchemaStore) MigrationPlan(ctx context.Context, scope principalmodel.SystemScope, manifest manifestmodel.ManifestSchema) ([]appschemamodel.ApplicationSchemaMigrationStep, error) {
	if err := requireMetadataInstallationScope(scope); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	steps := []appschemamodel.ApplicationSchemaMigrationStep{}
	for _, object := range manifest.Objects {
		table := strings.TrimSpace(object.Key)
		if table == "" {
			continue
		}
		existingTypes, err := r.tableColumnTypes(ctx, table)
		if err != nil {
			if contextErr := ctx.Err(); contextErr != nil {
				return nil, contextErr
			}
			steps = append(steps, metadataCreateTableStep(object, table))
			continue
		}
		for _, plan := range r.planObjectColumns(object, existingTypes) {
			if plan.mismatch {
				return nil, metadataPhysicalSchemaMismatch(object.Key, plan.column, plan.targetType, plan.currentType)
			}
			steps = append(steps, plan.migrationStep(object, table))
		}
	}
	return steps, nil
}

// SyncManifest materializes the manifest's objects. Receipts are keyed by the
// manifest version; callers that know the previous version and backup use
// ApplyUpgrade instead.
func (r ApplicationSchemaStore) SyncManifest(ctx context.Context, scope principalmodel.SystemScope, manifest manifestmodel.ManifestSchema) error {
	if err := requireMetadataInstallationScope(scope); err != nil {
		return err
	}
	return r.syncManifestForUpgrade(ctx, manifest, metadataUpgradeExecution{toVersion: strings.TrimSpace(manifest.Version)})
}

func (r ApplicationSchemaStore) loadPhysicalSchemaSnapshot(ctx context.Context, objects []definitionmodel.ObjectSchema) (*appschemastorage.PhysicalSchemaSnapshot, error) {
	inspector, ok := r.storage.(appschemastorage.BulkPhysicalSchemaInspector)
	if !ok {
		return nil, nil
	}
	tables := make([]string, 0, len(objects))
	seen := map[string]bool{}
	for _, object := range objects {
		table := strings.TrimSpace(object.Key)
		if table != "" && !seen[table] {
			tables = append(tables, table)
			seen[table] = true
		}
	}
	snapshot, err := inspector.PhysicalSchema(ctx, r.database(), r.store.SQLRenderer, r.store.DatabaseSchema(), tables)
	if err != nil {
		return nil, err
	}
	return &snapshot, nil
}

func (r ApplicationSchemaStore) syncManifestForUpgrade(ctx context.Context, manifest manifestmodel.ManifestSchema, execution metadataUpgradeExecution) error {
	hasObjects := false
	for _, object := range manifest.Objects {
		if strings.TrimSpace(object.Key) != "" {
			hasObjects = true
			break
		}
	}
	if !hasObjects {
		return nil
	}
	if execution.receiptStatuses == nil {
		statuses, err := r.loadUpgradeReceiptStatuses(ctx, execution.toVersion)
		if err != nil {
			return err
		}
		execution.receiptStatuses = statuses
	}
	physicalSchema, err := r.loadPhysicalSchemaSnapshot(ctx, manifest.Objects)
	if err != nil {
		return err
	}
	if err := r.migrateExactDecimalStorage(ctx, manifest, physicalSchema); err != nil {
		return err
	}
	for _, object := range manifest.Objects {
		if strings.TrimSpace(object.Key) == "" {
			continue
		}
		var existingTypes map[string]string
		var indexes map[string]bool
		if physicalSchema != nil {
			existingTypes = physicalSchema.ColumnsByTable[strings.TrimSpace(object.Key)]
			if len(existingTypes) > 0 {
				indexes = physicalSchema.IndexesByTable[strings.TrimSpace(object.Key)]
			}
		}
		if err := r.ensureObjectStorageWithPhysicalSchema(ctx, object, execution, existingTypes, indexes); err != nil {
			return err
		}
	}
	return nil
}

func (r ApplicationSchemaStore) ensureObjectStorage(ctx context.Context, object definitionmodel.ObjectSchema, execution metadataUpgradeExecution) error {
	return r.ensureObjectStorageWithPhysicalSchema(ctx, object, execution, nil, nil)
}

func (r ApplicationSchemaStore) ensureObjectStorageWithPhysicalSchema(ctx context.Context, object definitionmodel.ObjectSchema, execution metadataUpgradeExecution, existingTypes map[string]string, indexes map[string]bool) error {
	schemaDB := r.schemaDatabase()
	constraintIndexed := metadataConstraintIndexedFields(object)
	// Core identity/time columns may be declared by metadata so callers can
	// query and sort them; they still resolve to the canonical physical columns.
	// The remaining Record metadata columns are infrastructure-owned and cannot
	// be redefined as business data.
	reserved := map[string]bool{
		"deleted": true, "ext_info": true, "create_by": true, "update_by": true,
		"owner_user_id": true, "owner_org_id": true,
	}
	for _, field := range object.Fields {
		if reserved[strings.TrimSpace(field.Key)] {
			return fmt.Errorf("object %s field %s conflicts with a Record system column", object.Key, field.Key)
		}
	}
	// Record identity, timestamps, deletion metadata, extension metadata and
	// actor metadata are owned by domainry-orm. Runtime only adds business
	// fields below; redeclaring system columns here would override the ORM's
	// canonical cross-dialect types and defaults.
	if len(existingTypes) == 0 {
		createStatement, createArgs, buildErr := recordschema.NewTable(r.store.SQLRenderer, object.Key).IfNotExists().Build()
		if buildErr != nil {
			return fmt.Errorf("build object table %s: %w", object.Key, buildErr)
		}
		if _, err := schemaDB.ExecContext(ctx, createStatement, createArgs...); err != nil {
			return fmt.Errorf("ensure object table %s: %w", object.Key, err)
		}
	}
	existing := map[string]bool{}
	if len(existingTypes) > 0 {
		for column := range existingTypes {
			existing[column] = true
		}
	} else {
		var err error
		existing, err = r.tableColumns(ctx, object.Key)
		if err != nil {
			return err
		}
	}
	if err := r.migrateLegacyRecordActorColumns(ctx, object.Key, existing); err != nil {
		return err
	}
	if indexes == nil {
		var err error
		indexes, err = r.tableIndexes(ctx, object.Key)
		if err != nil {
			return err
		}
	}
	ensureIndex := func(name string, unique bool, columns ...string) error {
		if indexes[name] {
			return nil
		}
		if err := r.createIndexKnownMissing(ctx, object.Key, name, unique, columns...); err != nil {
			return err
		}
		indexes[name] = true
		return nil
	}
	if !existing["workspace_id"] {
		if _, err := schemaDB.ExecContext(ctx, "ALTER TABLE "+r.store.TableIdentifier(object.Key)+" ADD COLUMN "+r.store.Identifier("workspace_id")+" "+r.metadataIDColumnType()); err != nil {
			return fmt.Errorf("add column %s.workspace_id: %w", object.Key, err)
		}
		if _, err := schemaDB.ExecContext(ctx, "UPDATE "+r.store.TableIdentifier(object.Key)+" SET "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(1)+" WHERE "+r.store.Identifier("workspace_id")+" IS NULL OR "+r.store.Identifier("workspace_id")+" = ''", principalmodel.InstallationWorkspaceID); err != nil {
			return fmt.Errorf("backfill column %s.workspace_id: %w", object.Key, err)
		}
		existing["workspace_id"] = true
	}
	if !existing["id"] {
		return fmt.Errorf("object %s is missing required Record system column id", object.Key)
	}
	for _, columnName := range []string{"deleted", "ext_info", "create_by", "update_by", "owner_user_id", "owner_org_id"} {
		if existing[columnName] {
			continue
		}
		column, ok := recordschema.SystemColumn(columnName)
		if !ok {
			return fmt.Errorf("record system column %s is unavailable", columnName)
		}
		statement, args, buildErr := ormschema.NewAddColumn(r.store.SQLRenderer, object.Key, column).Build()
		if buildErr != nil {
			return fmt.Errorf("build record system column %s.%s: %w", object.Key, columnName, buildErr)
		}
		if _, err := schemaDB.ExecContext(ctx, statement, args...); err != nil {
			return fmt.Errorf("add record system column %s.%s: %w", object.Key, columnName, err)
		}
		existing[columnName] = true
	}
	if batchAdder, ok := r.storage.(appschemastorage.BatchColumnAdder); ok {
		additions := []appschemastorage.ColumnDefinition{}
		pending := map[string]bool{}
		for _, field := range object.Fields {
			field = metadataConstraintIndexedField(field, constraintIndexed[field.Key])
			fieldKey := strings.TrimSpace(field.Key)
			if fieldKey == "" || strings.TrimSpace(field.DisabledAt) != "" || existing[fieldKey] || pending[fieldKey] {
				continue
			}
			additions = append(additions, appschemastorage.ColumnDefinition{Name: fieldKey, Type: r.metadataSQLTypeForField(field)})
			pending[fieldKey] = true
		}
		if len(additions) > 0 {
			if _, err := schemaDB.ExecContext(ctx, batchAdder.BatchAddColumnsSQL(r.store.SQLRenderer, object.Key, additions)); err != nil {
				return fmt.Errorf("add object columns for %s: %w", object.Key, err)
			}
			for _, addition := range additions {
				existing[addition.Name] = true
				if existingTypes == nil {
					existingTypes = map[string]string{}
				}
				existingTypes[addition.Name] = addition.Type
				if err := r.writeUpgradeReceipt(ctx, execution, object.Key, addition.Name, metadataUpgradeStepKey(metadataColumnAddOperation, object.Key, addition.Name, execution.toVersion), metadataUpgradeReceiptCompleted, ""); err != nil {
					return err
				}
			}
		}
	}
	if err := ensureIndex(r.metadataFieldIndexName(object.Key, "workspace_id_id", true), true, "workspace_id", "id"); err != nil {
		return fmt.Errorf("create workspace record identity index for %s: %w", object.Key, err)
	}
	if err := ensureIndex(r.metadataFieldIndexName(object.Key, "owner_user_id", false), false, "workspace_id", "owner_user_id"); err != nil {
		return fmt.Errorf("create owner user index for %s: %w", object.Key, err)
	}
	if err := ensureIndex(r.metadataFieldIndexName(object.Key, "owner_org_id", false), false, "workspace_id", "owner_org_id"); err != nil {
		return fmt.Errorf("create owner organization index for %s: %w", object.Key, err)
	}
	for _, field := range object.Fields {
		field = metadataConstraintIndexedField(field, constraintIndexed[field.Key])
		fieldKey := strings.TrimSpace(field.Key)
		if fieldKey == "" || strings.TrimSpace(field.DisabledAt) != "" {
			continue
		}
		if existing[fieldKey] && metadataRequiresExactPhysicalType(field) {
			if existingTypes == nil {
				var typeErr error
				existingTypes, typeErr = r.tableColumnTypes(ctx, object.Key)
				if typeErr != nil {
					return typeErr
				}
			}
			currentType := existingTypes[fieldKey]
			targetType := r.metadataSQLTypeForField(field)
			if !metadataColumnTypeMatches(currentType, targetType) {
				return metadataPhysicalSchemaMismatch(object.Key, fieldKey, targetType, currentType)
			}
		}
		if !existing[fieldKey] {
			// Raw DDL is justified here: business column types come from the
			// dialect storage profile (NUMERIC vs DECIMAL on PostgreSQL, bounded
			// VARCHAR for indexed MySQL text), which ormschema.NewAddColumn cannot
			// render byte-identically; see TestMetadataAddColumnTypeDivergesFromORM.
			// The column stays nullable so rows that predate it remain readable
			// until the receipted backfill below fills them.
			addColumn := "ALTER TABLE " + r.store.TableIdentifier(object.Key) + " ADD COLUMN " + r.store.Identifier(fieldKey) + " " + r.metadataSQLTypeForField(field)
			if _, err := schemaDB.ExecContext(ctx, addColumn); err != nil {
				return fmt.Errorf("add column %s.%s: %w", object.Key, fieldKey, err)
			}
			existing[fieldKey] = true
			if err := r.writeUpgradeReceipt(ctx, execution, object.Key, fieldKey, metadataUpgradeStepKey(metadataColumnAddOperation, object.Key, fieldKey, execution.toVersion), metadataUpgradeReceiptCompleted, ""); err != nil {
				return err
			}
		}
		if backfillValue := field.FieldBackfillValue(); backfillValue != nil {
			// The backfill is receipt-driven rather than tied to the ADD COLUMN
			// above: a crash between the two statements leaves no completed
			// backfill receipt, so the next start repeats the idempotent UPDATE.
			stepKey := metadataUpgradeStepKey("backfill", object.Key, fieldKey, execution.toVersion)
			if err := r.runReceiptedUpgradeStep(ctx, execution, object.Key, fieldKey, stepKey, func() error {
				backfill, args, buildErr := query.NewUpdateBuilder(r.store.SQLRenderer, object.Key).Set(fieldKey, r.metadataDBFieldValue(field, backfillValue)).Where(query.IsNull(fieldKey)).Build()
				if buildErr != nil {
					return fmt.Errorf("build backfill for %s.%s: %w", object.Key, fieldKey, buildErr)
				}
				if _, err := schemaDB.ExecContext(ctx, backfill, args...); err != nil {
					return fmt.Errorf("backfill column %s.%s: %w", object.Key, fieldKey, err)
				}
				return nil
			}); err != nil {
				return err
			}
		}
		indexName := r.metadataFieldIndexName(object.Key, fieldKey, false)
		uniqueIndexName := r.metadataFieldIndexName(object.Key, fieldKey, true)
		if field.Unique {
			if indexes[indexName] {
				if err := r.dropManagedIndex(ctx, object.Key, indexName); err != nil {
					return err
				}
				delete(indexes, indexName)
			}
			if !indexes[uniqueIndexName] {
				if err := ensureIndex(uniqueIndexName, true, "workspace_id", fieldKey); err != nil {
					return fmt.Errorf("create unique field index %s: %w", uniqueIndexName, err)
				}
				indexes[uniqueIndexName] = true
			}
		} else if metadataFieldIndexed(field) {
			if indexes[uniqueIndexName] {
				if err := r.dropManagedIndex(ctx, object.Key, uniqueIndexName); err != nil {
					return err
				}
				delete(indexes, uniqueIndexName)
			}
			if !indexes[indexName] {
				if err := ensureIndex(indexName, false, fieldKey); err != nil {
					return fmt.Errorf("create field index %s: %w", indexName, err)
				}
				indexes[indexName] = true
			}
		} else {
			for _, managed := range []string{indexName, uniqueIndexName} {
				if indexes[managed] {
					if err := r.dropManagedIndex(ctx, object.Key, managed); err != nil {
						return err
					}
					delete(indexes, managed)
				}
			}
		}
	}
	desiredPublicIndexes := make(map[string]bool, len(object.PublicResources))
	for _, resource := range object.PublicResources {
		indexName := r.publicResourceAccessIndexName(object.Key, resource)
		desiredPublicIndexes[indexName] = true
		if indexes[indexName] {
			continue
		}
		if err := ensureIndex(indexName, true, strings.TrimSpace(resource.AccessKeyField)); err != nil {
			return fmt.Errorf("create public resource access index %s: %w", indexName, err)
		}
		indexes[indexName] = true
	}
	for existingIndex := range indexes {
		if !strings.HasPrefix(existingIndex, "uidx_public_") || desiredPublicIndexes[existingIndex] {
			continue
		}
		if err := r.dropManagedIndex(ctx, object.Key, existingIndex); err != nil {
			return err
		}
		delete(indexes, existingIndex)
	}
	for _, validation := range object.Validations {
		kind := strings.TrimSpace(validation.Type)
		if kind != "composite_unique" || len(validation.Fields) == 0 {
			continue
		}
		indexName := r.uniqueIndexName(object.Key, validation.Fields)
		if indexes[indexName] {
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
		fields = append([]string{"workspace_id"}, fields...)
		if err := ensureIndex(indexName, true, fields...); err != nil {
			return fmt.Errorf("create unique index %s: %w", indexName, err)
		}
	}
	conditionalPolicies, err := recordvalidation.RecordConditionalUniquePolicies(object)
	if err != nil {
		return err
	}
	desiredConditionalIndexes := make(map[string]bool, len(conditionalPolicies))
	for _, policy := range conditionalPolicies {
		desiredConditionalIndexes[r.conditionalUniqueIndexName(object.Key, policy)] = true
	}
	for existingIndex := range indexes {
		if !strings.HasPrefix(existingIndex, "uidx_conditional_") || desiredConditionalIndexes[existingIndex] {
			continue
		}
		if err := r.dropManagedIndex(ctx, object.Key, existingIndex); err != nil {
			return err
		}
		if guard := r.storage.ConditionalUniqueGuard(existingIndex); guard != "" {
			columns, columnErr := r.tableColumns(ctx, object.Key)
			if columnErr != nil {
				return columnErr
			}
			if columns[guard] {
				if columnErr := r.storage.DropColumn(ctx, schemaDB, r.store.SQLRenderer, object.Key, guard); columnErr != nil {
					return fmt.Errorf("drop stale conditional unique guard %s: %w", guard, columnErr)
				}
			}
		}
		delete(indexes, existingIndex)
	}
	for _, policy := range conditionalPolicies {
		indexName := r.conditionalUniqueIndexName(object.Key, policy)
		if indexes[indexName] {
			continue
		}
		if err := r.createConditionalUniqueIndexWithSnapshot(ctx, object.Key, indexName, policy, indexes); err != nil {
			return fmt.Errorf("create conditional unique index %s: %w", indexName, err)
		}
		indexes[indexName] = true
	}
	temporalPolicies, err := recordvalidation.RecordTemporalExclusionPolicies(object)
	if err != nil {
		return err
	}
	for _, policy := range temporalPolicies {
		fields := append([]string{"workspace_id"}, policy.ScopeFields...)
		fields = append(fields, policy.StartField, policy.EndField)
		indexName := r.temporalExclusionIndexName(object.Key, policy.Key, fields)
		if indexes[indexName] {
			continue
		}
		if err := ensureIndex(indexName, false, fields...); err != nil {
			return fmt.Errorf("create temporal exclusion index %s: %w", indexName, err)
		}
	}
	aggregatePolicies, err := recordvalidation.RecordRelatedAggregateInvariants(object)
	if err != nil {
		return err
	}
	for _, policy := range aggregatePolicies {
		fields := []string{"workspace_id", policy.RelationField}
		if policy.StatusField != "" {
			fields = append(fields, policy.StatusField)
		}
		indexName := r.relatedAggregateIndexName(object.Key, policy.Key, fields)
		if indexes[indexName] {
			continue
		}
		if err := ensureIndex(indexName, false, fields...); err != nil {
			return fmt.Errorf("create related aggregate index %s: %w", indexName, err)
		}
	}
	return nil
}

func metadataConstraintIndexedFields(object definitionmodel.ObjectSchema) map[string]bool {
	result := map[string]bool{}
	for _, validation := range object.Validations {
		if strings.TrimSpace(validation.Type) == "composite_unique" {
			for _, field := range validation.Fields {
				result[strings.TrimSpace(field)] = true
			}
		}
		if strings.TrimSpace(validation.Type) == recordvalidation.ConditionalUniqueValidationType {
			for _, field := range validation.Fields {
				result[strings.TrimSpace(field)] = true
			}
			result[recordvalidation.RecordConfigString(validation.Config["condition_field"])] = true
		}
		if strings.TrimSpace(validation.Type) == "temporal_exclusion" {
			for _, key := range []string{"start_field", "end_field"} {
				result[recordvalidation.RecordConfigString(validation.Config[key])] = true
			}
			for _, field := range recordStringValues(validation.Config["scope_fields"]) {
				result[field] = true
			}
		}
		if strings.TrimSpace(validation.Type) == "related_aggregate_invariant" {
			result[recordvalidation.RecordConfigString(validation.Config["relation_field"])] = true
			result[recordvalidation.RecordConfigString(validation.Config["status_field"])] = true
		}
	}
	delete(result, "")
	return result
}

func (r ApplicationSchemaStore) createConditionalUniqueIndex(ctx context.Context, table, indexName string, policy recordvalidation.RecordConditionalUniquePolicy) error {
	return r.createConditionalUniqueIndexWithSnapshot(ctx, table, indexName, policy, nil)
}

func (r ApplicationSchemaStore) createConditionalUniqueIndexWithSnapshot(ctx context.Context, table, indexName string, policy recordvalidation.RecordConditionalUniquePolicy, indexes map[string]bool) error {
	plan := r.storage.ConditionalUniquePlan(r.store.SQLRenderer, table, indexName, policy)
	if plan.GuardColumn != "" {
		columns, err := r.tableColumns(ctx, table)
		if err != nil {
			return err
		}
		if !columns[plan.GuardColumn] {
			if _, err := r.schemaDatabase().ExecContext(ctx, plan.AddGuardStatement); err != nil {
				return fmt.Errorf("add conditional unique guard %s: %w", plan.GuardColumn, err)
			}
		}
		if indexes == nil {
			return r.createIndexIfMissing(ctx, table, indexName, true, plan.IndexFields...)
		}
		return r.createIndexKnownMissing(ctx, table, indexName, true, plan.IndexFields...)
	}
	if _, err := r.schemaDatabase().ExecContext(ctx, plan.PartialStatement); err != nil {
		return fmt.Errorf("create partial unique index: %w", err)
	}
	return nil
}

func metadataConstraintIndexedField(field definitionmodel.FieldSchema, indexed bool) definitionmodel.FieldSchema {
	if !indexed {
		return field
	}
	config := make(map[string]any, len(field.Config)+1)
	for key, value := range field.Config {
		config[key] = value
	}
	config["indexed"] = true
	field.Config = config
	return field
}

func recordStringValues(value any) []string {
	result := []string{}
	switch values := value.(type) {
	case []string:
		for _, value := range values {
			if value = strings.TrimSpace(value); value != "" {
				result = append(result, value)
			}
		}
	case []any:
		for _, value := range values {
			if text := strings.TrimSpace(fmt.Sprint(value)); text != "" && text != "<nil>" {
				result = append(result, text)
			}
		}
	}
	return result
}

func metadataDBValue(value any) any {
	switch typed := value.(type) {
	case float32:
		return float64(typed)
	case int:
		return float64(typed)
	case int64:
		return float64(typed)
	default:
		return value
	}
}

func (r ApplicationSchemaStore) metadataDBFieldValue(field definitionmodel.FieldSchema, value any) any {
	if recordmodel.RecordIsStructuredFieldType(field.Type) {
		if encoded, err := recordmodel.RecordEncodeStructuredFieldValue(field, value); err == nil {
			return encoded
		}
		return value
	}
	if field.Type == "currency" || field.Type == "percent" {
		config, err := recordmodel.RecordNormalizeDecimalConfig(field.Config)
		if err != nil {
			return value
		}
		if r.store.RuntimeEngine != nil && r.store.RuntimeEngine.OrderedDecimalTextStorage() {
			if encoded, err := recordmodel.RecordEncodeSQLiteDecimal(value, config); err == nil {
				return encoded
			}
		}
		if normalized, err := recordmodel.RecordNormalizeDecimal(value, config); err == nil {
			return normalized
		}
	}
	return metadataDBValue(value)
}

func (r ApplicationSchemaStore) tableColumns(ctx context.Context, table string) (map[string]bool, error) {
	return r.storage.Columns(ctx, r.database(), r.store.SQLRenderer, r.store.DatabaseSchema(), table)
}

func (r ApplicationSchemaStore) tableColumnTypes(ctx context.Context, table string) (map[string]string, error) {
	return r.storage.ColumnTypes(ctx, r.database(), r.store.SQLRenderer, r.store.DatabaseSchema(), table)
}

func metadataColumnTypeMatches(current, target string) bool {
	normalize := func(value string) string {
		return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(value), " ", ""))
	}
	return normalize(current) == normalize(target)
}

func metadataRequiresExactPhysicalType(field definitionmodel.FieldSchema) bool {
	switch strings.TrimSpace(field.Type) {
	case "currency", "percent", "integer", "file", "file_list", "multi_select", "json":
		return true
	default:
		return false
	}
}

func metadataPhysicalSchemaMismatch(objectKey, columnKey, expectedType, actualType string) error {
	return &appschemamodel.ApplicationSchemaPhysicalSchemaMismatchError{
		ObjectKey:    strings.TrimSpace(objectKey),
		ColumnKey:    strings.TrimSpace(columnKey),
		ExpectedType: strings.TrimSpace(expectedType),
		ActualType:   strings.TrimSpace(actualType),
	}
}

func (r ApplicationSchemaStore) tableIndexes(ctx context.Context, table string) (map[string]bool, error) {
	return r.storage.Indexes(ctx, r.database(), r.store.SQLRenderer, r.store.DatabaseSchema(), table)
}

func (r ApplicationSchemaStore) dropManagedIndex(ctx context.Context, table, index string) error {
	if err := r.storage.DropIndex(ctx, r.schemaDatabase(), r.store.SQLRenderer, table, index); err != nil {
		return fmt.Errorf("drop index %s: %w", index, err)
	}
	return nil
}
