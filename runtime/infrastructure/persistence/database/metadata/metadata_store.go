// Metadata persistence.
package metadata

import (
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"

	"context"
	"database/sql"
	"fmt"

	"strings"

	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	metadatarepository "github.com/domainry/domainry-runtime/runtime/domain/metadata/repository"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	runtimeschema "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/schema"
)

// MetadataStore is the request-aware storage boundary for metadata.
type MetadataStore struct {
	store       *database.RuntimeStore
	db          *sql.DB
	schemaDB    runtimeschema.SQLDatabase
	createIndex func(context.Context, string, string, bool, ...string) error
}

var _ metadatarepository.MetadataRepository = MetadataStore{}

func (r MetadataStore) SnapshotRevision(ctx context.Context, scope principalmodel.SystemScope) (string, error) {
	if err := requireMetadataInstallationScope(scope); err != nil {
		return "", err
	}
	executor := database.ActionExecutionExecutor(r.database())
	if actionExecutor := database.ActionExecutionTransaction(ctx); actionExecutor != nil {
		executor = actionExecutor
	}
	var revision string
	err := executor.QueryRowContext(ctx, "SELECT "+r.store.Identifier("value")+" FROM "+r.store.TableIdentifier("metadata_catalog")+" WHERE "+r.store.Identifier("key")+" = "+r.store.Placeholder(1), "schema_hash").Scan(&revision)
	if err == sql.ErrNoRows {
		if refreshErr := r.refreshCatalogHashWithExecutor(ctx, executor); refreshErr != nil {
			return "", refreshErr
		}
		err = executor.QueryRowContext(ctx, "SELECT "+r.store.Identifier("value")+" FROM "+r.store.TableIdentifier("metadata_catalog")+" WHERE "+r.store.Identifier("key")+" = "+r.store.Placeholder(1), "schema_hash").Scan(&revision)
	}
	if err != nil {
		return "", fmt.Errorf("load metadata snapshot revision: %w", err)
	}
	return strings.TrimSpace(revision), nil
}

func NewMetadataStore(store *database.RuntimeStore) MetadataStore {
	return MetadataStore{store: store, db: store.DB()}
}

func (r MetadataStore) database() *sql.DB {
	if r.db != nil {
		return r.db
	}
	return r.store.DB()
}

func (r MetadataStore) schemaDatabase() runtimeschema.SQLDatabase {
	if r.schemaDB != nil {
		return r.schemaDB
	}
	return r.store.SchemaDB()
}

func (r MetadataStore) createIndexIfMissing(ctx context.Context, table, name string, unique bool, columns ...string) error {
	if r.createIndex != nil {
		return r.createIndex(ctx, table, name, unique, columns...)
	}
	return r.store.CreateIndexIfMissing(ctx, table, name, unique, columns...)
}

func recordMutationTxOptions() *sql.TxOptions {
	return &sql.TxOptions{Isolation: sql.LevelSerializable}
}

func placeholders(store *database.RuntimeStore, count int) []string {
	values := make([]string, 0, count)
	for position := 1; position <= count; position++ {
		values = append(values, store.Placeholder(position))
	}
	return values
}

func stringsJoinIdentifiers(store *database.RuntimeStore, columns ...string) string {
	return strings.Join(database.QuotedColumns(store, columns), ", ")
}

func joinIdentifiers(store *database.RuntimeStore, columns ...string) string {
	return stringsJoinIdentifiers(store, columns...)
}

func joinPlaceholders(store *database.RuntimeStore, count int) string {
	return strings.Join(placeholders(store, count), ", ")
}

