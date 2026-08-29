// Metadata persistence.
package metadata

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	ormdialect "github.com/domainry/domainry-orm/dialect"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	manifestmodel "github.com/domainry/domainry-runtime/runtime/domain/manifest/model"
	metadatamodel "github.com/domainry/domainry-runtime/runtime/domain/metadata/model"
	metadatarepository "github.com/domainry/domainry-runtime/runtime/domain/metadata/repository"
	principalmodel "github.com/domainry/domainry-runtime/runtime/domain/principal/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"

	database "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database"
	metadatamysql "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/metadata/mysql"
	metadatapostgres "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/metadata/postgres"
	metadatasqlite "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/metadata/sqlite"
	metadatastorage "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/metadata/storage"
	runtimeschema "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/schema"
)

// MetadataStore is the request-aware storage boundary for metadata.
type MetadataStore struct {
	store                *database.RuntimeStore
	db                   *sql.DB
	schemaDB             runtimeschema.SQLDatabase
	createIndex          func(context.Context, string, string, bool, ...string) error
	storage              metadatastorage.Profile
	exactDecimalMigrator metadataExactDecimalMigrator
}

type metadataProfileFactory func() (metadatastorage.Profile, metadataExactDecimalMigrator)

var metadataProfileFactories = map[ormdialect.Name]metadataProfileFactory{
	ormdialect.SQLite: func() (metadatastorage.Profile, metadataExactDecimalMigrator) {
		return metadatasqlite.NewMetadataStorageProfile(), sqliteExactDecimalMigrator{}
	},
	ormdialect.MySQL: func() (metadatastorage.Profile, metadataExactDecimalMigrator) {
		return metadatamysql.NewMetadataStorageProfile(), mysqlExactDecimalMigrator{}
	},
	ormdialect.Postgres: func() (metadatastorage.Profile, metadataExactDecimalMigrator) {
		return metadatapostgres.NewMetadataStorageProfile(), postgresExactDecimalMigrator{}
	},
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
	factory := metadataProfileFactories[store.Engine.Name()]
	if factory == nil {
		panic(fmt.Sprintf("unsupported Metadata storage profile %q", store.Engine.Name()))
	}
	profile, migrator := factory()
	return MetadataStore{store: store, db: store.DB(), storage: profile, exactDecimalMigrator: migrator}
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
					if r.storage.ExactDecimalUpgradeAllowed(currentType, field) {
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
	// Core identity/time columns may be declared by metadata so callers can
	// query and sort them; they still resolve to the canonical physical columns.
	// The remaining Record metadata columns are infrastructure-owned and cannot
	// be redefined as business data.
	reserved := map[string]bool{
		"deleted": true, "ext_info": true, "create_by": true, "update_by": true,
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
	createStatement, createArgs, buildErr := ormbuilder.NewCreateTableBuilder(r.store.SQLRenderer, object.Key).IfNotExists().Build()
	if buildErr != nil {
		return fmt.Errorf("build object table %s: %w", object.Key, buildErr)
	}
	if _, err := schemaDB.ExecContext(ctx, createStatement, createArgs...); err != nil {
		return fmt.Errorf("ensure object table %s: %w", object.Key, err)
	}
	existing, err := r.tableColumns(ctx, object.Key)
	if err != nil {
		return err
	}
	if err := r.migrateLegacyRecordActorColumns(ctx, object.Key, existing); err != nil {
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
	if !existing["id"] {
		return fmt.Errorf("object %s is missing required Record system column id", object.Key)
	}
	for _, columnName := range []string{"deleted", "ext_info", "create_by", "update_by"} {
		if existing[columnName] {
			continue
		}
		column, ok := ormbuilder.RecordSystemColumn(columnName)
		if !ok {
			return fmt.Errorf("record system column %s is unavailable", columnName)
		}
		statement, args, buildErr := ormbuilder.NewAddColumnBuilder(r.store.SQLRenderer, object.Key, column).Build()
		if buildErr != nil {
			return fmt.Errorf("build record system column %s.%s: %w", object.Key, columnName, buildErr)
		}
		if _, err := schemaDB.ExecContext(ctx, statement, args...); err != nil {
			return fmt.Errorf("add record system column %s.%s: %w", object.Key, columnName, err)
		}
		existing[columnName] = true
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
		return r.createIndexIfMissing(ctx, table, indexName, true, plan.IndexFields...)
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

func (r MetadataStore) tableColumns(ctx context.Context, table string) (map[string]bool, error) {
	return r.storage.Columns(ctx, r.database(), r.store.SQLRenderer, r.store.DatabaseSchema(), table)
}

func (r MetadataStore) tableColumnTypes(ctx context.Context, table string) (map[string]string, error) {
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
	return r.storage.Indexes(ctx, r.database(), r.store.SQLRenderer, r.store.DatabaseSchema(), table)
}

func (r MetadataStore) dropManagedIndex(ctx context.Context, table, index string) error {
	if err := r.storage.DropIndex(ctx, r.schemaDatabase(), r.store.SQLRenderer, table, index); err != nil {
		return fmt.Errorf("drop index %s: %w", index, err)
	}
	return nil
}
