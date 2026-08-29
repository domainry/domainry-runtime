package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	ormbuilder "github.com/domainry/domainry-orm/builder"
	definitionmodel "github.com/domainry/domainry-runtime/runtime/domain/definition/model"
	recordvalidation "github.com/domainry/domainry-runtime/runtime/domain/record/validation"
	metadatastorage "github.com/domainry/domainry-runtime/runtime/infrastructure/persistence/database/metadata/storage"
)

type MetadataStorageProfile struct{}

func NewMetadataStorageProfile() MetadataStorageProfile { return MetadataStorageProfile{} }
func (MetadataStorageProfile) IDColumnType() string     { return "VARCHAR(191)" }
func (MetadataStorageProfile) FieldColumnType(field definitionmodel.FieldSchema, indexed bool) string {
	switch strings.TrimSpace(field.Type) {
	case "integer":
		return "BIGINT"
	case "currency", "percent":
		config := metadatastorage.DecimalConfig(field)
		return fmt.Sprintf("DECIMAL(%d,%d)", config.Precision, config.Scale)
	case "number":
		return "DOUBLE"
	case "boolean":
		return "BOOLEAN"
	case "json":
		return "JSON"
	default:
		if strings.TrimSpace(field.Type) != "long_text" && indexed {
			return fmt.Sprintf("VARCHAR(%d)", metadatastorage.IndexedTextLength(field))
		}
		return "TEXT"
	}
}
func (MetadataStorageProfile) Columns(ctx context.Context, queryer metadatastorage.Queryer, renderer ormbuilder.Renderer, _ string, table string) (map[string]bool, error) {
	rows, err := queryer.QueryContext(ctx, "SELECT column_name FROM information_schema.columns WHERE table_name = "+renderer.Placeholder(1)+" AND table_schema = DATABASE()", table)
	return metadatastorage.ReadColumns(rows, err, table)
}
func (MetadataStorageProfile) ColumnTypes(ctx context.Context, queryer metadatastorage.Queryer, renderer ormbuilder.Renderer, _ string, table string) (map[string]string, error) {
	rows, err := queryer.QueryContext(ctx, "SELECT column_name, data_type, numeric_precision, numeric_scale FROM information_schema.columns WHERE table_name = "+renderer.Placeholder(1)+" AND table_schema = DATABASE()", table)
	return metadatastorage.ReadColumnTypes(rows, err, table)
}
func (MetadataStorageProfile) Indexes(ctx context.Context, queryer metadatastorage.Queryer, renderer ormbuilder.Renderer, _ string, table string) (map[string]bool, error) {
	rows, err := queryer.QueryContext(ctx, "SELECT DISTINCT index_name FROM information_schema.statistics WHERE table_schema = DATABASE() AND table_name = "+renderer.Placeholder(1), table)
	return metadatastorage.ReadIndexes(rows, err)
}
func (MetadataStorageProfile) DropIndex(ctx context.Context, executor metadatastorage.Executor, renderer ormbuilder.Renderer, table, index string) error {
	_, err := executor.ExecContext(ctx, "DROP INDEX "+renderer.Identifier(index)+" ON "+renderer.Table(table))
	return err
}
func (MetadataStorageProfile) ConditionalUniquePlan(renderer ormbuilder.Renderer, table, index string, policy recordvalidation.RecordConditionalUniquePolicy) metadatastorage.ConditionalUniquePlan {
	guard := metadatastorage.GuardColumn(index)
	fields := append(metadatastorage.ConditionalUniqueFields(policy), guard)
	statement := "ALTER TABLE " + renderer.Table(table) + " ADD COLUMN " + renderer.Identifier(guard) + " TINYINT GENERATED ALWAYS AS (CASE WHEN " + metadatastorage.ConditionalUniqueCondition(renderer, policy) + " THEN 1 ELSE NULL END) STORED"
	return metadatastorage.ConditionalUniquePlan{GuardColumn: guard, AddGuardStatement: statement, IndexFields: fields}
}
func (MetadataStorageProfile) ConditionalUniqueGuard(index string) string {
	return metadatastorage.GuardColumn(index)
}
func (MetadataStorageProfile) DropColumn(ctx context.Context, executor metadatastorage.Executor, renderer ormbuilder.Renderer, table, column string) error {
	_, err := executor.ExecContext(ctx, "ALTER TABLE "+renderer.Table(table)+" DROP COLUMN "+renderer.Identifier(column))
	return err
}
func (MetadataStorageProfile) ExactDecimalUpgradeAllowed(current string, field definitionmodel.FieldSchema) bool {
	if kind := strings.TrimSpace(field.Type); kind != "currency" && kind != "percent" {
		return false
	}
	normalized := metadatastorage.NormalizePhysicalType(current)
	return strings.HasPrefix(normalized, "FLOAT") || strings.HasPrefix(normalized, "DOUBLE") || strings.HasPrefix(normalized, "DECIMAL(") || strings.HasPrefix(normalized, "NUMERIC(")
}

func (MetadataStorageProfile) ExactDecimalPreflightSQL(table, column, target string) string {
	return "SELECT COUNT(*) FROM " + table + " WHERE " + column + " IS NOT NULL AND " + column + " <> CAST(" + column + " AS " + target + ")"
}

func (MetadataStorageProfile) ExactDecimalAlterSQL(table string, definitions []string) string {
	return "ALTER TABLE " + table + " " + strings.Join(definitions, ", ") + ", ALGORITHM=COPY"
}

func (MetadataStorageProfile) ExactDecimalColumnDefinition(ctx context.Context, queryer metadatastorage.QueryRower, table, column string) (string, sql.NullString, error) {
	var nullable string
	var defaultValue sql.NullString
	err := queryer.QueryRowContext(ctx, "SELECT is_nullable, column_default FROM information_schema.columns WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?", table, column).Scan(&nullable, &defaultValue)
	return nullable, defaultValue, err
}