func (r MetadataStore) MigrationPlan(ctx context.Context, scope principalmodel.SystemScope, manifest manifestmodel.ManifestSchema) ([]metadatamodel.MetadataMigrationStep, error) {
	if err := requireMetadataInstallationScope(scope); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	steps := []metadatamodel.MetadataMigrationStep{}
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
			steps = append(steps, metadatamodel.MetadataMigrationStep{ObjectKey: object.Key, Table: table, Operation: "create_table", Description: "backend.metadata.migration.createTable"})
			continue
		}
		existing := make(map[string]bool, len(existingTypes))
		for column := range existingTypes {
			existing[column] = true
		}
		for _, field := range object.Fields {
			column := strings.TrimSpace(field.Key)
			if column == "" || strings.TrimSpace(field.DisabledAt) != "" {
				continue
			}
			if currentType, ok := existingTypes[column]; ok {
				targetType := r.metadataSQLTypeForField(field)
				if metadataRequiresExactPhysicalType(field) && !metadataColumnTypeMatches(currentType, targetType) {
					if metadataExactDecimalUpgradeAllowed(r.store.Driver(), currentType, field) {
						steps = append(steps, metadatamodel.MetadataMigrationStep{ObjectKey: object.Key, Table: table, Operation: "alter_column_exact_decimal", ColumnKey: column, ColumnType: targetType, Reversible: false, Description: "backend.metadata.migration.exactDecimal"})
						continue
					}
					return nil, metadataPhysicalSchemaMismatch(object.Key, column, targetType, currentType)
				}
				continue
			}
			steps = append(steps, metadatamodel.MetadataMigrationStep{ObjectKey: object.Key, Table: table, Operation: "add_column", ColumnKey: column, ColumnType: r.metadataSQLTypeForField(field), Description: "backend.metadata.migration.addColumn"})
		}
	}
	return steps, nil
}

func (r MetadataStore) SyncManifest(ctx context.Context, scope principalmodel.SystemScope, manifest manifestmodel.ManifestSchema) error {
	if err := requireMetadataInstallationScope(scope); err != nil {
		return err
	}
	if err := r.migrateExactDecimalStorage(ctx, manifest); err != nil {
		return err
	}
	for _, object := range manifest.Objects {
		if strings.TrimSpace(object.Key) == "" {
			continue
		}
		if err := r.ensureObjectStorage(ctx, object); err != nil {
			return err
		}
	}
	return nil
}

func (r MetadataStore) ensureObjectStorage(ctx context.Context, object definitionmodel.ObjectSchema) error {
	schemaDB := r.schemaDatabase()
	constraintIndexed := metadataConstraintIndexedFields(object)
	columns := []string{r.store.Identifier("workspace_id") + " " + r.metadataIDColumnType() + " NOT NULL", r.store.Identifier("id") + " " + r.metadataIDColumnType() + " NOT NULL", r.store.Identifier("created_at") + " TEXT NOT NULL", r.store.Identifier("updated_at") + " TEXT NOT NULL"}
	createSQL := "CREATE TABLE IF NOT EXISTS " + r.store.TableIdentifier(object.Key) + " (\n  " + strings.Join(columns, ",\n  ") + "\n)"
	if _, err := schemaDB.ExecContext(ctx, createSQL); err != nil {
		return fmt.Errorf("ensure object table %s: %w", object.Key, err)
	}
	existing, err := r.tableColumns(ctx, object.Key)
	if err != nil {
		return err
	}
	var existingTypes map[string]string
	indexes, _ := r.tableIndexes(ctx, object.Key)
	if !existing["workspace_id"] {
		if _, err := schemaDB.ExecContext(ctx, "ALTER TABLE "+r.store.TableIdentifier(object.Key)+" ADD COLUMN "+r.store.Identifier("workspace_id")+" "+r.metadataIDColumnType()); err != nil {
			return fmt.Errorf("add column %s.workspace_id: %w", object.Key, err)
		}
		if _, err := schemaDB.ExecContext(ctx, "UPDATE "+r.store.TableIdentifier(object.Key)+" SET "+r.store.Identifier("workspace_id")+" = "+r.store.Placeholder(1)+" WHERE "+r.store.Identifier("workspace_id")+" IS NULL OR "+r.store.Identifier("workspace_id")+" = ''", principalmodel.InstallationWorkspaceID); err != nil {
			return fmt.Errorf("backfill column %s.workspace_id: %w", object.Key, err)
		}
		existing["workspace_id"] = true
	}
	if err := r.createIndexIfMissing(ctx, object.Key, r.metadataFieldIndexName(object.Key, "workspace_id_id", true), true, "workspace_id", "id"); err != nil {
		return fmt.Errorf("create workspace record identity index for %s: %w", object.Key, err)
	}
	for _, field := range object.Fields {
		field = metadataConstraintIndexedField(field, constraintIndexed[field.Key])
		fieldKey := strings.TrimSpace(field.Key)
		if fieldKey == "" || strings.TrimSpace(field.DisabledAt) != "" {
			continue
		}
		if existing[fieldKey] && metadataRequiresExactPhysicalType(field) {
			if existingTypes == nil {
				existingTypes, err = r.tableColumnTypes(ctx, object.Key)
				if err != nil {
					return err
				}
			}
			currentType := existingTypes[fieldKey]
			targetType := r.metadataSQLTypeForField(field)
			if !metadataColumnTypeMatches(currentType, targetType) {
				return metadataPhysicalSchemaMismatch(object.Key, fieldKey, targetType, currentType)
			}
		}
		if !existing[fieldKey] {
			query := "ALTER TABLE " + r.store.TableIdentifier(object.Key) + " ADD COLUMN " + r.store.Identifier(fieldKey) + " " + r.metadataSQLTypeForField(field)
			if _, err := schemaDB.ExecContext(ctx, query); err != nil {
				return fmt.Errorf("add column %s.%s: %w", object.Key, fieldKey, err)
			}
			existing[fieldKey] = true
		}
		defaultValue := field.DefaultValue
		if defaultValue == nil {
			defaultValue = field.Default
		}
		if defaultValue != nil {
			query := "UPDATE " + r.store.TableIdentifier(object.Key) + " SET " + r.store.Identifier(fieldKey) + " = " + r.store.Placeholder(1) + " WHERE " + r.store.Identifier(fieldKey) + " IS NULL"
			if _, err := schemaDB.ExecContext(ctx, query, metadataDBValue(defaultValue)); err != nil {
				return fmt.Errorf("backfill column %s.%s: %w", object.Key, fieldKey, err)
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
				if err := r.createIndexIfMissing(ctx, object.Key, uniqueIndexName, true, "workspace_id", fieldKey); err != nil {
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
				if err := r.createIndexIfMissing(ctx, object.Key, indexName, false, fieldKey); err != nil {
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
		if err := r.createIndexIfMissing(ctx, object.Key, indexName, true, fields...); err != nil {
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
		if r.store.Driver() == "mysql" {
			guard := conditionalUniqueGuardColumn(existingIndex)
			columns, columnErr := r.tableColumns(ctx, object.Key)
			if columnErr != nil {
				return columnErr
			}
			if columns[guard] {
				query := "ALTER TABLE " + r.store.TableIdentifier(object.Key) + " DROP COLUMN " + r.store.Identifier(guard)
				if _, columnErr := schemaDB.ExecContext(ctx, query); columnErr != nil {
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
		if err := r.createConditionalUniqueIndex(ctx, object.Key, indexName, policy); err != nil {
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
		if err := r.createIndexIfMissing(ctx, object.Key, indexName, false, fields...); err != nil {
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
		if err := r.createIndexIfMissing(ctx, object.Key, indexName, false, fields...); err != nil {
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

func (r MetadataStore) createConditionalUniqueIndex(ctx context.Context, table, indexName string, policy recordvalidation.RecordConditionalUniquePolicy) error {
	guard, guardSQL, partialSQL, fields := r.conditionalUniqueIndexDDL(table, indexName, policy)
	if r.store.Driver() == "mysql" {
		columns, err := r.tableColumns(ctx, table)
		if err != nil {
			return err
		}
		if !columns[guard] {
			if _, err := r.schemaDatabase().ExecContext(ctx, guardSQL); err != nil {
				return fmt.Errorf("add conditional unique guard %s: %w", guard, err)
			}
		}
		return r.createIndexIfMissing(ctx, table, indexName, true, fields...)
	}
	if _, err := r.schemaDatabase().ExecContext(ctx, partialSQL); err != nil {
		return fmt.Errorf("create partial unique index: %w", err)
	}
	return nil
}

func (r MetadataStore) conditionalUniqueIndexDDL(table, indexName string, policy recordvalidation.RecordConditionalUniquePolicy) (guard, guardSQL, partialSQL string, fields []string) {
	fields = append([]string{"workspace_id"}, policy.Fields...)
	values := make([]string, len(policy.ConditionValues))
	for index, value := range policy.ConditionValues {
		values[index] = "'" + strings.ReplaceAll(value, "'", "''") + "'"
	}
	condition := r.store.Identifier(policy.ConditionField) + " IN (" + strings.Join(values, ", ") + ")"
	if r.store.Driver() == "mysql" {
		guard = conditionalUniqueGuardColumn(indexName)
		guardSQL = "ALTER TABLE " + r.store.TableIdentifier(table) +
			" ADD COLUMN " + r.store.Identifier(guard) +
			" TINYINT GENERATED ALWAYS AS (CASE WHEN " + condition + " THEN 1 ELSE NULL END) STORED"
		fields = append(fields, guard)
		return guard, guardSQL, "", fields
	}
	quotedFields := make([]string, len(fields))
	for index, field := range fields {
		quotedFields[index] = r.store.Identifier(field)
	}
	partialSQL = "CREATE UNIQUE INDEX IF NOT EXISTS " + r.store.Identifier(indexName) +
		" ON " + r.store.TableIdentifier(table) + " (" + strings.Join(quotedFields, ", ") + ") WHERE " + condition
	return "", "", partialSQL, fields
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

func (r MetadataStore) tableColumns(ctx context.Context, table string) (map[string]bool, error) {
	return r.tableColumnsForDriver(ctx, table, r.store.Driver())
}

func (r MetadataStore) tableColumnsForDriver(ctx context.Context, table, driver string) (map[string]bool, error) {
	out := map[string]bool{}
	if driver == "sqlite" {
		rows, err := r.database().QueryContext(ctx, "PRAGMA table_info("+r.store.Identifier(table)+")")
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var cid int
			var name, dataType string
			var notNull, pk int
			var defaultValue any
			if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &pk); err != nil {
				return nil, err
			}
			out[name] = true
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("table not found: %s", table)
		}
		return out, rows.Err()
	}
	query := "SELECT column_name FROM information_schema.columns WHERE table_name = " + r.store.Placeholder(1)
	args := []any{table}
	if driver == "mysql" {
		query += " AND table_schema = DATABASE()"
	} else if driver == "postgres" {
		query = "SELECT column_name FROM information_schema.columns WHERE table_schema = " + r.store.Placeholder(1) + " AND table_name = " + r.store.Placeholder(2)
		args = []any{r.store.DatabaseSchema(), table}
	}
	rows, err := r.database().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = true
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("table not found: %s", table)
	}
	return out, rows.Err()
}

func (r MetadataStore) tableColumnTypes(ctx context.Context, table string) (map[string]string, error) {
	return r.tableColumnTypesForDriver(ctx, table, r.store.Driver())
}

func (r MetadataStore) tableColumnTypesForDriver(ctx context.Context, table, driver string) (map[string]string, error) {
	out := map[string]string{}
	if driver == "sqlite" {
		rows, err := r.database().QueryContext(ctx, "PRAGMA table_info("+r.store.Identifier(table)+")")
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var cid int
			var name, dataType string
			var notNull, pk int
			var defaultValue any
			if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &pk); err != nil {
				return nil, err
			}
			out[name] = dataType
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("table not found: %s", table)
		}
		return out, rows.Err()
	}
	query := "SELECT column_name, data_type, numeric_precision, numeric_scale FROM information_schema.columns WHERE table_name = " + r.store.Placeholder(1)
	args := []any{table}
	if driver == "mysql" {
		query += " AND table_schema = DATABASE()"
	} else if driver == "postgres" {
		query = "SELECT column_name, data_type, numeric_precision, numeric_scale FROM information_schema.columns WHERE table_schema = " + r.store.Placeholder(1) + " AND table_name = " + r.store.Placeholder(2)
		args = []any{r.store.DatabaseSchema(), table}
	}
	rows, err := r.database().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var name, dataType string
		var precision, scale sql.NullInt64
		if err := rows.Scan(&name, &dataType, &precision, &scale); err != nil {
			return nil, err
		}
		if precision.Valid && scale.Valid && (strings.EqualFold(dataType, "decimal") || strings.EqualFold(dataType, "numeric")) {
			dataType = fmt.Sprintf("%s(%d,%d)", dataType, precision.Int64, scale.Int64)
		}
		out[name] = dataType
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("table not found: %s", table)
	}
	return out, rows.Err()
}

func metadataColumnTypeMatches(current, target string) bool {
	normalize := func(value string) string {
		return strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(value), " ", ""))
	}
	return normalize(current) == normalize(target)
}

func metadataRequiresExactPhysicalType(field definitionmodel.FieldSchema) bool {
	switch strings.TrimSpace(field.Type) {
	case "currency", "percent", "integer":
		return true
	default:
		return false
	}
}

func metadataPhysicalSchemaMismatch(objectKey, columnKey, expectedType, actualType string) error {
	return &metadatamodel.MetadataPhysicalSchemaMismatchError{
		ObjectKey:    strings.TrimSpace(objectKey),
		ColumnKey:    strings.TrimSpace(columnKey),
		ExpectedType: strings.TrimSpace(expectedType),
		ActualType:   strings.TrimSpace(actualType),
	}
}

func (r MetadataStore) tableIndexes(ctx context.Context, table string) (map[string]bool, error) {
	return r.tableIndexesForDriver(ctx, table, r.store.Driver())
}

func (r MetadataStore) tableIndexesForDriver(ctx context.Context, table, driver string) (map[string]bool, error) {
	out := map[string]bool{}
	query, args := "SELECT name FROM sqlite_master WHERE type='index' AND tbl_name="+r.store.Placeholder(1), []any{table}
	if driver == "mysql" {
		query = "SELECT DISTINCT index_name FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = " + r.store.Placeholder(1)
	} else if driver == "postgres" {
		query = "SELECT indexname FROM pg_indexes WHERE schemaname = " + r.store.Placeholder(1) + " AND tablename = " + r.store.Placeholder(2)
		args = []any{r.store.DatabaseSchema(), table}
	}
	rows, err := r.database().QueryContext(ctx, query, args...)
	if err != nil {
		// Index introspection is best-effort during schema reconciliation; the
		// table may have been created moments earlier by this same operation.
		return out, nil
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		out[name] = true
	}
	return out, rows.Err()
}

func (r MetadataStore) dropManagedIndex(ctx context.Context, table, index string) error {
	return r.dropManagedIndexForDriver(ctx, table, index, r.store.Driver())
}

func (r MetadataStore) dropManagedIndexForDriver(ctx context.Context, table, index, driver string) error {
	query := "DROP INDEX IF EXISTS " + r.store.Identifier(index)
	if driver == "mysql" {
		// MySQL has no DROP INDEX IF EXISTS form on this path. Callers derive
		// existence from information_schema and only invoke this operation for
		// a present managed index.
		query = "DROP INDEX " + r.store.Identifier(index) + " ON " + r.store.TableIdentifier(table)
	}
	if _, err := r.schemaDatabase().ExecContext(ctx, query); err != nil {
		return fmt.Errorf("drop index %s: %w", index, err)
	}
	return nil
}
